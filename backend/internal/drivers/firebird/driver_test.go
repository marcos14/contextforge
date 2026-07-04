package firebird

import (
	"context"
	"errors"
	"testing"

	"github.com/marcos14/contextforge/backend/internal/drivers"
)

// Firebird has no server-side EXPLAIN reachable over the wire protocol, so the
// driver reports the capability as unsupported for both estimated and analyze
// modes.
func TestExplainUnsupported(t *testing.T) {
	d := &driver{}
	for _, analyze := range []bool{false, true} {
		if _, err := d.Explain(context.Background(), "SELECT 1 FROM RDB$DATABASE", analyze); !errors.Is(err, drivers.ErrUnsupported) {
			t.Fatalf("Explain(analyze=%v): expected ErrUnsupported, got %v", analyze, err)
		}
	}
}
