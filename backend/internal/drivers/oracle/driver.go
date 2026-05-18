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

func init() {
	drivers.Register(drivers.KindOracle, func(cfg []byte) (drivers.Driver, error) {
		return nil, ErrNotEnabled
	})
}
