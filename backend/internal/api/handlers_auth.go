package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/marcos14/contextforge/backend/internal/auth"
	"github.com/marcos14/contextforge/backend/internal/store"
)

// ============== Auth ==============

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type tokenPair struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Role    string `json:"role"`
	UserID  string `json:"user_id"`
	Email   string `json:"email"`
}

func (a *API) Login(w http.ResponseWriter, r *http.Request) {
	var in loginReq
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	var u store.User
	err := a.Pool.QueryRow(r.Context(),
		`SELECT id, email, password_hash, role, disabled FROM users WHERE email=$1`,
		in.Email).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.Disabled)
	if err != nil || u.Disabled {
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err := auth.VerifyPassword(u.PasswordHash, in.Password); err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	access, _ := a.Signer.IssueAccess(u.ID, string(u.Role))
	refresh, _ := a.Signer.IssueRefresh(u.ID, string(u.Role))
	writeJSON(w, http.StatusOK, tokenPair{Access: access, Refresh: refresh, Role: string(u.Role), UserID: u.ID.String(), Email: u.Email})
}

func (a *API) Refresh(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Refresh string `json:"refresh_token"`
	}
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	c, err := a.Signer.Verify(in.Refresh)
	if err != nil || c.Type != "refresh" {
		writeErr(w, http.StatusUnauthorized, "invalid refresh")
		return
	}
	access, _ := a.Signer.IssueAccess(c.UserID, c.Role)
	writeJSON(w, http.StatusOK, map[string]any{"access_token": access})
}

// ============== Users (admin only) ==============

type createUserReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (a *API) CreateUser(w http.ResponseWriter, r *http.Request) {
	var in createUserReq
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if in.Role != "admin" && in.Role != "editor" && in.Role != "viewer" {
		writeErr(w, http.StatusBadRequest, "invalid role")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	id := uuid.New()
	_, err = a.Pool.Exec(r.Context(),
		`INSERT INTO users (id, email, password_hash, role) VALUES ($1,$2,$3,$4)`,
		id, strings.ToLower(in.Email), hash, in.Role)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (a *API) ListUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.Pool.Query(r.Context(),
		`SELECT id, email, role, disabled, created_at FROM users ORDER BY created_at DESC`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id uuid.UUID
		var email, role string
		var disabled bool
		var created time.Time
		_ = rows.Scan(&id, &email, &role, &disabled, &created)
		out = append(out, map[string]any{
			"id": id, "email": email, "role": role, "disabled": disabled, "created_at": created,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// ============== Health / Me ==============

func (a *API) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (a *API) Me(w http.ResponseWriter, r *http.Request) {
	uid, role, ok := currentUser(r.Context())
	if !ok {
		writeErr(w, http.StatusUnauthorized, "no session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": uid, "role": role})
}

// nullDecode is a small helper used by other modules to unmarshal raw JSON
// fields into a json.RawMessage even when null.
func nullDecode(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}
