package mongo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/marcos14/contextforge/backend/internal/drivers"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Config is the MongoDB connection configuration.
type Config struct {
	URI      string `json:"uri"`      // mongodb://...
	Database string `json:"database"` // default database
}

type driver struct {
	client *mongo.Client
	dbName string
}

// QuerySpec is the JSON-encoded form of a Mongo tool's "query".
// Either Aggregate (pipeline) or Find (filter+options) must be set.
type QuerySpec struct {
	Collection string            `json:"collection"`
	Aggregate  []json.RawMessage `json:"aggregate,omitempty"`
	Find       *FindSpec         `json:"find,omitempty"`
}

type FindSpec struct {
	Filter     json.RawMessage `json:"filter,omitempty"`
	Projection json.RawMessage `json:"projection,omitempty"`
	Sort       json.RawMessage `json:"sort,omitempty"`
	Limit      int64           `json:"limit,omitempty"`
}

func New(cfg []byte) (drivers.Driver, error) {
	var c Config
	if err := json.Unmarshal(cfg, &c); err != nil {
		return nil, fmt.Errorf("mongo config: %w", err)
	}
	if c.URI == "" {
		return nil, fmt.Errorf("mongo: uri required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cli, err := mongo.Connect(ctx, options.Client().ApplyURI(c.URI))
	if err != nil {
		return nil, err
	}
	return &driver{client: cli, dbName: c.Database}, nil
}

func (d *driver) Kind() drivers.Kind             { return drivers.KindMongo }
func (d *driver) Ping(ctx context.Context) error { return d.client.Ping(ctx, nil) }
func (d *driver) Close() error                   { return d.client.Disconnect(context.Background()) }

func (d *driver) Introspect(ctx context.Context) ([]drivers.Table, error) {
	db := d.client.Database(d.dbName)
	names, err := db.ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	out := make([]drivers.Table, 0, len(names))
	for _, n := range names {
		out = append(out, drivers.Table{Schema: d.dbName, Name: n})
	}
	return out, nil
}

// substituteParams walks a JSON value and replaces "$param:name" strings with
// the value from params. This allows users to write Mongo specs with named
// parameter placeholders while keeping injection impossible.
func substituteParams(raw json.RawMessage, params map[string]any) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return walk(v, params)
}

func walk(v any, p map[string]any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			nv, err := walk(val, p)
			if err != nil {
				return nil, err
			}
			t[k] = nv
		}
		return t, nil
	case []any:
		for i, val := range t {
			nv, err := walk(val, p)
			if err != nil {
				return nil, err
			}
			t[i] = nv
		}
		return t, nil
	case string:
		if len(t) > 7 && t[:7] == "$param:" {
			name := t[7:]
			val, ok := p[name]
			if !ok {
				return nil, fmt.Errorf("missing parameter %q", name)
			}
			return val, nil
		}
		return t, nil
	default:
		return t, nil
	}
}

func (d *driver) Execute(ctx context.Context, req drivers.ExecRequest) (*drivers.ExecResult, error) {
	var spec QuerySpec
	if err := json.Unmarshal([]byte(req.Query), &spec); err != nil {
		return nil, fmt.Errorf("mongo query is not valid JSON: %w", err)
	}
	if spec.Collection == "" {
		return nil, fmt.Errorf("mongo: collection required")
	}
	cctx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	coll := d.client.Database(d.dbName).Collection(spec.Collection)

	var cursor *mongo.Cursor
	var err error
	switch {
	case len(spec.Aggregate) > 0:
		pipeline := make([]any, 0, len(spec.Aggregate))
		for _, st := range spec.Aggregate {
			s, err := substituteParams(st, req.Params)
			if err != nil {
				return nil, err
			}
			pipeline = append(pipeline, s)
		}
		cursor, err = coll.Aggregate(cctx, pipeline)
	case spec.Find != nil:
		filter, err2 := substituteParams(spec.Find.Filter, req.Params)
		if err2 != nil {
			return nil, err2
		}
		if filter == nil {
			filter = bson.D{}
		}
		opts := options.Find()
		if len(spec.Find.Projection) > 0 {
			pv, _ := substituteParams(spec.Find.Projection, req.Params)
			opts.SetProjection(pv)
		}
		if len(spec.Find.Sort) > 0 {
			sv, _ := substituteParams(spec.Find.Sort, req.Params)
			opts.SetSort(sv)
		}
		if spec.Find.Limit > 0 {
			opts.SetLimit(spec.Find.Limit)
		} else if req.RowLimit > 0 {
			opts.SetLimit(int64(req.RowLimit))
		}
		cursor, err = coll.Find(cctx, filter, opts)
	default:
		return nil, fmt.Errorf("mongo: either find or aggregate must be set")
	}
	if err != nil {
		return nil, err
	}
	defer cursor.Close(cctx)

	res := &drivers.ExecResult{Rows: []map[string]any{}}
	colset := map[string]struct{}{}
	for cursor.Next(cctx) {
		if req.RowLimit > 0 && res.Count >= req.RowLimit {
			break
		}
		var doc map[string]any
		if err := cursor.Decode(&doc); err != nil {
			return nil, err
		}
		for k := range doc {
			colset[k] = struct{}{}
		}
		res.Rows = append(res.Rows, doc)
		res.Count++
	}
	for k := range colset {
		res.Columns = append(res.Columns, k)
	}
	return res, cursor.Err()
}

func init() { drivers.Register(drivers.KindMongo, New) }
