// Package oracle is a placeholder for the Oracle driver.
//
// Oracle support requires godror + Oracle Instant Client native libraries and
// is only built into the optional "oracle" image profile (see
// deploy/backend.oracle.Dockerfile). In the default build it returns
// ErrNotEnabled at construction time.
package oracle

import (
	"context"
	"errors"

	"github.com/marcos14/contextforge/backend/internal/drivers"
)

// ErrNotEnabled is returned by the default build when Oracle is not compiled in.
var ErrNotEnabled = errors.New("oracle driver not enabled in this build; rebuild with -tags oracle and provide Instant Client")

type stub struct{}

func (stub) Kind() drivers.Kind                                  { return drivers.KindOracle }
func (stub) Ping(context.Context) error                          { return ErrNotEnabled }
func (stub) Close() error                                        { return nil }
func (stub) Introspect(context.Context) ([]drivers.Table, error) { return nil, ErrNotEnabled }
func (stub) Execute(context.Context, drivers.ExecRequest) (*drivers.ExecResult, error) {
	return nil, ErrNotEnabled
}

// buildExplainPlanSQL and displayPlanSQL document Oracle's EXPLAIN syntax for a
// build compiled with -tags oracle. Oracle's EXPLAIN PLAN is a two-step
// operation: EXPLAIN PLAN FOR <q> populates PLAN_TABLE (without running the
// query), then a SELECT over DBMS_XPLAN.DISPLAY renders it. Kept as pure
// functions so the command format is unit-tested even though the default build
// ships a disabled stub.
func buildExplainPlanSQL(query string) string {
	return "EXPLAIN PLAN FOR " + query
}

func displayPlanSQL() string {
	return "SELECT plan_table_output FROM TABLE(DBMS_XPLAN.DISPLAY())"
}

// ensure the stub satisfies the optional EXPLAIN capability.
var _ drivers.Explainer = stub{}

// Explain satisfies drivers.Explainer. The default build ships a disabled stub
// (New always returns ErrNotEnabled, so this is unreachable in practice); a
// build with -tags oracle and Instant Client would implement it using
// buildExplainPlanSQL + displayPlanSQL against a live connection.
func (stub) Explain(context.Context, string, bool) (*drivers.ExplainResult, error) {
	return nil, ErrNotEnabled
}

func init() {
	drivers.Register(drivers.KindOracle, func(cfg []byte) (drivers.Driver, error) {
		return nil, ErrNotEnabled
	})
}
