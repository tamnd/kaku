// Package config loads kaku settings: built-in defaults, then the user's
// ~/.kaku/config.json, then the project's .kaku/settings.json.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

// MCPServer describes one MCP server to connect to. Command starts a stdio
// server; URL points at a streamable HTTP server. Exactly one is set.
type MCPServer struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
}

// Hook is one lifecycle hook entry. Match filters by tool name glob for
// tool events and is ignored for the rest.
type Hook struct {
	Match   string `json:"match,omitempty"`
	Command string `json:"command"`
}

// Permissions mirrors the permissions block in settings.
type Permissions struct {
	Mode  string   `json:"mode,omitempty"`
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// Config is the merged view the rest of kaku consumes.
type Config struct {
	Provider   string `json:"provider,omitempty"` // "anthropic" or "openai"
	Model      string `json:"model,omitempty"`
	SmallModel string `json:"small_model,omitempty"` // summarizer and other cheap calls
	BaseURL    string `json:"base_url,omitempty"`
	APIKeyEnv  string `json:"api_key_env,omitempty"`
	Reasoning  string `json:"reasoning,omitempty"` // global default reasoning level
	MaxTokens  int    `json:"max_tokens,omitempty"`
	MaxTurns   int    `json:"max_turns,omitempty"`
	Theme      string `json:"theme,omitempty"` // TUI theme name (dark, light, or a custom one)

	// ModelCycle is an ordered list of model refs that ctrl+n steps through in
	// the TUI. Empty means cycle every resolvable model.
	ModelCycle []string `json:"models,omitempty"`

	Permissions  Permissions            `json:"permissions"`
	MCPServers   map[string]MCPServer   `json:"mcpServers,omitempty"`
	Providers    map[string]ProviderDef `json:"providers,omitempty"` // named custom providers
	Hooks        map[string][]Hook      `json:"hooks,omitempty"`
	Instructions []string               `json:"instructions,omitempty"` // extra instruction-file globs
	Tools        map[string]bool        `json:"tools,omitempty"`        // enable/disable tools by name glob
	Formatter    FormatterConfig        `json:"formatter"`              // post-write formatting

	// AuthLookup, when set, supplies a stored API key for a provider name when
	// no explicit key resolves from config or the environment. build wires it
	// to the on-disk auth store. It is never serialized.
	AuthLookup func(provider string) (string, bool) `json:"-"`
}

// FormatterConfig configures formatting after a write or edit. It accepts a
// bare bool in JSON (true enables the builtin formatters, false or absent is
// off) or an object mapping a formatter name to its overrides, which also
// enables the builtins.
type FormatterConfig struct {
	Enabled bool
	Specs   map[string]FormatterSpec
}

// FormatterSpec overrides a builtin formatter or registers a custom one.
type FormatterSpec struct {
	Disabled   bool     `json:"disabled,omitempty"`
	Command    []string `json:"command,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
}

// UnmarshalJSON accepts either a bool or an object of name -> spec.
func (f *FormatterConfig) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "true" || trimmed == "false" {
		f.Enabled = trimmed == "true"
		f.Specs = nil
		return nil
	}
	var m map[string]FormatterSpec
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	f.Enabled = true
	f.Specs = m
	return nil
}

// Default returns the built-in configuration.
func Default() *Config {
	return &Config{
		Provider:   "anthropic",
		Model:      "claude-sonnet-5",
		SmallModel: "claude-haiku-4-5",
		APIKeyEnv:  "ANTHROPIC_API_KEY",
		MaxTokens:  16384,
		MaxTurns:   80,
		Permissions: Permissions{
			Mode: "ask",
		},
	}
}

// Load merges defaults with the user config and the project settings found
// under dir. Missing files are fine; malformed files are an error.
func Load(dir string) (*Config, error) {
	c := Default()
	home, err := os.UserHomeDir()
	if err == nil {
		if err := mergeFile(c, filepath.Join(home, ".kaku", "config.json")); err != nil {
			return nil, err
		}
	}
	if err := mergeFile(c, filepath.Join(dir, ".kaku", "settings.json")); err != nil {
		return nil, err
	}
	return c, nil
}

func mergeFile(c *Config, path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var over Config
	if err := json.Unmarshal(data, &over); err != nil {
		return err
	}
	merge(c, &over)
	return nil
}

func merge(c, over *Config) {
	if over.Provider != "" {
		c.Provider = over.Provider
	}
	if over.Model != "" {
		c.Model = over.Model
	}
	if over.SmallModel != "" {
		c.SmallModel = over.SmallModel
	}
	if over.BaseURL != "" {
		c.BaseURL = over.BaseURL
	}
	if over.APIKeyEnv != "" {
		c.APIKeyEnv = over.APIKeyEnv
	}
	if over.Reasoning != "" {
		c.Reasoning = over.Reasoning
	}
	if over.MaxTokens != 0 {
		c.MaxTokens = over.MaxTokens
	}
	if over.MaxTurns != 0 {
		c.MaxTurns = over.MaxTurns
	}
	if over.Theme != "" {
		c.Theme = over.Theme
	}
	if len(over.ModelCycle) > 0 {
		c.ModelCycle = over.ModelCycle
	}
	if over.Permissions.Mode != "" {
		c.Permissions.Mode = over.Permissions.Mode
	}
	c.Permissions.Allow = append(c.Permissions.Allow, over.Permissions.Allow...)
	c.Permissions.Deny = append(c.Permissions.Deny, over.Permissions.Deny...)
	if len(over.MCPServers) > 0 {
		if c.MCPServers == nil {
			c.MCPServers = map[string]MCPServer{}
		}
		maps.Copy(c.MCPServers, over.MCPServers)
	}
	if len(over.Providers) > 0 {
		if c.Providers == nil {
			c.Providers = map[string]ProviderDef{}
		}
		maps.Copy(c.Providers, over.Providers)
	}
	if len(over.Hooks) > 0 {
		if c.Hooks == nil {
			c.Hooks = map[string][]Hook{}
		}
		for k, v := range over.Hooks {
			c.Hooks[k] = append(c.Hooks[k], v...)
		}
	}
	c.Instructions = append(c.Instructions, over.Instructions...)
	if len(over.Tools) > 0 {
		if c.Tools == nil {
			c.Tools = map[string]bool{}
		}
		maps.Copy(c.Tools, over.Tools)
	}
	if over.Formatter.Enabled || len(over.Formatter.Specs) > 0 {
		c.Formatter = over.Formatter
	}
}

// APIKey resolves the key from the configured environment variable.
func (c *Config) APIKey() string {
	if c.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(c.APIKeyEnv)
}
