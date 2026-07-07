// Package tool defines the tool SPI and the registry the engine draws from.
package tool

import (
	"context"
	"encoding/json"
	"sort"
	"sync"

	"github.com/tamnd/kaku/pkg/provider"
)

// Tool is one capability the model can invoke.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Run(ctx context.Context, input json.RawMessage) (string, error)
}

// ReadOnlyMarker is implemented by tools that never mutate anything.
// The permission engine auto-allows them in plan and ask modes.
type ReadOnlyMarker interface {
	ReadOnly() bool
}

// Func adapts a plain function into a Tool.
type Func struct {
	ToolName    string
	Desc        string
	InputSchema json.RawMessage
	Safe        bool // read-only
	Fn          func(ctx context.Context, input json.RawMessage) (string, error)
}

func (f Func) Name() string            { return f.ToolName }
func (f Func) Description() string     { return f.Desc }
func (f Func) Schema() json.RawMessage { return f.InputSchema }
func (f Func) ReadOnly() bool          { return f.Safe }
func (f Func) Run(ctx context.Context, input json.RawMessage) (string, error) {
	return f.Fn(ctx, input)
}

// Registry holds the tools available to one agent.
type Registry struct {
	mu     sync.RWMutex
	byName map[string]Tool
}

// NewRegistry builds a registry from the given tools.
func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{byName: map[string]Tool{}}
	for _, t := range tools {
		r.Add(t)
	}
	return r
}

// Add registers a tool, replacing any existing tool with the same name.
func (r *Registry) Add(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byName[t.Name()] = t
}

// Remove drops a tool by name. Removing a missing tool is a no-op.
func (r *Registry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byName, name)
}

// Get looks a tool up by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.byName[name]
	return t, ok
}

// List returns all tools sorted by name.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.byName))
	for _, t := range r.byName {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// ReadOnly reports whether the named tool is marked read-only.
func (r *Registry) ReadOnly(name string) bool {
	t, ok := r.Get(name)
	if !ok {
		return false
	}
	m, ok := t.(ReadOnlyMarker)
	return ok && m.ReadOnly()
}

// Defs renders the registry as provider tool definitions.
func (r *Registry) Defs() []provider.ToolDef {
	tools := r.List()
	defs := make([]provider.ToolDef, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, provider.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      t.Schema(),
		})
	}
	return defs
}
