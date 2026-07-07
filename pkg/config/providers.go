package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ProviderDef is one named provider entry: an endpoint, its wire format, a
// credential, and the models it serves. It mirrors opencode's provider.<id>.
type ProviderDef struct {
	API     string              `json:"api"`      // anthropic|openai|responses
	BaseURL string              `json:"base_url"` //
	APIKey  string              `json:"api_key"`  // supports {env:VAR} and {file:path}
	Headers map[string]string   `json:"headers,omitempty"`
	Models  map[string]ModelDef `json:"models,omitempty"`
}

// ModelDef carries per-model settings. Every field is optional.
type ModelDef struct {
	Reasoning string `json:"reasoning,omitempty"` // off|minimal|low|medium|high|xhigh
	MaxTokens int    `json:"max_tokens,omitempty"`
	Context   int    `json:"context,omitempty"`
	Name      string `json:"name,omitempty"` // display only
	Cost      *Cost  `json:"cost,omitempty"` // USD per million tokens, for the footer estimate
}

// Cost is a model's price in USD per million tokens, used to estimate a run's
// spend in the TUI footer. Nil on a model means the footer shows tokens only.
type Cost struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// Resolved is the concrete set of settings for one model reference, with the
// credential already expanded. It is what build() constructs a provider from.
type Resolved struct {
	API       string
	BaseURL   string
	APIKey    string
	Headers   map[string]string
	Model     string
	Reasoning string
	MaxTokens int
	Cost      *Cost
}

// Resolve turns a model reference into concrete provider settings. A reference
// is "provider/model", a bare "model", or "" for the configured default. A
// trailing ":level" sets reasoning for the run and overrides the model default.
//
// A bare model is searched for in the default provider first, then in each
// named provider in declaration-agnostic order (maps have no order, so a bare
// name that appears in more than one provider is ambiguous and the first stable
// match by sorted provider name wins). Prefer "provider/model" when a name is
// not unique.
func (c *Config) Resolve(ref string) (Resolved, error) {
	ref, level := splitLevel(ref)
	prov, model := splitProvider(ref)

	// Default provider path: no "provider/" prefix and the model is not found
	// in the named map, or the model matches the flat default.
	if prov == "" {
		if r, ok, err := c.resolveNamed("", model); err != nil {
			return Resolved{}, err
		} else if ok {
			return withLevel(r, level), nil
		}
		return withLevel(c.resolveDefault(model), level), nil
	}

	r, ok, err := c.resolveNamed(prov, model)
	if err != nil {
		return Resolved{}, err
	}
	if !ok {
		return Resolved{}, fmt.Errorf("unknown provider %q in model reference %q", prov, ref)
	}
	return withLevel(r, level), nil
}

// resolveDefault builds a Resolved from the flat top-level fields. An empty
// model means "use the configured default model".
func (c *Config) resolveDefault(model string) Resolved {
	if model == "" {
		model = c.Model
	}
	key := c.APIKey()
	if key == "" && c.AuthLookup != nil {
		if k, ok := c.AuthLookup(c.Provider); ok {
			key = k
		}
	}
	return Resolved{
		API:       c.Provider,
		BaseURL:   c.BaseURL,
		APIKey:    key,
		Model:     model,
		MaxTokens: c.MaxTokens,
	}
}

// resolveNamed looks a model up in the named provider map. With prov == "" it
// searches every provider by sorted name and returns the first match. It
// returns ok == false when nothing matches so the caller can fall back to the
// default provider.
func (c *Config) resolveNamed(prov, model string) (Resolved, bool, error) {
	if len(c.Providers) == 0 || model == "" {
		return Resolved{}, false, nil
	}
	names := sortedKeys(c.Providers)
	for _, name := range names {
		if prov != "" && name != prov {
			continue
		}
		def := c.Providers[name]
		md, ok := def.Models[model]
		if !ok {
			continue
		}
		key, err := expand(def.APIKey)
		if err != nil {
			return Resolved{}, false, fmt.Errorf("provider %q: %w", name, err)
		}
		if key == "" && c.AuthLookup != nil {
			if k, ok := c.AuthLookup(name); ok {
				key = k
			}
		}
		max := md.MaxTokens
		if max == 0 {
			max = c.MaxTokens
		}
		return Resolved{
			API:       def.API,
			BaseURL:   def.BaseURL,
			APIKey:    key,
			Headers:   def.Headers,
			Model:     model,
			Reasoning: md.Reasoning,
			MaxTokens: max,
			Cost:      md.Cost,
		}, true, nil
	}
	return Resolved{}, false, nil
}

// withLevel applies a run-level reasoning override.
func withLevel(r Resolved, level string) Resolved {
	if level != "" {
		r.Reasoning = level
	}
	return r
}

// splitLevel peels a trailing ":level" off a model reference. A "/" after the
// colon (as in a URL) is not a level, but references never contain one.
func splitLevel(ref string) (rest, level string) {
	i := strings.LastIndex(ref, ":")
	if i < 0 {
		return ref, ""
	}
	return ref[:i], ref[i+1:]
}

// splitProvider splits "provider/model" into its parts. A bare model returns an
// empty provider. Only the first slash splits, so a model id may contain more.
func splitProvider(ref string) (provider, model string) {
	if p, m, ok := strings.Cut(ref, "/"); ok {
		return p, m
	}
	return "", ref
}

var expandRef = regexp.MustCompile(`\{(env|file):([^}]*)\}`)

// expand replaces {env:VAR} with the environment value and {file:path} with the
// file's trimmed contents (~ expanded). A plain string with no reference passes
// through. A missing env var or unreadable file is an error, not a silent
// empty string, so a misconfigured key fails loudly.
func expand(s string) (string, error) {
	if !strings.Contains(s, "{") {
		return s, nil
	}
	var firstErr error
	out := expandRef.ReplaceAllStringFunc(s, func(m string) string {
		g := expandRef.FindStringSubmatch(m)
		kind, arg := g[1], g[2]
		switch kind {
		case "env":
			v, ok := os.LookupEnv(arg)
			if !ok {
				if firstErr == nil {
					firstErr = fmt.Errorf("env var %q is not set", arg)
				}
				return ""
			}
			return v
		case "file":
			path := arg
			if strings.HasPrefix(path, "~/") {
				if home, err := os.UserHomeDir(); err == nil {
					path = filepath.Join(home, path[2:])
				}
			}
			data, err := os.ReadFile(path)
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("reading key file %q: %w", arg, err)
				}
				return ""
			}
			return strings.TrimSpace(string(data))
		}
		return m
	})
	if firstErr != nil {
		return "", firstErr
	}
	return out, nil
}

// Models lists every model kaku can resolve, the default pair first, then every
// named provider's models sorted by provider then model. It is what the
// "kaku models" command prints.
func (c *Config) Models() []ModelInfo {
	var out []ModelInfo
	if c.Model != "" {
		out = append(out, ModelInfo{Provider: c.Provider, Model: c.Model, Default: true})
	}
	for _, name := range sortedKeys(c.Providers) {
		def := c.Providers[name]
		for _, m := range sortedKeys(def.Models) {
			md := def.Models[m]
			out = append(out, ModelInfo{
				Provider:  name,
				Model:     m,
				Reasoning: md.Reasoning,
				Context:   md.Context,
			})
		}
	}
	return out
}

// ModelInfo is one row of the models listing.
type ModelInfo struct {
	Provider  string
	Model     string
	Reasoning string
	Context   int
	Default   bool
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
