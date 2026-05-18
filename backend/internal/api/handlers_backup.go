package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/marcos14/contextforge/backend/internal/backup"
)

const maxBackupUploadBytes = 32 * 1024 * 1024 // 32 MiB

// BackupExport accepts a JSON Selection and streams an encrypted backup file.
func (a *API) BackupExport(w http.ResponseWriter, r *http.Request) {
	var sel backup.Selection
	if r.ContentLength > 0 {
		if err := decodeBody(r, &sel); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid selection: "+err.Error())
			return
		}
	} else {
		sel.Global = true
	}
	uid, _, _ := currentUser(r.Context())
	svc := backup.New(a.Pool, a.Cipher)
	blob, err := svc.Export(r.Context(), sel, uid.String())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.audit(r, "backup.export", "", map[string]any{
		"global":         sel.Global,
		"connection_ids": sel.ConnectionIDs,
		"group_ids":      sel.GroupIDs,
		"tool_ids":       sel.ToolIDs,
		"bytes":          len(blob),
	})

	filename := fmt.Sprintf("contextforge-%s.mcpbak", time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(blob)))
	_, _ = w.Write(blob)
}

// BackupPreview accepts a multipart upload (field "file"), decrypts and parses
// the manifest, and returns a per-item status diff.
func (a *API) BackupPreview(w http.ResponseWriter, r *http.Request) {
	data, err := readBackupUpload(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	svc := backup.New(a.Pool, a.Cipher)
	m, err := svc.Parse(data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	prev, err := svc.Preview(r.Context(), m)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prev)
}

// BackupRestore accepts a multipart upload with fields "file" (the backup) and
// "resolutions" (JSON array of ItemResolution). Applies the restore in a
// single transaction and returns a per-item report.
func (a *API) BackupRestore(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxBackupUploadBytes); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid multipart body: "+err.Error())
		return
	}
	data, err := readBackupUpload(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var resolutions []backup.ItemResolution
	if raw := r.FormValue("resolutions"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &resolutions); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid resolutions: "+err.Error())
			return
		}
	}

	svc := backup.New(a.Pool, a.Cipher)
	m, err := svc.Parse(data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rep, err := svc.Apply(r.Context(), m, resolutions)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	a.audit(r, "backup.restore", "", map[string]any{
		"counts":             rep.Counts,
		"source_fingerprint": m.SourceMasterKeyFingerprint,
		"exported_at":        m.ExportedAt,
	})

	writeJSON(w, http.StatusOK, rep)
}

// readBackupUpload reads the "file" field from a multipart form (parsed by the
// caller for the restore endpoint) or, for the preview endpoint, parses the
// multipart form here.
func readBackupUpload(r *http.Request) ([]byte, error) {
	if r.MultipartForm == nil {
		if err := r.ParseMultipartForm(maxBackupUploadBytes); err != nil {
			return nil, fmt.Errorf("invalid multipart body: %w", err)
		}
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		return nil, fmt.Errorf("missing form file %q: %w", "file", err)
	}
	defer f.Close()
	if header.Size > maxBackupUploadBytes {
		return nil, fmt.Errorf("backup file is too large (max %d bytes)", maxBackupUploadBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBackupUploadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBackupUploadBytes {
		return nil, fmt.Errorf("backup file is too large (max %d bytes)", maxBackupUploadBytes)
	}
	return data, nil
}

// audit writes a row to audit_logs. Failures are logged-and-swallowed since
// they should never block the user action.
func (a *API) audit(r *http.Request, action, target string, details map[string]any) {
	uid, _, _ := currentUser(r.Context())
	var actor *uuid.UUID
	if uid != uuid.Nil {
		actor = &uid
	}
	js, _ := json.Marshal(details)
	_, _ = a.Pool.Exec(r.Context(),
		`INSERT INTO audit_logs (actor_id, action, target, details) VALUES ($1,$2,$3,$4)`,
		actor, action, nullIfEmpty(target), js)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
