package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marcos14/contextforge/backend/internal/llm"
)

func TestClampPreviewLimit(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, queryStudioPreviewMaxRows},  // unset → forced max
		{-5, queryStudioPreviewMaxRows}, // negative → forced max
		{10, 10},                        // within range → honoured
		{queryStudioPreviewMaxRows, queryStudioPreviewMaxRows},
		{queryStudioPreviewMaxRows + 1, queryStudioPreviewMaxRows}, // over → capped
		{100000, queryStudioPreviewMaxRows},                        // absurd → capped
	}
	for _, c := range cases {
		if got := clampPreviewLimit(c.in); got != c.want {
			t.Errorf("clampPreviewLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// postJSON drives a handler with a JSON body and returns the recorder. The
// handlers under test validate the query and required fields before touching
// any dependency, so a zero-value *API is enough to exercise those paths.
func postJSON(t *testing.T, h http.HandlerFunc, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(buf))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	s, _ := m["error"].(string)
	return s
}

// nonSelectQueries must all be rejected by the SELECT-only enforcement before
// any driver/DB access, so they are safe to run against a zero-value *API.
var nonSelectQueries = []string{
	"DELETE FROM users",
	"UPDATE users SET name='x'",
	"DROP TABLE users",
	"INSERT INTO users VALUES (1)",
	"TRUNCATE users",
	"EXPLAIN SELECT 1",           // raw EXPLAIN is rejected: the backend wraps it
	"SHOW TABLES",                // SHOW is rejected too
	"SELECT 1; DROP TABLE users", // multi-statement
	"",                           // empty
}

func TestQueryStudioPreview_RejectsNonSelect(t *testing.T) {
	a := &API{}
	for _, q := range nonSelectQueries {
		rec := postJSON(t, a.QueryStudioPreview, queryStudioPreviewReq{Query: q})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("preview query %q: status = %d, want 400 (body %q)", q, rec.Code, rec.Body.String())
		}
	}
}

func TestQueryStudioExplain_RejectsNonSelect(t *testing.T) {
	a := &API{}
	for _, q := range nonSelectQueries {
		rec := postJSON(t, a.QueryStudioExplain, queryStudioExplainReq{Query: q, Analyze: true})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("explain query %q: status = %d, want 400 (body %q)", q, rec.Code, rec.Body.String())
		}
	}
}

// A valid SELECT must pass SELECT-only enforcement and then fail on the missing
// connection guard — proving valid queries are NOT rejected by the parser and
// that a connection is required before any driver work.
func TestQueryStudioPreview_ValidSelectRequiresConnection(t *testing.T) {
	a := &API{}
	rec := postJSON(t, a.QueryStudioPreview, queryStudioPreviewReq{
		Query:    "SELECT id, name FROM users WHERE active = true",
		RowLimit: 5000, // over the cap; must not affect the connection-guard path
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if msg := decodeErr(t, rec); !strings.Contains(msg, "connection_id") {
		t.Errorf("error = %q, want it to mention connection_id", msg)
	}
}

func TestQueryStudioExplain_ValidSelectRequiresConnection(t *testing.T) {
	a := &API{}
	rec := postJSON(t, a.QueryStudioExplain, queryStudioExplainReq{
		Query: "WITH t AS (SELECT 1) SELECT * FROM t",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if msg := decodeErr(t, rec); !strings.Contains(msg, "connection_id") {
		t.Errorf("error = %q, want it to mention connection_id", msg)
	}
}

// When EXPLAIN ANALYZE is disabled by configuration, a valid SELECT with
// analyze:true must be rejected with 403 BEFORE any connection/driver work,
// regardless of whether a connection is supplied.
func TestQueryStudioExplain_AnalyzeDisabled(t *testing.T) {
	a := &API{AllowAnalyze: false}
	rec := postJSON(t, a.QueryStudioExplain, queryStudioExplainReq{
		Query:   "SELECT id FROM users",
		Analyze: true,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %q)", rec.Code, rec.Body.String())
	}
	if msg := decodeErr(t, rec); !strings.Contains(strings.ToLower(msg), "disabled") {
		t.Errorf("error = %q, want it to mention the feature is disabled", msg)
	}
}

// With ANALYZE disabled, a non-analyze EXPLAIN must NOT be blocked by the flag:
// it proceeds past the flag check and stops on the missing-connection guard.
func TestQueryStudioExplain_AnalyzeDisabled_PlainExplainAllowed(t *testing.T) {
	a := &API{AllowAnalyze: false}
	rec := postJSON(t, a.QueryStudioExplain, queryStudioExplainReq{
		Query:   "SELECT id FROM users",
		Analyze: false,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
	if msg := decodeErr(t, rec); !strings.Contains(msg, "connection_id") {
		t.Errorf("error = %q, want it to mention connection_id", msg)
	}
}

// With ANALYZE enabled, a valid analyze:true request passes the flag check and
// stops on the missing-connection guard (proving the flag does not block it).
func TestQueryStudioExplain_AnalyzeEnabled(t *testing.T) {
	a := &API{AllowAnalyze: true}
	rec := postJSON(t, a.QueryStudioExplain, queryStudioExplainReq{
		Query:   "SELECT id FROM users",
		Analyze: true,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
	if msg := decodeErr(t, rec); !strings.Contains(msg, "connection_id") {
		t.Errorf("error = %q, want it to mention connection_id", msg)
	}
}

func TestQueryStudioCapabilities(t *testing.T) {
	for _, allow := range []bool{true, false} {
		a := &API{AllowAnalyze: allow}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		a.QueryStudioCapabilities(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("allow=%v: status = %d, want 200", allow, rec.Code)
		}
		var got queryStudioCapabilities
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("allow=%v: decode %q: %v", allow, rec.Body.String(), err)
		}
		if got.AllowAnalyze != allow {
			t.Errorf("allow=%v: AllowAnalyze = %v, want %v", allow, got.AllowAnalyze, allow)
		}
	}
}

func TestQueryStudioChat_RequiresMessages(t *testing.T) {
	a := &API{}
	rec := postJSON(t, a.QueryStudioChat, queryStudioChatReq{})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
}

func TestQueryStudioChat_LLMNotConfigured(t *testing.T) {
	a := &API{} // LLM is nil
	rec := postJSON(t, a.QueryStudioChat, queryStudioChatReq{
		Messages: []llm.Message{{Role: "user", Content: "faturamento por cliente"}},
	})
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (body %q)", rec.Code, rec.Body.String())
	}
}
