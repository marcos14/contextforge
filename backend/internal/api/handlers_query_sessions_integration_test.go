//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marcos14/contextforge/backend/internal/store"
)

// TestQuerySessions_CRUD_Integration exercises the full session lifecycle
// (create → list paged → get → update → delete → 404) against a real Postgres.
// Gated by the `integration` build tag AND self-skips without
// QUERY_STUDIO_TEST_PG_DSN.
//
// Run with:
//
//	QUERY_STUDIO_TEST_PG_DSN="postgres://user:pass@host:5432/db?sslmode=disable" \
//	  go test -tags=integration ./internal/api/ -run TestQuerySessions_CRUD_Integration -v
func TestQuerySessions_CRUD_Integration(t *testing.T) {
	dsn := os.Getenv("QUERY_STUDIO_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("QUERY_STUDIO_TEST_PG_DSN not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	if err := store.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	a := &API{Pool: pool}

	// Provision an owner user and a target connection (both are FK targets).
	var uid, connID uuid.UUID
	email := "qs-sess-" + uuid.NewString() + "@test.local"
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, role) VALUES ($1,'x','editor') RETURNING id`,
		email).Scan(&uid); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	connName := "qs-sess-conn-" + uuid.NewString()
	if err := pool.QueryRow(ctx,
		`INSERT INTO connections (name, type, encrypted_config) VALUES ($1,'pg',$2) RETURNING id`,
		connName, []byte{0x00}).Scan(&connID); err != nil {
		t.Fatalf("insert connection: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM query_sessions WHERE created_by=$1`, uid)
		_, _ = pool.Exec(context.Background(), `DELETE FROM connections WHERE id=$1`, connID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid)
	}()

	withUser := func(r *http.Request) *http.Request {
		c := context.WithValue(r.Context(), ctxKeyUserID, uid)
		c = context.WithValue(c, ctxKeyRole, "editor")
		return r.WithContext(c)
	}
	post := func(body any) *httptest.ResponseRecorder {
		buf, _ := json.Marshal(body)
		req := withUser(httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(buf)))
		rec := httptest.NewRecorder()
		a.SaveQuerySession(rec, req)
		return rec
	}
	withID := func(r *http.Request, id string) *http.Request {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}

	// --- Create ---
	rec := post(querySessionReq{
		ConnectionID: connID,
		Title:        "Faturamento",
		QueryText:    "SELECT 1",
		ChatLog:      json.RawMessage(`[{"role":"user","content":"oi"}]`),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d (body %q)", rec.Code, rec.Body.String())
	}
	var created store.QuerySession
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ID == uuid.Nil || created.Title != "Faturamento" || created.CreatedBy == nil || *created.CreatedBy != uid {
		t.Fatalf("unexpected created session: %+v", created)
	}

	// --- List (paged) ---
	rec = httptest.NewRecorder()
	a.ListQuerySessions(rec, withUser(httptest.NewRequest(http.MethodGet, "/?page=1&page_size=10", nil)))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var page struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("expected 1 session, got total=%d items=%d", page.Total, len(page.Items))
	}

	// --- Get ---
	rec = httptest.NewRecorder()
	a.GetQuerySession(rec, withID(withUser(httptest.NewRequest(http.MethodGet, "/", nil)), created.ID.String()))
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d (body %q)", rec.Code, rec.Body.String())
	}
	var got store.QuerySession
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode got: %v", err)
	}
	if string(got.ChatLog) == "" || got.QueryText != "SELECT 1" {
		t.Fatalf("unexpected fetched session: %+v", got)
	}

	// --- Update (same id in body) ---
	rec = post(querySessionReq{
		ID:           created.ID,
		ConnectionID: connID,
		Title:        "Faturamento v2",
		QueryText:    "SELECT 2",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d (body %q)", rec.Code, rec.Body.String())
	}
	var updated store.QuerySession
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if updated.ID != created.ID || updated.Title != "Faturamento v2" || updated.QueryText != "SELECT 2" {
		t.Fatalf("unexpected updated session: %+v", updated)
	}

	// --- Delete ---
	rec = httptest.NewRecorder()
	a.DeleteQuerySession(rec, withID(withUser(httptest.NewRequest(http.MethodDelete, "/", nil)), created.ID.String()))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d (body %q)", rec.Code, rec.Body.String())
	}

	// --- Get after delete → 404 ---
	rec = httptest.NewRecorder()
	a.GetQuerySession(rec, withID(withUser(httptest.NewRequest(http.MethodGet, "/", nil)), created.ID.String()))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get-after-delete status = %d, want 404", rec.Code)
	}
}
