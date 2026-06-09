// Command contextforge is the unified backend: REST admin API, MCP Streamable
// HTTP server, and SPA static file server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/marcos14/contextforge/backend/internal/api"
	"github.com/marcos14/contextforge/backend/internal/auth"
	"github.com/marcos14/contextforge/backend/internal/cache"
	"github.com/marcos14/contextforge/backend/internal/codetool"
	"github.com/marcos14/contextforge/backend/internal/config"
	"github.com/marcos14/contextforge/backend/internal/crypto"
	"github.com/marcos14/contextforge/backend/internal/executor"
	"github.com/marcos14/contextforge/backend/internal/llm"
	"github.com/marcos14/contextforge/backend/internal/mcpsrv"
	"github.com/marcos14/contextforge/backend/internal/ratelimit"
	"github.com/marcos14/contextforge/backend/internal/registry"
	"github.com/marcos14/contextforge/backend/internal/store"

	// Driver side-effect imports (register factories).
	_ "github.com/marcos14/contextforge/backend/internal/drivers/firebird"
	_ "github.com/marcos14/contextforge/backend/internal/drivers/mongo"
	_ "github.com/marcos14/contextforge/backend/internal/drivers/mssql"
	_ "github.com/marcos14/contextforge/backend/internal/drivers/mysql"
	_ "github.com/marcos14/contextforge/backend/internal/drivers/oracle"
	_ "github.com/marcos14/contextforge/backend/internal/drivers/pg"
	_ "github.com/marcos14/contextforge/backend/internal/drivers/rest"

	"github.com/marcos14/contextforge/backend/internal/drivers"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ---- DB ----
	if err := store.Migrate(ctx, cfg.DatabaseURL); err != nil {
		logger.Error("migrate", "err", err)
		os.Exit(1)
	}
	pool, err := store.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("db connect", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// ---- Redis ----
	ropt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Error("redis url", "err", err)
		os.Exit(1)
	}
	rdb := redis.NewClient(ropt)
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Warn("redis ping failed", "err", err)
	}

	// ---- Core services ----
	cipher, err := crypto.New(cfg.MasterKey)
	if err != nil {
		logger.Error("cipher", "err", err)
		os.Exit(1)
	}
	signer := auth.NewSigner(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	rl := ratelimit.New(rdb)
	cch := cache.New(rdb)

	reg := registry.New(pool)
	exec := executor.New(executor.Deps{
		Pool: pool, Registry: reg, Cache: cch, Limiter: rl,
		Cipher: cipher, IPMaxPerMin: cfg.RateLimitIPPerMin,
	})

	// Wire code-tool runtime AFTER exec is built (mutual deps: runtime needs
	// the executor for tools.call/db()).
	codeRT := codetool.New(reg, exec, exec, logger, cfg.DefaultRowLimit)
	exec.SetCodeRuntime(func(ctx context.Context, inv executor.CodeInvocation) (*drivers.ExecResult, error) {
		return codeRT.Run(ctx, codetool.Invocation{
			Slug:      inv.Slug,
			Code:      inv.Code,
			Params:    inv.Params,
			TokenView: inv.TokenView,
			TokenID:   inv.TokenID,
			ClientIP:  inv.ClientIP,
			RowLimit:  inv.RowLimit,
			Timeout:   inv.Timeout,
			Preview:   inv.Preview,
		})
	})

	var llmClient *llm.Client
	if cfg.OpenRouterAPIKey != "" {
		llmClient = llm.New(cfg.OpenRouterAPIKey, cfg.OpenRouterModel, cfg.OpenRouterBaseURL, cfg.OpenRouterTimeout)
	}

	// ---- Bootstrap admin if no users exist ----
	if err := bootstrapAdmin(ctx, pool, cfg); err != nil {
		logger.Warn("bootstrap admin", "err", err)
	}

	// ---- Registry refresher ----
	go reg.Run(ctx, cfg.RegistryRefresh, logger)

	// ---- MCP server ----
	mcpServer := mcpsrv.New(reg, exec)
	mcpServer.SyncTools()
	// Re-sync tools on every refresh interval to pick up new ones.
	go func() {
		t := time.NewTicker(cfg.RegistryRefresh)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				mcpServer.SyncTools()
			}
		}
	}()

	// ---- HTTP router ----
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(loggingMiddleware(logger))
	if len(cfg.CORSAllowedOrigins) > 0 {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins:   cfg.CORSAllowedOrigins,
			AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Authorization", "Content-Type"},
			AllowCredentials: true,
			MaxAge:           300,
		}))
	}
	// Security headers (only relevant for the SPA + API; MCP /mcp clients ignore them).
	r.Use(securityHeaders)

	if cfg.MetricsEnabled {
		r.Handle("/metrics", promhttp.Handler())
	}

	apiSrv := &api.API{
		Pool: pool, Signer: signer, Cipher: cipher,
		Registry: reg, Executor: exec, CodeRuntime: codeRT, LLM: llmClient, Cache: cch,
		DefaultRowLimit: cfg.DefaultRowLimit,
	}
	r.Route("/api", apiSrv.Mount)

	// MCP Streamable HTTP endpoint.
	r.Mount("/mcp", mcpServer.Handler())

	// SPA fallback — serve frontend/dist if present.
	if _, err := os.Stat("./frontend/dist"); err == nil {
		fs := http.FileServer(http.Dir("./frontend/dist"))
		r.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// SPA history fallback: if file doesn't exist, serve index.html.
			path := "./frontend/dist" + req.URL.Path
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				http.ServeFile(w, req, "./frontend/dist/index.html")
				return
			}
			fs.ServeHTTP(w, req)
		}))
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("http listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}

func newLogger(level string) *slog.Logger {
	lvl := slog.LevelInfo
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}

func loggingMiddleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

func bootstrapAdmin(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config) error {
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if cfg.BootstrapAdminPassword == "" {
		return errors.New("no users exist and BOOTSTRAP_ADMIN_PASSWORD not set")
	}
	hash, err := auth.HashPassword(cfg.BootstrapAdminPassword)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, role) VALUES ($1,$2,$3,'admin')`,
		uuid.New(), strings.ToLower(cfg.BootstrapAdminEmail), hash)
	if err == nil {
		slog.Info("bootstrap admin created", "email", cfg.BootstrapAdminEmail)
	}
	return err
}
