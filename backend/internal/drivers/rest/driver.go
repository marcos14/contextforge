package rest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/marcos14/contextforge/backend/internal/drivers"
)

// Auth describes the auth scheme to use against the upstream API.
type Auth struct {
	Type   string `json:"type"` // "none" | "bearer" | "api_key" | "basic"
	Token  string `json:"token,omitempty"`
	Header string `json:"header,omitempty"` // for api_key (e.g. "X-API-Key")
	User   string `json:"user,omitempty"`   // for basic
	Pass   string `json:"pass,omitempty"`
}

// Config is the REST connection configuration.
type Config struct {
	BaseURL string            `json:"base_url"` // e.g. https://api.example.com
	Headers map[string]string `json:"headers,omitempty"`
	Auth    Auth              `json:"auth"`
}

type driver struct {
	cfg    Config
	client *http.Client
}

// QuerySpec describes one REST tool's "query".
// Path may contain :name placeholders that are substituted from params.
// Query string params are taken from the "query" map; values may also be
// :name placeholders. Body is not supported (GET only in MVP).
type QuerySpec struct {
	Method string            `json:"method,omitempty"` // default GET
	Path   string            `json:"path"`             // e.g. /users/:id
	Query  map[string]string `json:"query,omitempty"`
}

func New(cfg []byte) (drivers.Driver, error) {
	var c Config
	if err := json.Unmarshal(cfg, &c); err != nil {
		return nil, fmt.Errorf("rest config: %w", err)
	}
	if c.BaseURL == "" {
		return nil, errors.New("rest: base_url required")
	}
	return &driver{
		cfg:    c,
		client: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (d *driver) Kind() drivers.Kind { return drivers.KindREST }
func (d *driver) Close() error       { return nil }
func (d *driver) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.cfg.BaseURL, nil)
	if err != nil {
		return err
	}
	d.applyAuth(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Introspect is not meaningful for generic REST. Returns an empty list.
func (d *driver) Introspect(ctx context.Context) ([]drivers.Table, error) {
	return []drivers.Table{}, nil
}

func (d *driver) Execute(ctx context.Context, req drivers.ExecRequest) (*drivers.ExecResult, error) {
	var spec QuerySpec
	if err := json.Unmarshal([]byte(req.Query), &spec); err != nil {
		return nil, fmt.Errorf("rest query is not valid JSON: %w", err)
	}
	if spec.Method == "" {
		spec.Method = http.MethodGet
	}
	if !strings.EqualFold(spec.Method, http.MethodGet) {
		return nil, errors.New("rest: only GET is supported in MVP")
	}

	path := spec.Path
	for k, v := range req.Params {
		path = strings.ReplaceAll(path, ":"+k, fmt.Sprint(v))
	}
	u, err := url.Parse(strings.TrimRight(d.cfg.BaseURL, "/") + "/" + strings.TrimLeft(path, "/"))
	if err != nil {
		return nil, err
	}
	q := u.Query()
	for k, raw := range spec.Query {
		val := raw
		if strings.HasPrefix(raw, ":") {
			p, ok := req.Params[raw[1:]]
			if !ok {
				return nil, fmt.Errorf("missing parameter %q", raw[1:])
			}
			val = fmt.Sprint(p)
		}
		q.Set(k, val)
	}
	u.RawQuery = q.Encode()

	cctx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	hr, err := http.NewRequestWithContext(cctx, spec.Method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	for k, v := range d.cfg.Headers {
		hr.Header.Set(k, v)
	}
	d.applyAuth(hr)

	resp, err := d.client.Do(hr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("rest: upstream returned %d: %s", resp.StatusCode, truncate(string(body), 256))
	}
	return parseBody(body, req.RowLimit)
}

func (d *driver) applyAuth(r *http.Request) {
	switch d.cfg.Auth.Type {
	case "bearer":
		r.Header.Set("Authorization", "Bearer "+d.cfg.Auth.Token)
	case "api_key":
		h := d.cfg.Auth.Header
		if h == "" {
			h = "X-API-Key"
		}
		r.Header.Set(h, d.cfg.Auth.Token)
	case "basic":
		creds := d.cfg.Auth.User + ":" + d.cfg.Auth.Pass
		r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(creds)))
	}
}

func parseBody(body []byte, rowLimit int) (*drivers.ExecResult, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		// Return raw string under a single "body" column.
		return &drivers.ExecResult{
			Columns: []string{"body"},
			Rows:    []map[string]any{{"body": string(body)}},
			Count:   1,
		}, nil
	}
	switch t := v.(type) {
	case []any:
		res := &drivers.ExecResult{Rows: []map[string]any{}}
		cols := map[string]struct{}{}
		for _, el := range t {
			if rowLimit > 0 && res.Count >= rowLimit {
				break
			}
			row, ok := el.(map[string]any)
			if !ok {
				row = map[string]any{"value": el}
			}
			for k := range row {
				cols[k] = struct{}{}
			}
			res.Rows = append(res.Rows, row)
			res.Count++
		}
		for k := range cols {
			res.Columns = append(res.Columns, k)
		}
		return res, nil
	case map[string]any:
		res := &drivers.ExecResult{Rows: []map[string]any{t}, Count: 1}
		for k := range t {
			res.Columns = append(res.Columns, k)
		}
		return res, nil
	default:
		return &drivers.ExecResult{
			Columns: []string{"value"},
			Rows:    []map[string]any{{"value": v}},
			Count:   1,
		}, nil
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func init() { drivers.Register(drivers.KindREST, New) }
