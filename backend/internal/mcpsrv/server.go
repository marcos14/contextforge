// Package mcpsrv wires the MCP Streamable HTTP server using mark3labs/mcp-go.
// It performs Bearer token auth, exposes only tools the token may see, and
// dispatches calls through the executor.
package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/marcos14/contextforge/backend/internal/auth"
	"github.com/marcos14/contextforge/backend/internal/executor"
	"github.com/marcos14/contextforge/backend/internal/registry"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type ctxKey int

const (
	ctxKeyTokenID ctxKey = iota
	ctxKeyTokenView
	ctxKeyClientIP
)

// Server bundles the MCP server and its HTTP handler.
type Server struct {
	mcp  *server.MCPServer
	http *server.StreamableHTTPServer
	reg  *registry.Registry
	exec *executor.Executor
}

// New constructs the MCP server.
func New(reg *registry.Registry, exec *executor.Executor) *Server {
	s := &Server{reg: reg, exec: exec}

	mcpSrv := server.NewMCPServer(
		"ContextForge",
		"0.1.0",
		server.WithToolCapabilities(true),
		server.WithRecovery(),
		server.WithToolFilter(s.toolFilter),
	)

	// Register a single "router" tool dynamically isn't supported; instead
	// we register every active tool on every snapshot change by re-walking
	// the registry. mcp-go supports AddTool/DeleteTool at runtime.
	s.mcp = mcpSrv
	s.http = server.NewStreamableHTTPServer(mcpSrv,
		server.WithEndpointPath("/mcp"),
	)
	return s
}

// SyncTools registers every active tool in the current snapshot with the MCP
// server. Call after each registry refresh; the per-session ToolFilter
// further narrows the list per token.
func (s *Server) SyncTools() {
	snap := s.reg.Get()
	// mcp-go exposes AddTool but not a wholesale "set tools" — to keep it
	// simple, we add any new tools and rely on the filter to hide stale
	// (removed) ones. For long-running servers a periodic rebuild may be
	// preferable; left as an improvement.
	for _, t := range snap.Tools {
		t := t
		params := buildSchema(t.ParamsSchema)
		def := mcp.NewToolWithRawSchema(t.Slug, t.Description, params)
		s.mcp.AddTool(def, s.handleCall(t.Slug))
	}
}

func buildSchema(raw json.RawMessage) json.RawMessage {
	if len(raw) > 0 {
		return raw
	}
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// handleCall builds a tool handler bound to a specific slug.
func (s *Server) handleCall(slug string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		tv, _ := ctx.Value(ctxKeyTokenView).(*registry.TokenView)
		tokID, _ := ctx.Value(ctxKeyTokenID).(uuid.UUID)
		ip, _ := ctx.Value(ctxKeyClientIP).(string)

		args := map[string]any{}
		if m := req.GetArguments(); m != nil {
			args = m
		}

		res, err := s.exec.Run(ctx, executor.Invocation{
			TokenID:   tokID,
			TokenView: tv,
			ToolSlug:  slug,
			Params:    args,
			ClientIP:  ip,
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		body, _ := json.MarshalIndent(res.Data, "", "  ")
		return mcp.NewToolResultText(string(body)), nil
	}
}

// toolFilter restricts which tools are visible per session.
func (s *Server) toolFilter(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	tv, ok := ctx.Value(ctxKeyTokenView).(*registry.TokenView)
	if !ok || tv == nil {
		return nil
	}
	snap := s.reg.Get()
	out := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		def, ok := snap.BySlug[t.Name]
		if !ok {
			continue
		}
		if tv.ShouldList(def) {
			out = append(out, t)
		}
	}
	return out
}

// AuthMiddleware verifies the Bearer token and attaches the token view to
// the request context.
func (s *Server) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix, secret, ok := auth.ParseClientToken(r.Header.Get("Authorization"))
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "missing or malformed bearer token")
			return
		}
		snap := s.reg.Get()
		tv, found := snap.Tokens[prefix]
		if !found || !auth.VerifyClientSecret(secret, tv.HashedSecret) {
			writeJSONError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		ip := clientIP(r)
		if !ipAllowed(ip, tv.IPAllowlist) {
			writeJSONError(w, http.StatusForbidden, "ip not allowed")
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyTokenID, tv.ID)
		ctx = context.WithValue(ctx, ctxKeyTokenView, tv)
		ctx = context.WithValue(ctx, ctxKeyClientIP, ip)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Handler returns the http.Handler that serves /mcp.
func (s *Server) Handler() http.Handler {
	return s.AuthMiddleware(s.http)
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.Index(xff, ","); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		return host[:i]
	}
	return host
}

func ipAllowed(ip string, allow []string) bool {
	if len(allow) == 0 {
		return true
	}
	for _, a := range allow {
		if a == ip {
			return true
		}
	}
	return false
}

// SanityCheck logs configuration anomalies at startup.
func (s *Server) SanityCheck() error {
	if s.exec == nil || s.reg == nil {
		return errors.New("mcp server not fully wired")
	}
	fmt.Println("mcp server ready")
	return nil
}
