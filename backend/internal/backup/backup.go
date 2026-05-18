// Package backup implements export and restore of ContextForge configuration
// (connections, tool groups, tools and tool versions) as a single encrypted
// file. The blob is encrypted with the server's master key via the crypto
// package, so backups are portable across instances ONLY when they share the
// same MASTER_KEY.
//
// File layout:
//
//	[4 bytes magic "MCPB"][1 byte version=1][N bytes AES-GCM blob]
//
// The decrypted payload is a UTF-8 JSON Manifest.
package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marcos14/contextforge/backend/internal/crypto"
)

// ---- File framing ---------------------------------------------------------

var magic = []byte{'M', 'C', 'P', 'B'}

const (
	fileVersion byte   = 1
	aad         string = "contextforge-backup:v1"
)

// ---- Manifest DTOs --------------------------------------------------------

// Manifest is the decrypted JSON payload of a backup file.
type Manifest struct {
	Format                     string        `json:"format"`
	Version                    int           `json:"version"`
	ExportedAt                 time.Time     `json:"exported_at"`
	ExportedBy                 string        `json:"exported_by,omitempty"`
	SourceMasterKeyFingerprint string        `json:"source_master_key_fingerprint"`
	Items                      ManifestItems `json:"items"`
	Note                       string        `json:"note,omitempty"`
}

type ManifestItems struct {
	Connections  []ConnectionDTO  `json:"connections"`
	ToolGroups   []GroupDTO       `json:"tool_groups"`
	Tools        []ToolDTO        `json:"tools"`
	ToolVersions []ToolVersionDTO `json:"tool_versions"`
}

type ConnectionDTO struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	Type            string    `json:"type"`
	Description     string    `json:"description"`
	EncryptedConfig []byte    `json:"encrypted_config"` // base64 in JSON
	CreatedAt       time.Time `json:"created_at"`
}

type GroupDTO struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	HiddenByDefault bool      `json:"hidden_by_default"`
	CreatedAt       time.Time `json:"created_at"`
}

type ToolDTO struct {
	ID            uuid.UUID       `json:"id"`
	Kind          string          `json:"kind"`
	GroupID       uuid.UUID       `json:"group_id"`
	ConnectionID  *uuid.UUID      `json:"connection_id,omitempty"`
	Slug          string          `json:"slug"`
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	QueryText     string          `json:"query_text"`
	ParamsSchema  json.RawMessage `json:"params_schema"`
	OutputSchema  json.RawMessage `json:"output_schema,omitempty"`
	CodeRefs      json.RawMessage `json:"code_refs,omitempty"`
	ChatLog       json.RawMessage `json:"chat_log,omitempty"`
	LastTest      json.RawMessage `json:"last_test,omitempty"`
	RowLimit      int             `json:"row_limit"`
	TimeoutMS     int             `json:"timeout_ms"`
	CacheTTLSec   int             `json:"cache_ttl_sec"`
	CachePerToken bool            `json:"cache_per_token"`
	Status        string          `json:"status"`
	Version       int             `json:"version"`
	CreatedAt     time.Time       `json:"created_at"`
}

type ToolVersionDTO struct {
	ToolID    uuid.UUID       `json:"tool_id"`
	Version   int             `json:"version"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// ---- Selection / preview / report -----------------------------------------

// Selection chooses what to export. If Global is true the other fields are
// ignored and every entity in the database is exported.
type Selection struct {
	Global        bool        `json:"global"`
	ConnectionIDs []uuid.UUID `json:"connection_ids"`
	GroupIDs      []uuid.UUID `json:"group_ids"`
	ToolIDs       []uuid.UUID `json:"tool_ids"`
}

// ConflictAction is how a single item is applied on restore.
type ConflictAction string

const (
	ActionSkip      ConflictAction = "skip"
	ActionOverwrite ConflictAction = "overwrite"
	ActionDuplicate ConflictAction = "duplicate"
)

// ItemStatus describes the situation of a manifest item against the live DB.
type ItemStatus string

const (
	StatusNew               ItemStatus = "new"
	StatusConflictID        ItemStatus = "conflict_id"
	StatusConflictUnique    ItemStatus = "conflict_unique"
	StatusMissingDependency ItemStatus = "missing_dependency"
)

// PreviewItem describes a single manifest item ahead of restore.
type PreviewItem struct {
	Type       string         `json:"type"` // "connection" | "group" | "tool"
	ID         uuid.UUID      `json:"id"`
	Name       string         `json:"name"` // user-visible label (name/slug)
	Status     ItemStatus     `json:"status"`
	ExistingID *uuid.UUID     `json:"existing_id,omitempty"` // when conflict by unique field
	Suggested  ConflictAction `json:"suggested_action"`
	Reason     string         `json:"reason,omitempty"`
}

// Preview is the response of Importer.Preview.
type Preview struct {
	Manifest *Manifest      `json:"manifest"`
	Items    []PreviewItem  `json:"items"`
	Counts   map[string]int `json:"counts"`
}

// ItemResolution is one user decision sent back from the UI.
type ItemResolution struct {
	Type   string         `json:"type"`
	ID     uuid.UUID      `json:"id"`
	Action ConflictAction `json:"action"`
}

// ItemReport tells what happened to a manifest item during Apply.
type ItemReport struct {
	Type    string         `json:"type"`
	ID      uuid.UUID      `json:"id"`
	NewID   *uuid.UUID     `json:"new_id,omitempty"`
	Name    string         `json:"name"`
	Action  ConflictAction `json:"action"`
	Status  string         `json:"status"` // "created" | "updated" | "skipped" | "error"
	Message string         `json:"message,omitempty"`
}

// Report is returned by Importer.Apply.
type Report struct {
	Items  []ItemReport   `json:"items"`
	Counts map[string]int `json:"counts"` // "created","updated","skipped","duplicated","errors"
}

// ---- Service --------------------------------------------------------------

// Service performs export and import.
type Service struct {
	Pool   *pgxpool.Pool
	Cipher *crypto.Cipher
}

// New constructs a Service.
func New(pool *pgxpool.Pool, c *crypto.Cipher) *Service {
	return &Service{Pool: pool, Cipher: c}
}

// ---------------------------------------------------------------------------
// Export
// ---------------------------------------------------------------------------

// Export reads the requested entities and returns an encrypted backup blob.
// When sel.Global is true, every connection/group/tool is included.
// For selective exports, dependencies (connections + groups referenced by
// included tools, groups referenced by included connections-via-tools) are
// auto-included so the resulting file is self-consistent and restorable.
func (s *Service) Export(ctx context.Context, sel Selection, actor string) ([]byte, error) {
	m := &Manifest{
		Format:                     "contextforge-backup",
		Version:                    1,
		ExportedAt:                 time.Now().UTC(),
		ExportedBy:                 actor,
		SourceMasterKeyFingerprint: s.Cipher.Fingerprint(),
	}

	// Load entities according to selection.
	tools, err := s.loadTools(ctx, sel)
	if err != nil {
		return nil, fmt.Errorf("load tools: %w", err)
	}

	// Collect referenced group/connection IDs so dependencies are auto-included.
	refGroups := map[uuid.UUID]struct{}{}
	refConns := map[uuid.UUID]struct{}{}
	for _, t := range tools {
		refGroups[t.GroupID] = struct{}{}
		if t.ConnectionID != nil {
			refConns[*t.ConnectionID] = struct{}{}
		}
	}
	groups, err := s.loadGroups(ctx, sel, refGroups)
	if err != nil {
		return nil, fmt.Errorf("load groups: %w", err)
	}
	conns, err := s.loadConnections(ctx, sel, refConns)
	if err != nil {
		return nil, fmt.Errorf("load connections: %w", err)
	}
	versions, err := s.loadToolVersions(ctx, tools)
	if err != nil {
		return nil, fmt.Errorf("load tool versions: %w", err)
	}

	m.Items = ManifestItems{
		Connections:  conns,
		ToolGroups:   groups,
		Tools:        tools,
		ToolVersions: versions,
	}

	payload, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	ct, err := s.Cipher.Encrypt(payload, []byte(aad))
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(magic)+1+len(ct))
	out = append(out, magic...)
	out = append(out, fileVersion)
	out = append(out, ct...)
	return out, nil
}

func (s *Service) loadConnections(ctx context.Context, sel Selection, deps map[uuid.UUID]struct{}) ([]ConnectionDTO, error) {
	q := `SELECT id, name, type, description, encrypted_config, created_at FROM connections`
	args := []any{}
	if !sel.Global {
		ids := dedupeUUIDs(append([]uuid.UUID{}, sel.ConnectionIDs...))
		for id := range deps {
			ids = append(ids, id)
		}
		ids = dedupeUUIDs(ids)
		if len(ids) == 0 {
			return []ConnectionDTO{}, nil
		}
		q += ` WHERE id = ANY($1)`
		args = append(args, ids)
	}
	q += ` ORDER BY name`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConnectionDTO{}
	for rows.Next() {
		var c ConnectionDTO
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.Description, &c.EncryptedConfig, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) loadGroups(ctx context.Context, sel Selection, deps map[uuid.UUID]struct{}) ([]GroupDTO, error) {
	q := `SELECT id, name, description, hidden_by_default, created_at FROM tool_groups`
	args := []any{}
	if !sel.Global {
		ids := dedupeUUIDs(append([]uuid.UUID{}, sel.GroupIDs...))
		for id := range deps {
			ids = append(ids, id)
		}
		ids = dedupeUUIDs(ids)
		if len(ids) == 0 {
			return []GroupDTO{}, nil
		}
		q += ` WHERE id = ANY($1)`
		args = append(args, ids)
	}
	q += ` ORDER BY name`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GroupDTO{}
	for rows.Next() {
		var g GroupDTO
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.HiddenByDefault, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Service) loadTools(ctx context.Context, sel Selection) ([]ToolDTO, error) {
	q := `SELECT id, kind, group_id, connection_id, slug, title, description,
		query_text, params_schema, output_schema, code_refs, chat_log, last_test,
		row_limit, timeout_ms, cache_ttl_sec, cache_per_token, status, version, created_at
		FROM tools`
	args := []any{}
	if !sel.Global {
		ids := dedupeUUIDs(append([]uuid.UUID{}, sel.ToolIDs...))
		if len(ids) == 0 {
			return []ToolDTO{}, nil
		}
		q += ` WHERE id = ANY($1)`
		args = append(args, ids)
	}
	q += ` ORDER BY slug`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ToolDTO{}
	for rows.Next() {
		var t ToolDTO
		var connID *uuid.UUID
		var outputSchema, codeRefs, chatLog, lastTest []byte
		if err := rows.Scan(&t.ID, &t.Kind, &t.GroupID, &connID, &t.Slug, &t.Title, &t.Description,
			&t.QueryText, &t.ParamsSchema, &outputSchema, &codeRefs, &chatLog, &lastTest,
			&t.RowLimit, &t.TimeoutMS, &t.CacheTTLSec, &t.CachePerToken, &t.Status, &t.Version, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.ConnectionID = connID
		if len(outputSchema) > 0 {
			t.OutputSchema = json.RawMessage(outputSchema)
		}
		if len(codeRefs) > 0 {
			t.CodeRefs = json.RawMessage(codeRefs)
		}
		if len(chatLog) > 0 {
			t.ChatLog = json.RawMessage(chatLog)
		}
		if len(lastTest) > 0 {
			t.LastTest = json.RawMessage(lastTest)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Service) loadToolVersions(ctx context.Context, tools []ToolDTO) ([]ToolVersionDTO, error) {
	if len(tools) == 0 {
		return []ToolVersionDTO{}, nil
	}
	ids := make([]uuid.UUID, 0, len(tools))
	for _, t := range tools {
		ids = append(ids, t.ID)
	}
	rows, err := s.Pool.Query(ctx,
		`SELECT tool_id, version, payload, created_at FROM tool_versions WHERE tool_id = ANY($1) ORDER BY tool_id, version`,
		ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ToolVersionDTO{}
	for rows.Next() {
		var v ToolVersionDTO
		var payload []byte
		if err := rows.Scan(&v.ToolID, &v.Version, &payload, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.Payload = json.RawMessage(payload)
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Import
// ---------------------------------------------------------------------------

// Parse validates the file framing, decrypts with the server master key, and
// unmarshals the manifest. It returns a friendly error when the file's master
// key fingerprint does not match the current server.
func (s *Service) Parse(data []byte) (*Manifest, error) {
	if len(data) < len(magic)+1 {
		return nil, errors.New("backup file is too short")
	}
	for i, b := range magic {
		if data[i] != b {
			return nil, errors.New("not a ContextForge backup file (bad magic)")
		}
	}
	v := data[len(magic)]
	if v != fileVersion {
		return nil, fmt.Errorf("unsupported backup file version %d", v)
	}
	body := data[len(magic)+1:]
	plain, err := s.Cipher.Decrypt(body, []byte(aad))
	if err != nil {
		return nil, errors.New("failed to decrypt backup: this server's MASTER_KEY does not match the one used to create the file")
	}
	var m Manifest
	if err := json.Unmarshal(plain, &m); err != nil {
		return nil, fmt.Errorf("invalid backup payload: %w", err)
	}
	if m.Format != "contextforge-backup" {
		return nil, fmt.Errorf("unexpected backup format %q", m.Format)
	}
	if m.SourceMasterKeyFingerprint != "" && m.SourceMasterKeyFingerprint != s.Cipher.Fingerprint() {
		// Should not happen because Decrypt would have failed first, but be defensive.
		return nil, errors.New("backup master key fingerprint does not match this server")
	}
	return &m, nil
}

// Preview compares the manifest against the live DB and returns a per-item
// status with a suggested action. It does not modify anything.
func (s *Service) Preview(ctx context.Context, m *Manifest) (*Preview, error) {
	items := []PreviewItem{}

	// Connections
	for _, c := range m.Items.Connections {
		st, existing, reason, err := s.classifyConnection(ctx, c)
		if err != nil {
			return nil, err
		}
		items = append(items, PreviewItem{
			Type: "connection", ID: c.ID, Name: c.Name,
			Status: st, ExistingID: existing, Suggested: suggestedFor(st), Reason: reason,
		})
	}
	// Groups
	for _, g := range m.Items.ToolGroups {
		st, existing, reason, err := s.classifyGroup(ctx, g)
		if err != nil {
			return nil, err
		}
		items = append(items, PreviewItem{
			Type: "group", ID: g.ID, Name: g.Name,
			Status: st, ExistingID: existing, Suggested: suggestedFor(st), Reason: reason,
		})
	}
	// Tools
	for _, t := range m.Items.Tools {
		st, existing, reason, err := s.classifyTool(ctx, t, m)
		if err != nil {
			return nil, err
		}
		items = append(items, PreviewItem{
			Type: "tool", ID: t.ID, Name: t.Slug,
			Status: st, ExistingID: existing, Suggested: suggestedFor(st), Reason: reason,
		})
	}

	counts := map[string]int{
		"connections":   len(m.Items.Connections),
		"groups":        len(m.Items.ToolGroups),
		"tools":         len(m.Items.Tools),
		"tool_versions": len(m.Items.ToolVersions),
	}
	return &Preview{Manifest: m, Items: items, Counts: counts}, nil
}

func (s *Service) classifyConnection(ctx context.Context, c ConnectionDTO) (ItemStatus, *uuid.UUID, string, error) {
	var existing uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT id FROM connections WHERE id=$1`, c.ID).Scan(&existing)
	if err == nil {
		return StatusConflictID, &existing, "connection with this ID already exists", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", nil, "", err
	}
	err = s.Pool.QueryRow(ctx, `SELECT id FROM connections WHERE name=$1`, c.Name).Scan(&existing)
	if err == nil {
		return StatusConflictUnique, &existing, fmt.Sprintf("a different connection already uses name %q", c.Name), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", nil, "", err
	}
	return StatusNew, nil, "", nil
}

func (s *Service) classifyGroup(ctx context.Context, g GroupDTO) (ItemStatus, *uuid.UUID, string, error) {
	var existing uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT id FROM tool_groups WHERE id=$1`, g.ID).Scan(&existing)
	if err == nil {
		return StatusConflictID, &existing, "group with this ID already exists", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", nil, "", err
	}
	err = s.Pool.QueryRow(ctx, `SELECT id FROM tool_groups WHERE name=$1`, g.Name).Scan(&existing)
	if err == nil {
		return StatusConflictUnique, &existing, fmt.Sprintf("a different group already uses name %q", g.Name), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", nil, "", err
	}
	return StatusNew, nil, "", nil
}

func (s *Service) classifyTool(ctx context.Context, t ToolDTO, m *Manifest) (ItemStatus, *uuid.UUID, string, error) {
	// Dependency check: group_id and connection_id (when query tool) must be
	// either present in the manifest or already live in the DB.
	if missing, ok := s.dependencyMissing(ctx, t, m); ok {
		return StatusMissingDependency, nil, missing, nil
	}
	var existing uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT id FROM tools WHERE id=$1`, t.ID).Scan(&existing)
	if err == nil {
		return StatusConflictID, &existing, "tool with this ID already exists", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", nil, "", err
	}
	err = s.Pool.QueryRow(ctx, `SELECT id FROM tools WHERE slug=$1`, t.Slug).Scan(&existing)
	if err == nil {
		return StatusConflictUnique, &existing, fmt.Sprintf("a different tool already uses slug %q", t.Slug), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", nil, "", err
	}
	return StatusNew, nil, "", nil
}

func (s *Service) dependencyMissing(ctx context.Context, t ToolDTO, m *Manifest) (string, bool) {
	// group
	found := false
	for _, g := range m.Items.ToolGroups {
		if g.ID == t.GroupID {
			found = true
			break
		}
	}
	if !found {
		var x uuid.UUID
		if err := s.Pool.QueryRow(ctx, `SELECT id FROM tool_groups WHERE id=$1`, t.GroupID).Scan(&x); err != nil {
			return fmt.Sprintf("group %s not found (not in backup and not present on server)", t.GroupID), true
		}
	}
	// connection (query tools only)
	if t.Kind == "query" || (t.Kind == "" && t.ConnectionID != nil) {
		if t.ConnectionID == nil {
			return "query tool has no connection_id", true
		}
		for _, c := range m.Items.Connections {
			if c.ID == *t.ConnectionID {
				return "", false
			}
		}
		var x uuid.UUID
		if err := s.Pool.QueryRow(ctx, `SELECT id FROM connections WHERE id=$1`, *t.ConnectionID).Scan(&x); err != nil {
			return fmt.Sprintf("connection %s not found (not in backup and not present on server)", *t.ConnectionID), true
		}
	}
	return "", false
}

func suggestedFor(st ItemStatus) ConflictAction {
	switch st {
	case StatusNew:
		return ActionOverwrite // i.e. insert; "overwrite" means apply
	case StatusConflictID, StatusConflictUnique:
		return ActionSkip
	case StatusMissingDependency:
		return ActionSkip
	default:
		return ActionSkip
	}
}

// Apply commits the restore in a single transaction. Resolutions map manifest
// item identities (type+ID) to the action chosen by the user. Items not in
// the map use the suggestion from Preview.
func (s *Service) Apply(ctx context.Context, m *Manifest, resolutions []ItemResolution) (*Report, error) {
	// Re-compute preview to obtain suggested actions for missing entries.
	prev, err := s.Preview(ctx, m)
	if err != nil {
		return nil, err
	}
	// Build lookup of explicit resolutions: key = "type:id"
	resMap := map[string]ConflictAction{}
	for _, r := range resolutions {
		resMap[r.Type+":"+r.ID.String()] = r.Action
	}
	actionFor := func(it PreviewItem) ConflictAction {
		if a, ok := resMap[it.Type+":"+it.ID.String()]; ok {
			return a
		}
		return it.Suggested
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	report := &Report{
		Counts: map[string]int{"created": 0, "updated": 0, "skipped": 0, "duplicated": 0, "errors": 0},
	}
	// Index manifest entries.
	connByID := map[uuid.UUID]ConnectionDTO{}
	for _, c := range m.Items.Connections {
		connByID[c.ID] = c
	}
	groupByID := map[uuid.UUID]GroupDTO{}
	for _, g := range m.Items.ToolGroups {
		groupByID[g.ID] = g
	}
	toolByID := map[uuid.UUID]ToolDTO{}
	for _, t := range m.Items.Tools {
		toolByID[t.ID] = t
	}

	// Track ID remappings caused by "duplicate" so child refs can be rewired.
	connIDMap := map[uuid.UUID]uuid.UUID{}
	groupIDMap := map[uuid.UUID]uuid.UUID{}
	toolIDMap := map[uuid.UUID]uuid.UUID{}

	// Order: groups → connections → tools → tool_versions
	previewByKey := map[string]PreviewItem{}
	for _, p := range prev.Items {
		previewByKey[p.Type+":"+p.ID.String()] = p
	}

	for _, g := range m.Items.ToolGroups {
		pi := previewByKey["group:"+g.ID.String()]
		act := actionFor(pi)
		r, err := s.applyGroup(ctx, tx, g, pi, act, groupIDMap)
		report.Items = append(report.Items, r)
		bumpCount(report.Counts, r.Status, act)
		if err != nil {
			return nil, err
		}
	}
	for _, c := range m.Items.Connections {
		pi := previewByKey["connection:"+c.ID.String()]
		act := actionFor(pi)
		r, err := s.applyConnection(ctx, tx, c, pi, act, connIDMap)
		report.Items = append(report.Items, r)
		bumpCount(report.Counts, r.Status, act)
		if err != nil {
			return nil, err
		}
	}
	for _, t := range m.Items.Tools {
		pi := previewByKey["tool:"+t.ID.String()]
		act := actionFor(pi)
		r, err := s.applyTool(ctx, tx, t, pi, act, groupIDMap, connIDMap, toolIDMap)
		report.Items = append(report.Items, r)
		bumpCount(report.Counts, r.Status, act)
		if err != nil {
			return nil, err
		}
	}
	for _, v := range m.Items.ToolVersions {
		// Tool versions follow their parent tool. If tool was skipped, skip too.
		newID, mapped := toolIDMap[v.ToolID]
		if !mapped {
			// Parent skipped or missing — silently skip versions for it.
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO tool_versions (tool_id, version, payload) VALUES ($1,$2,$3)
			 ON CONFLICT (tool_id, version) DO UPDATE SET payload = EXCLUDED.payload`,
			newID, v.Version, []byte(v.Payload)); err != nil {
			return nil, fmt.Errorf("insert tool_version %s/%d: %w", newID, v.Version, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return report, nil
}

func bumpCount(c map[string]int, status string, act ConflictAction) {
	switch status {
	case "created":
		c["created"]++
		if act == ActionDuplicate {
			c["duplicated"]++
		}
	case "updated":
		c["updated"]++
	case "skipped":
		c["skipped"]++
	case "error":
		c["errors"]++
	}
}

// ---- per-entity apply -----------------------------------------------------

func (s *Service) applyGroup(ctx context.Context, tx pgx.Tx, g GroupDTO, pi PreviewItem, act ConflictAction, idMap map[uuid.UUID]uuid.UUID) (ItemReport, error) {
	ir := ItemReport{Type: "group", ID: g.ID, Name: g.Name, Action: act}
	switch act {
	case ActionSkip:
		ir.Status = "skipped"
		return ir, nil
	case ActionDuplicate:
		newID := uuid.New()
		newName, err := uniqueName(ctx, tx, "tool_groups", "name", g.Name)
		if err != nil {
			ir.Status = "error"
			ir.Message = err.Error()
			return ir, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO tool_groups (id, name, description, hidden_by_default) VALUES ($1,$2,$3,$4)`,
			newID, newName, g.Description, g.HiddenByDefault); err != nil {
			ir.Status = "error"
			ir.Message = err.Error()
			return ir, err
		}
		idMap[g.ID] = newID
		ir.NewID = &newID
		ir.Name = newName
		ir.Status = "created"
		return ir, nil
	case ActionOverwrite:
		// "overwrite" means: insert when new, update when conflict by ID,
		// or update the existing-by-unique record when conflict by name.
		switch pi.Status {
		case StatusConflictUnique:
			// Update the existing row sharing the unique name.
			if _, err := tx.Exec(ctx,
				`UPDATE tool_groups SET description=$2, hidden_by_default=$3, updated_at=now() WHERE id=$1`,
				*pi.ExistingID, g.Description, g.HiddenByDefault); err != nil {
				ir.Status = "error"
				ir.Message = err.Error()
				return ir, err
			}
			idMap[g.ID] = *pi.ExistingID
			ir.NewID = pi.ExistingID
			ir.Status = "updated"
			return ir, nil
		default:
			if _, err := tx.Exec(ctx,
				`INSERT INTO tool_groups (id, name, description, hidden_by_default)
				 VALUES ($1,$2,$3,$4)
				 ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, description=EXCLUDED.description,
				   hidden_by_default=EXCLUDED.hidden_by_default, updated_at=now()`,
				g.ID, g.Name, g.Description, g.HiddenByDefault); err != nil {
				ir.Status = "error"
				ir.Message = err.Error()
				return ir, err
			}
			idMap[g.ID] = g.ID
			if pi.Status == StatusNew {
				ir.Status = "created"
			} else {
				ir.Status = "updated"
			}
			return ir, nil
		}
	}
	ir.Status = "skipped"
	return ir, nil
}

func (s *Service) applyConnection(ctx context.Context, tx pgx.Tx, c ConnectionDTO, pi PreviewItem, act ConflictAction, idMap map[uuid.UUID]uuid.UUID) (ItemReport, error) {
	ir := ItemReport{Type: "connection", ID: c.ID, Name: c.Name, Action: act}
	switch act {
	case ActionSkip:
		ir.Status = "skipped"
		return ir, nil
	case ActionDuplicate:
		newID := uuid.New()
		newName, err := uniqueName(ctx, tx, "connections", "name", c.Name)
		if err != nil {
			ir.Status = "error"
			ir.Message = err.Error()
			return ir, err
		}
		// Note: encrypted_config AAD is bound to old UUID. Re-encrypt with new AAD.
		plain, err := s.Cipher.Decrypt(c.EncryptedConfig, []byte("connection:"+c.ID.String()))
		if err != nil {
			ir.Status = "error"
			ir.Message = "cannot decrypt connection config: " + err.Error()
			return ir, err
		}
		enc, err := s.Cipher.Encrypt(plain, []byte("connection:"+newID.String()))
		if err != nil {
			ir.Status = "error"
			ir.Message = err.Error()
			return ir, err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO connections (id, name, type, encrypted_config, description) VALUES ($1,$2,$3,$4,$5)`,
			newID, newName, c.Type, enc, c.Description); err != nil {
			ir.Status = "error"
			ir.Message = err.Error()
			return ir, err
		}
		idMap[c.ID] = newID
		ir.NewID = &newID
		ir.Name = newName
		ir.Status = "created"
		return ir, nil
	case ActionOverwrite:
		switch pi.Status {
		case StatusConflictUnique:
			// Overwrite existing row matched by name; keep its ID.
			existingID := *pi.ExistingID
			plain, err := s.Cipher.Decrypt(c.EncryptedConfig, []byte("connection:"+c.ID.String()))
			if err != nil {
				ir.Status = "error"
				ir.Message = "cannot decrypt connection config: " + err.Error()
				return ir, err
			}
			enc, err := s.Cipher.Encrypt(plain, []byte("connection:"+existingID.String()))
			if err != nil {
				ir.Status = "error"
				ir.Message = err.Error()
				return ir, err
			}
			if _, err := tx.Exec(ctx,
				`UPDATE connections SET type=$2, encrypted_config=$3, description=$4, updated_at=now() WHERE id=$1`,
				existingID, c.Type, enc, c.Description); err != nil {
				ir.Status = "error"
				ir.Message = err.Error()
				return ir, err
			}
			idMap[c.ID] = existingID
			ir.NewID = &existingID
			ir.Status = "updated"
			return ir, nil
		default:
			if _, err := tx.Exec(ctx,
				`INSERT INTO connections (id, name, type, encrypted_config, description)
				 VALUES ($1,$2,$3,$4,$5)
				 ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, type=EXCLUDED.type,
				   encrypted_config=EXCLUDED.encrypted_config, description=EXCLUDED.description, updated_at=now()`,
				c.ID, c.Name, c.Type, c.EncryptedConfig, c.Description); err != nil {
				ir.Status = "error"
				ir.Message = err.Error()
				return ir, err
			}
			idMap[c.ID] = c.ID
			if pi.Status == StatusNew {
				ir.Status = "created"
			} else {
				ir.Status = "updated"
			}
			return ir, nil
		}
	}
	ir.Status = "skipped"
	return ir, nil
}

func (s *Service) applyTool(ctx context.Context, tx pgx.Tx, t ToolDTO, pi PreviewItem, act ConflictAction,
	groupMap, connMap, toolMap map[uuid.UUID]uuid.UUID) (ItemReport, error) {
	ir := ItemReport{Type: "tool", ID: t.ID, Name: t.Slug, Action: act}
	if pi.Status == StatusMissingDependency {
		ir.Status = "skipped"
		ir.Message = pi.Reason
		return ir, nil
	}
	switch act {
	case ActionSkip:
		ir.Status = "skipped"
		return ir, nil
	case ActionDuplicate:
		newID := uuid.New()
		newSlug, err := uniqueName(ctx, tx, "tools", "slug", t.Slug)
		if err != nil {
			ir.Status = "error"
			ir.Message = err.Error()
			return ir, err
		}
		// Sanitize slug to the regex constraint.
		newSlug = sanitizeSlug(newSlug)
		groupID := resolveID(groupMap, t.GroupID)
		var connID any
		if t.ConnectionID != nil {
			cid := resolveID(connMap, *t.ConnectionID)
			connID = cid
		} else {
			connID = nil
		}
		if _, err := tx.Exec(ctx, insertToolSQL,
			newID, kindOrDefault(t.Kind), groupID, connID, newSlug, t.Title, t.Description,
			t.QueryText, t.ParamsSchema, nullableJSON(t.OutputSchema), t.RowLimit, t.TimeoutMS,
			t.CacheTTLSec, t.CachePerToken, t.Status, t.CodeRefs, t.ChatLog, nullableJSON(t.LastTest),
		); err != nil {
			ir.Status = "error"
			ir.Message = err.Error()
			return ir, err
		}
		toolMap[t.ID] = newID
		ir.NewID = &newID
		ir.Name = newSlug
		ir.Status = "created"
		return ir, nil
	case ActionOverwrite:
		groupID := resolveID(groupMap, t.GroupID)
		var connID any
		if t.ConnectionID != nil {
			cid := resolveID(connMap, *t.ConnectionID)
			connID = cid
		} else {
			connID = nil
		}
		targetID := t.ID
		if pi.Status == StatusConflictUnique {
			targetID = *pi.ExistingID
		}
		if _, err := tx.Exec(ctx, upsertToolSQL,
			targetID, kindOrDefault(t.Kind), groupID, connID, t.Slug, t.Title, t.Description,
			t.QueryText, t.ParamsSchema, nullableJSON(t.OutputSchema), t.RowLimit, t.TimeoutMS,
			t.CacheTTLSec, t.CachePerToken, t.Status, t.CodeRefs, t.ChatLog, nullableJSON(t.LastTest),
		); err != nil {
			ir.Status = "error"
			ir.Message = err.Error()
			return ir, err
		}
		toolMap[t.ID] = targetID
		newID := targetID
		ir.NewID = &newID
		if pi.Status == StatusNew {
			ir.Status = "created"
		} else {
			ir.Status = "updated"
		}
		return ir, nil
	}
	ir.Status = "skipped"
	return ir, nil
}

const insertToolSQL = `
INSERT INTO tools (id, kind, group_id, connection_id, slug, title, description,
	query_text, params_schema, output_schema, row_limit, timeout_ms,
	cache_ttl_sec, cache_per_token, status, code_refs, chat_log, last_test)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`

const upsertToolSQL = `
INSERT INTO tools (id, kind, group_id, connection_id, slug, title, description,
	query_text, params_schema, output_schema, row_limit, timeout_ms,
	cache_ttl_sec, cache_per_token, status, code_refs, chat_log, last_test)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
ON CONFLICT (id) DO UPDATE SET
	kind=EXCLUDED.kind, group_id=EXCLUDED.group_id, connection_id=EXCLUDED.connection_id,
	slug=EXCLUDED.slug, title=EXCLUDED.title, description=EXCLUDED.description,
	query_text=EXCLUDED.query_text, params_schema=EXCLUDED.params_schema,
	output_schema=EXCLUDED.output_schema, row_limit=EXCLUDED.row_limit,
	timeout_ms=EXCLUDED.timeout_ms, cache_ttl_sec=EXCLUDED.cache_ttl_sec,
	cache_per_token=EXCLUDED.cache_per_token, status=EXCLUDED.status,
	code_refs=EXCLUDED.code_refs, chat_log=EXCLUDED.chat_log, last_test=EXCLUDED.last_test,
	version = tools.version + 1, updated_at=now()`

// ---- helpers --------------------------------------------------------------

func kindOrDefault(k string) string {
	if k == "code" {
		return "code"
	}
	return "query"
}

func nullableJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

func resolveID(m map[uuid.UUID]uuid.UUID, id uuid.UUID) uuid.UUID {
	if nid, ok := m[id]; ok {
		return nid
	}
	return id
}

func dedupeUUIDs(in []uuid.UUID) []uuid.UUID {
	if len(in) == 0 {
		return in
	}
	seen := make(map[uuid.UUID]struct{}, len(in))
	out := make([]uuid.UUID, 0, len(in))
	for _, id := range in {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// uniqueName finds a free value for a UNIQUE text column by appending "_copy"
// and then "_copyN" until no row collides. Used by Duplicate action.
func uniqueName(ctx context.Context, tx pgx.Tx, table, column, base string) (string, error) {
	candidate := base + "_copy"
	for i := 0; i < 1000; i++ {
		c := candidate
		if i > 0 {
			c = fmt.Sprintf("%s_copy%d", base, i+1)
		}
		var x uuid.UUID
		err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE %s=$1`, table, column), c).Scan(&x)
		if errors.Is(err, pgx.ErrNoRows) {
			return c, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("could not derive a unique name after 1000 attempts")
}

// sanitizeSlug ensures the result still matches '^[a-z0-9_]{1,64}$'. Replaces
// anything else with '_' and lowercases. Truncates to 64.
func sanitizeSlug(s string) string {
	s = strings.ToLower(s)
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			b = append(b, c)
		case c >= '0' && c <= '9':
			b = append(b, c)
		case c == '_':
			b = append(b, c)
		default:
			b = append(b, '_')
		}
	}
	if len(b) == 0 {
		b = []byte("tool_copy")
	}
	if len(b) > 64 {
		b = b[:64]
	}
	return string(b)
}
