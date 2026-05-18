package store

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Role is the admin UI role.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleEditor Role = "editor"
	RoleViewer Role = "viewer"
)

type User struct {
	ID           uuid.UUID `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         Role      `json:"role"`
	Disabled     bool      `json:"disabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ConnectionType enumerates supported data source kinds.
type ConnectionType string

const (
	ConnPg       ConnectionType = "pg"
	ConnMySQL    ConnectionType = "mysql"
	ConnMSSQL    ConnectionType = "mssql"
	ConnOracle   ConnectionType = "oracle"
	ConnMongo    ConnectionType = "mongo"
	ConnFirebird ConnectionType = "firebird"
	ConnREST     ConnectionType = "rest"
)

type Connection struct {
	ID              uuid.UUID      `json:"id"`
	Name            string         `json:"name"`
	Type            ConnectionType `json:"type"`
	EncryptedConfig []byte         `json:"-"`
	Description     string         `json:"description"`
	CreatedBy       *uuid.UUID     `json:"created_by,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type ToolGroup struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	HiddenByDefault bool      `json:"hidden_by_default"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ToolStatus string

const (
	ToolDraft    ToolStatus = "draft"
	ToolActive   ToolStatus = "active"
	ToolDisabled ToolStatus = "disabled"
)

// ToolKind discriminates between query-backed tools (the default — SQL/Mongo/
// REST against a single connection) and code-backed tools (a JavaScript
// snippet executed by the embedded goja runtime, with access to multiple
// connections and other tools).
type ToolKind string

const (
	ToolKindQuery ToolKind = "query"
	ToolKindCode  ToolKind = "code"
)

type Tool struct {
	ID            uuid.UUID       `json:"id"`
	Kind          ToolKind        `json:"kind"`
	GroupID       uuid.UUID       `json:"group_id"`
	ConnectionID  uuid.UUID       `json:"connection_id"`
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
	Status        ToolStatus      `json:"status"`
	Version       int             `json:"version"`
	CreatedBy     *uuid.UUID      `json:"created_by,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type Token struct {
	ID              uuid.UUID  `json:"id"`
	Name            string     `json:"name"`
	Prefix          string     `json:"prefix"`
	HashedSecret    []byte     `json:"-"`
	OwnerUserID     *uuid.UUID `json:"owner_user_id,omitempty"`
	RateLimitPerMin int        `json:"rate_limit_per_min"`
	IPAllowlist     []string   `json:"ip_allowlist"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type GrantScope string

const (
	GrantScopeTool  GrantScope = "tool"
	GrantScopeGroup GrantScope = "group"
)

type TokenGrant struct {
	ID                      uuid.UUID  `json:"id"`
	TokenID                 uuid.UUID  `json:"token_id"`
	Scope                   GrantScope `json:"scope"`
	TargetID                uuid.UUID  `json:"target_id"`
	RateLimitPerMinOverride *int       `json:"rate_limit_per_min_override,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
}

type GroupVisibilityOverride struct {
	TokenID uuid.UUID `json:"token_id"`
	GroupID uuid.UUID `json:"group_id"`
	Show    bool      `json:"show"`
}
