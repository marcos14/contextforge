package drivers

import (
	"context"
	"errors"
	"testing"
)

// baseDriver implements Driver but NOT Explainer.
type baseDriver struct{}

func (baseDriver) Kind() Kind                                  { return KindMongo }
func (baseDriver) Ping(context.Context) error                  { return nil }
func (baseDriver) Introspect(context.Context) ([]Table, error) { return nil, nil }
func (baseDriver) Execute(context.Context, ExecRequest) (*ExecResult, error) {
	return nil, nil
}
func (baseDriver) Close() error { return nil }

// explainDriver embeds baseDriver and implements Explainer.
type explainDriver struct {
	baseDriver
	gotQuery   string
	gotAnalyze bool
}

func (e *explainDriver) Explain(_ context.Context, query string, analyze bool) (*ExplainResult, error) {
	e.gotQuery = query
	e.gotAnalyze = analyze
	return &ExplainResult{Dialect: "pg", Plan: "[]", Format: "json", Analyze: analyze}, nil
}

func TestExplain_UnsupportedDriver(t *testing.T) {
	_, err := Explain(context.Background(), baseDriver{}, "SELECT 1", false)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestExplain_DelegatesToExplainer(t *testing.T) {
	d := &explainDriver{}
	res, err := Explain(context.Background(), d, "SELECT 1", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.gotQuery != "SELECT 1" || d.gotAnalyze != true {
		t.Fatalf("Explain did not delegate correctly: query=%q analyze=%v", d.gotQuery, d.gotAnalyze)
	}
	if res == nil || res.Dialect != "pg" || !res.Analyze {
		t.Fatalf("unexpected result: %+v", res)
	}
}
