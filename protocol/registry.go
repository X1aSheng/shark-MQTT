package protocol

import "fmt"

// Registry holds the registered protocol adapters and selects one for an
// accepted connection using an "explicit binding first, fingerprint sniffing
// otherwise" policy.
type Registry struct {
	adapters []Adapter
	byName   map[string]Adapter
}

// NewRegistry creates a registry pre-populated with the given adapters.
func NewRegistry(adapters ...Adapter) *Registry {
	r := &Registry{byName: make(map[string]Adapter)}
	for _, a := range adapters {
		r.Register(a)
	}
	return r
}

// Register adds an adapter. A later registration with the same name replaces
// the earlier one (both in the sniffing order and the name index).
func (r *Registry) Register(a Adapter) {
	if a == nil {
		return
	}
	name := a.Name()
	if _, exists := r.byName[name]; exists {
		for i, existing := range r.adapters {
			if existing.Name() == name {
				r.adapters[i] = a
				break
			}
		}
	} else {
		r.adapters = append(r.adapters, a)
	}
	r.byName[name] = a
}

// Lookup returns the adapter registered under name.
func (r *Registry) Lookup(name string) (Adapter, bool) {
	a, ok := r.byName[name]
	return a, ok
}

// Select returns the adapter that should handle conn:
//
//  1. when the listener explicitly binds a protocol name, that adapter is
//     returned directly;
//  2. otherwise the registered adapters are asked to Identify(conn) in order
//     and the first match wins.
//
// The connection passed to Select must be a PeekConn so sniffing does not
// consume the stream.
func (r *Registry) Select(conn PeekConn, bound string) (Adapter, error) {
	if bound != "" {
		if a, ok := r.byName[bound]; ok {
			return a, nil
		}
		return nil, fmt.Errorf("protocol: no adapter registered for bound protocol %q", bound)
	}
	for _, a := range r.adapters {
		ok, err := a.Identify(conn)
		if err != nil {
			return nil, err
		}
		if ok {
			return a, nil
		}
	}
	return nil, ErrNoMatchingAdapter
}

// Adapters returns the registered adapters in selection order (read-only view).
func (r *Registry) Adapters() []Adapter {
	out := make([]Adapter, len(r.adapters))
	copy(out, r.adapters)
	return out
}
