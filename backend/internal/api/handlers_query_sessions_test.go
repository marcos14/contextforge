package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The save handler validates title and connection_id BEFORE any DB access, so a
// zero-value *API is enough to exercise those rejection paths.

func TestSaveQuerySession_RequiresTitle(t *testing.T) {
	a := &API{}
	rec := postJSON(t, a.SaveQuerySession, querySessionReq{
		ConnectionID: uuid.New(),
		Title:        "   ", // whitespace only → treated as empty
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
	if msg := decodeErr(t, rec); !strings.Contains(msg, "title") {
		t.Errorf("error = %q, want it to mention title", msg)
	}
}

func TestSaveQuerySession_RequiresConnection(t *testing.T) {
	a := &API{}
	rec := postJSON(t, a.SaveQuerySession, querySessionReq{
		Title: "Faturamento por cliente",
		// ConnectionID omitted → uuid.Nil
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
	if msg := decodeErr(t, rec); !strings.Contains(msg, "connection_id") {
		t.Errorf("error = %q, want it to mention connection_id", msg)
	}
}

// Get/Delete reject a non-UUID id before touching the DB. Without a chi route
// context the {id} param resolves to "", which fails to parse.
func TestGetQuerySession_BadID(t *testing.T) {
	a := &API{}
	rec := httptest.NewRecorder()
	a.GetQuerySession(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
}

func TestDeleteQuerySession_BadID(t *testing.T) {
	a := &API{}
	rec := httptest.NewRecorder()
	a.DeleteQuerySession(rec, httptest.NewRequest(http.MethodDelete, "/", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
}
