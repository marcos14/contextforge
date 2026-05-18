package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/marcos14/contextforge/backend/internal/drivers"
	"github.com/marcos14/contextforge/backend/internal/store"
)

// ============== Connections ==============

type connReq struct {
	Name        string               `json:"name"`
	Type        store.ConnectionType `json:"type"`
	Description string               `json:"description"`
	Config      json.RawMessage      `json:"config"` // plain JSON; encrypted before storage
}

func (a *API) CreateConnection(w http.ResponseWriter, r *http.Request) {
	var in connReq
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if in.Name == "" || in.Type == "" || len(in.Config) == 0 {
		writeErr(w, http.StatusBadRequest, "name, type and config are required")
		return
	}
	id := uuid.New()
	enc, err := a.Cipher.Encrypt(in.Config, []byte("connection:"+id.String()))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	uid, _, _ := currentUser(r.Context())
	_, err = a.Pool.Exec(r.Context(), `
INSERT INTO connections (id, name, type, encrypted_config, description, created_by)
VALUES ($1,$2,$3,$4,$5,$6)`,
		id, in.Name, in.Type, enc, in.Description, uid)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (a *API) ListConnections(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	where := ""
	args := []any{}
	if p.Q != "" {
		where = ` WHERE (name ILIKE $1 OR type::text ILIKE $1 OR description ILIKE $1)`
		args = append(args, "%"+p.Q+"%")
	}

	var total int
	if p.Paged {
		if err := a.Pool.QueryRow(r.Context(),
			`SELECT count(*) FROM connections`+where, args...).Scan(&total); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	sql := `SELECT id, name, type, description, created_at FROM connections` + where + ` ORDER BY name`
	if p.Paged {
		sql += fmt.Sprintf(" LIMIT %d OFFSET %d", p.PageSize, p.Offset())
	}
	rows, err := a.Pool.Query(r.Context(), sql, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		m := map[string]any{}
		var id uuid.UUID
		var name, typ, desc string
		var created interface{}
		_ = rows.Scan(&id, &name, &typ, &desc, &created)
		m["id"], m["name"], m["type"], m["description"], m["created_at"] = id, name, typ, desc, created
		out = append(out, m)
	}
	if p.Paged {
		writePage(w, out, total, p)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) TestConnection(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	drv, err := a.openDriver(r, id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer drv.Close()
	if err := drv.Ping(r.Context()); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) IntrospectConnection(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	drv, err := a.openDriver(r, id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer drv.Close()
	tables, err := drv.Introspect(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tables)
}

func (a *API) GetConnection(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var name, kind, desc string
	var enc []byte
	err = a.Pool.QueryRow(r.Context(),
		`SELECT name, type, description, encrypted_config FROM connections WHERE id=$1`, id).
		Scan(&name, &kind, &desc, &enc)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	plain, err := a.Cipher.Decrypt(enc, []byte("connection:"+id.String()))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var cfg any
	if err := json.Unmarshal(plain, &cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          id,
		"name":        name,
		"type":        kind,
		"description": desc,
		"config":      cfg,
	})
}

func (a *API) UpdateConnection(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var in connReq
	if err := decodeBody(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if in.Name == "" || in.Type == "" || len(in.Config) == 0 {
		writeErr(w, http.StatusBadRequest, "name, type and config are required")
		return
	}
	enc, err := a.Cipher.Encrypt(in.Config, []byte("connection:"+id.String()))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := a.Pool.Exec(r.Context(), `
UPDATE connections
   SET name=$2, type=$3, encrypted_config=$4, description=$5, updated_at=now()
 WHERE id=$1`,
		id, in.Name, in.Type, enc, in.Description)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if res.RowsAffected() == 0 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	a.Executor.InvalidateDriver(id)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (a *API) DeleteConnection(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	_, err = a.Pool.Exec(r.Context(), `DELETE FROM connections WHERE id=$1`, id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.Executor.InvalidateDriver(id)
	w.WriteHeader(http.StatusNoContent)
}

// openDriver decrypts and builds an ad-hoc driver for one-off operations
// (ping, introspect). Callers must Close().
func (a *API) openDriver(r *http.Request, id uuid.UUID) (drivers.Driver, error) {
	var kind string
	var enc []byte
	err := a.Pool.QueryRow(r.Context(),
		`SELECT type, encrypted_config FROM connections WHERE id=$1`, id).
		Scan(&kind, &enc)
	if err != nil {
		return nil, err
	}
	plain, err := a.Cipher.Decrypt(enc, []byte("connection:"+id.String()))
	if err != nil {
		return nil, err
	}
	return drivers.Build(kind, plain)
}
