package drivers

import "fmt"

// Registry maps a connection type string to a Factory.
var Registry = map[string]Factory{}

// Register adds a factory under the given kind.
func Register(kind Kind, f Factory) { Registry[string(kind)] = f }

// Build builds a driver for the given kind and JSON config.
func Build(kind string, cfg []byte) (Driver, error) {
	f, ok := Registry[kind]
	if !ok {
		return nil, fmt.Errorf("unknown connection type %q", kind)
	}
	return f(cfg)
}
