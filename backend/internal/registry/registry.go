// Package registry holds an in-memory snapshot of tools + tokens + grants
// and refreshes it periodically from the database so changes in the admin UI
// become available without a server restart.
package registry

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/marcos14/contextforge/backend/internal/store"
)

// ToolDef is the projected, registry-level view of a tool.
type ToolDef struct {
	ID             uuid.UUID
	Kind           store.ToolKind
	GroupID        uuid.UUID
	GroupName      string
	GroupHidden    bool
	ConnectionID   uuid.UUID // uuid.Nil when Kind == ToolKindCode
	ConnectionType store.ConnectionType
	Slug           string
	Title          string
	Description    string
	QueryText      string
	ParamsSchema   json.RawMessage
	OutputSchema   json.RawMessage
	RowLimit       int
	TimeoutMS      int
	CacheTTLSec    int
	CachePerToken  bool
	Version        int
}

// TokenView is the projected, registry-level view of an active token + its grants.
type TokenView struct {
	ID                uuid.UUID
	Prefix            string
	HashedSecret      []byte
	RateLimitPerMin   int
	IPAllowlist       []string
	AllowedToolIDs    map[uuid.UUID]struct{} // explicit tool grants
	AllowedGroupIDs   map[uuid.UUID]struct{} // group grants (all tools in group)
	ToolRateOverride  map[uuid.UUID]int      // override per tool
	GroupRateOverride map[uuid.UUID]int      // override per group
	ShowGroup         map[uuid.UUID]bool     // explicit visibility override (true/false)
}

// Snapshot is the consistent, immutable read-side projection.
type Snapshot struct {
	Tools      map[uuid.UUID]*ToolDef
	BySlug     map[string]*ToolDef
	Tokens     map[string]*TokenView // by prefix
	ConnByName map[string]uuid.UUID  // connection name -> id
	BuiltAt    time.Time
}

// Registry is the live, atomically-swappable snapshot.
type Registry struct {
	mu   sync.RWMutex
	cur  *Snapshot
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Registry {
	return &Registry{pool: pool, cur: &Snapshot{
		Tools:      map[uuid.UUID]*ToolDef{},
		BySlug:     map[string]*ToolDef{},
		Tokens:     map[string]*TokenView{},
		ConnByName: map[string]uuid.UUID{},
	}}
}

// Get returns the current snapshot (safe for concurrent reads).
func (r *Registry) Get() *Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cur
}

// Run starts a goroutine that refreshes the snapshot every interval.
func (r *Registry) Run(ctx context.Context, interval time.Duration, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	if err := r.Refresh(ctx); err != nil {
		log.Error("registry initial refresh failed", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := r.Refresh(ctx); err != nil {
				log.Error("registry refresh failed", "err", err)
			}
		}
	}
}

// Refresh rebuilds the snapshot from the database in one pass.
func (r *Registry) Refresh(ctx context.Context) error {
	snap := &Snapshot{
		Tools:      map[uuid.UUID]*ToolDef{},
		BySlug:     map[string]*ToolDef{},
		Tokens:     map[string]*TokenView{},
		ConnByName: map[string]uuid.UUID{},
		BuiltAt:    time.Now(),
	}

	// ---- Groups (id -> name, hidden) ----
	type grp struct {
		name   string
		hidden bool
	}
	groups := map[uuid.UUID]grp{}
	rows, err := r.pool.Query(ctx, `SELECT id, name, hidden_by_default FROM tool_groups`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uuid.UUID
		var name string
		var hidden bool
		if err := rows.Scan(&id, &name, &hidden); err != nil {
			rows.Close()
			return err
		}
		groups[id] = grp{name, hidden}
	}
	rows.Close()

	// ---- Connections (id -> type) ----
	conns := map[uuid.UUID]store.ConnectionType{}
	rows, err = r.pool.Query(ctx, `SELECT id, name, type FROM connections`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uuid.UUID
		var name, t string
		if err := rows.Scan(&id, &name, &t); err != nil {
			rows.Close()
			return err
		}
		conns[id] = store.ConnectionType(t)
		snap.ConnByName[name] = id
	}
	rows.Close()

	// ---- Active tools ----
	rows, err = r.pool.Query(ctx, `
SELECT id, kind, group_id,
       COALESCE(connection_id, '00000000-0000-0000-0000-000000000000'::uuid),
       slug, title, description, query_text,
       params_schema, output_schema, row_limit, timeout_ms,
       cache_ttl_sec, cache_per_token, version
FROM tools
WHERE status = 'active'`)
	if err != nil {
		return err
	}
	for rows.Next() {
		t := &ToolDef{}
		var outSchema *json.RawMessage
		if err := rows.Scan(
			&t.ID, &t.Kind, &t.GroupID, &t.ConnectionID, &t.Slug, &t.Title, &t.Description, &t.QueryText,
			&t.ParamsSchema, &outSchema, &t.RowLimit, &t.TimeoutMS,
			&t.CacheTTLSec, &t.CachePerToken, &t.Version,
		); err != nil {
			rows.Close()
			return err
		}
		if outSchema != nil {
			t.OutputSchema = *outSchema
		}
		if g, ok := groups[t.GroupID]; ok {
			t.GroupName = g.name
			t.GroupHidden = g.hidden
		}
		if t.ConnectionID != uuid.Nil {
			t.ConnectionType = conns[t.ConnectionID]
		}
		snap.Tools[t.ID] = t
		snap.BySlug[t.Slug] = t
	}
	rows.Close()

	// ---- Tokens (active, not revoked) ----
	tokensByID := map[uuid.UUID]*TokenView{}
	rows, err = r.pool.Query(ctx, `
SELECT id, prefix, hashed_secret, rate_limit_per_min, ip_allowlist
FROM tokens
WHERE revoked_at IS NULL`)
	if err != nil {
		return err
	}
	for rows.Next() {
		tv := &TokenView{
			AllowedToolIDs:    map[uuid.UUID]struct{}{},
			AllowedGroupIDs:   map[uuid.UUID]struct{}{},
			ToolRateOverride:  map[uuid.UUID]int{},
			GroupRateOverride: map[uuid.UUID]int{},
			ShowGroup:         map[uuid.UUID]bool{},
		}
		if err := rows.Scan(&tv.ID, &tv.Prefix, &tv.HashedSecret, &tv.RateLimitPerMin, &tv.IPAllowlist); err != nil {
			rows.Close()
			return err
		}
		snap.Tokens[tv.Prefix] = tv
		tokensByID[tv.ID] = tv
	}
	rows.Close()

	// ---- Grants ----
	rows, err = r.pool.Query(ctx, `
SELECT token_id, scope, target_id, rate_limit_per_min_override
FROM token_grants`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var tokID, targetID uuid.UUID
		var scope string
		var override *int
		if err := rows.Scan(&tokID, &scope, &targetID, &override); err != nil {
			rows.Close()
			return err
		}
		tv, ok := tokensByID[tokID]
		if !ok {
			continue
		}
		switch scope {
		case "tool":
			tv.AllowedToolIDs[targetID] = struct{}{}
			if override != nil {
				tv.ToolRateOverride[targetID] = *override
			}
		case "group":
			tv.AllowedGroupIDs[targetID] = struct{}{}
			if override != nil {
				tv.GroupRateOverride[targetID] = *override
			}
		}
	}
	rows.Close()

	// ---- Visibility overrides ----
	rows, err = r.pool.Query(ctx, `SELECT token_id, group_id, show FROM group_visibility_overrides`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var tokID, gID uuid.UUID
		var show bool
		if err := rows.Scan(&tokID, &gID, &show); err != nil {
			rows.Close()
			return err
		}
		if tv, ok := tokensByID[tokID]; ok {
			tv.ShowGroup[gID] = show
		}
	}
	rows.Close()

	r.mu.Lock()
	r.cur = snap
	r.mu.Unlock()
	return nil
}

// CanAccess reports whether a token may invoke a given tool.
func (tv *TokenView) CanAccess(t *ToolDef) bool {
	if _, ok := tv.AllowedToolIDs[t.ID]; ok {
		return true
	}
	if _, ok := tv.AllowedGroupIDs[t.GroupID]; ok {
		return true
	}
	return false
}

// ShouldList reports whether a tool should be listed for a token (visibility
// rule). Default-hidden groups stay hidden unless the token has an explicit
// override (show=true) OR a tool-level grant on the tool itself.
func (tv *TokenView) ShouldList(t *ToolDef) bool {
	if !tv.CanAccess(t) {
		return false
	}
	// Explicit override per token+group
	if v, ok := tv.ShowGroup[t.GroupID]; ok {
		return v
	}
	if !t.GroupHidden {
		return true
	}
	// Group is hidden by default. If the token has an explicit tool grant,
	// surface it; group grants alone keep the group hidden.
	_, hasTool := tv.AllowedToolIDs[t.ID]
	return hasTool
}

// RateForTool returns the minute limit applicable for (token, tool).
// Precedence: tool override > group override > token base.
func (tv *TokenView) RateForTool(t *ToolDef) int {
	if v, ok := tv.ToolRateOverride[t.ID]; ok {
		return v
	}
	if v, ok := tv.GroupRateOverride[t.GroupID]; ok {
		return v
	}
	return tv.RateLimitPerMin
}
