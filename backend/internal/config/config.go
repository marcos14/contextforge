package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration loaded from the environment.
type Config struct {
	HTTPAddr           string
	PublicBaseURL      string
	LogLevel           string
	CORSAllowedOrigins []string

	DatabaseURL string
	RedisURL    string

	MasterKey []byte // 32 raw bytes
	JWTSecret []byte

	JWTAccessTTL  time.Duration
	JWTRefreshTTL time.Duration

	RegistryRefresh   time.Duration
	RateLimitIPPerMin int

	OpenRouterAPIKey  string
	OpenRouterModel   string
	OpenRouterBaseURL string
	OpenRouterTimeout time.Duration

	BootstrapAdminEmail    string
	BootstrapAdminPassword string

	DefaultRowLimit int

	// QueryStudioAllowAnalyze gates the EXPLAIN ANALYZE capability of the Query
	// Studio module. When false, /query-studio/explain rejects analyze:true
	// requests (defence in depth over the UI confirmation). Defaults to true.
	QueryStudioAllowAnalyze bool

	OTELEndpoint   string
	OTELService    string
	MetricsEnabled bool
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	c := &Config{
		HTTPAddr:                envStr("HTTP_ADDR", ":8080"),
		PublicBaseURL:           envStr("PUBLIC_BASE_URL", "http://localhost:8080"),
		LogLevel:                envStr("LOG_LEVEL", "info"),
		DatabaseURL:             os.Getenv("DATABASE_URL"),
		RedisURL:                envStr("REDIS_URL", "redis://localhost:6379/0"),
		JWTAccessTTL:            time.Duration(envInt("JWT_ACCESS_TTL_MINUTES", 60)) * time.Minute,
		JWTRefreshTTL:           time.Duration(envInt("JWT_REFRESH_TTL_DAYS", 30)) * 24 * time.Hour,
		RegistryRefresh:         time.Duration(envInt("REGISTRY_REFRESH_SECONDS", 5)) * time.Second,
		RateLimitIPPerMin:       envInt("RATE_LIMIT_IP_PER_MIN", 600),
		OpenRouterAPIKey:        os.Getenv("OPENROUTER_API_KEY"),
		OpenRouterModel:         envStr("OPENROUTER_MODEL", "xiaomi/mimo-v2.5"),
		OpenRouterBaseURL:       envStr("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"),
		OpenRouterTimeout:       time.Duration(envInt("OPENROUTER_TIMEOUT_SECONDS", 60)) * time.Second,
		BootstrapAdminEmail:     envStr("BOOTSTRAP_ADMIN_EMAIL", "admin@example.com"),
		BootstrapAdminPassword:  os.Getenv("BOOTSTRAP_ADMIN_PASSWORD"),
		DefaultRowLimit:         envInt("TOOL_DEFAULT_ROW_LIMIT", 1000),
		QueryStudioAllowAnalyze: envBool("QUERY_STUDIO_ALLOW_ANALYZE", true),
		OTELEndpoint:            os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		OTELService:             envStr("OTEL_SERVICE_NAME", "contextforge"),
		MetricsEnabled:          envBool("METRICS_ENABLED", true),
	}

	if origins := os.Getenv("CORS_ALLOWED_ORIGINS"); origins != "" {
		for _, o := range strings.Split(origins, ",") {
			if t := strings.TrimSpace(o); t != "" {
				c.CORSAllowedOrigins = append(c.CORSAllowedOrigins, t)
			}
		}
	}

	if c.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL is required")
	}

	mkRaw := os.Getenv("MASTER_KEY")
	if mkRaw == "" {
		return nil, errors.New("MASTER_KEY is required (base64-encoded 32 bytes)")
	}
	mk, err := base64.StdEncoding.DecodeString(mkRaw)
	if err != nil {
		return nil, fmt.Errorf("MASTER_KEY base64 decode: %w", err)
	}
	if len(mk) != 32 {
		return nil, fmt.Errorf("MASTER_KEY must decode to 32 bytes, got %d", len(mk))
	}
	c.MasterKey = mk

	js := os.Getenv("JWT_SECRET")
	if len(js) < 16 {
		return nil, errors.New("JWT_SECRET is required (>=16 chars)")
	}
	c.JWTSecret = []byte(js)

	return c, nil
}
