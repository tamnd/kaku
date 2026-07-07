package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandKey(t *testing.T) {
	t.Setenv("ZEN_KEY", "sk-zen-123")
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")
	if err := os.WriteFile(keyFile, []byte("  sk-file-456\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		in, want string
	}{
		{"sk-plain", "sk-plain"},
		{"{env:ZEN_KEY}", "sk-zen-123"},
		{"{file:" + keyFile + "}", "sk-file-456"},
		{"Bearer {env:ZEN_KEY}", "Bearer sk-zen-123"},
	}
	for _, c := range cases {
		got, err := expand(c.in)
		if err != nil {
			t.Fatalf("expand(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("expand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExpandKeyMissing(t *testing.T) {
	if _, err := expand("{env:DEFINITELY_NOT_SET_KAKU}"); err == nil {
		t.Error("missing env var should error")
	}
	if _, err := expand("{file:/no/such/path/kaku}"); err == nil {
		t.Error("missing file should error")
	}
}

func TestResolveDefault(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-default")
	c := Default()
	r, err := c.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if r.API != "anthropic" || r.Model != c.Model || r.APIKey != "sk-default" {
		t.Fatalf("default resolve = %+v", r)
	}
}

func TestResolveDefaultUsesAuthWhenEnvEmpty(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	c := Default()
	c.AuthLookup = func(p string) (string, bool) {
		if p == "anthropic" {
			return "sk-stored", true
		}
		return "", false
	}
	r, err := c.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if r.APIKey != "sk-stored" {
		t.Fatalf("stored key should fill in for an empty env, got %q", r.APIKey)
	}
}

func TestResolveEnvBeatsAuth(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-env")
	c := Default()
	c.AuthLookup = func(string) (string, bool) { return "sk-stored", true }
	r, err := c.Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if r.APIKey != "sk-env" {
		t.Fatalf("env var should win over the stored key, got %q", r.APIKey)
	}
}

func TestResolveNamedUsesAuthWhenKeyEmpty(t *testing.T) {
	c := Default()
	c.Providers = map[string]ProviderDef{
		"zen": {
			API:    "openai",
			Models: map[string]ModelDef{"big-pickle": {}},
		},
	}
	c.AuthLookup = func(p string) (string, bool) {
		if p == "zen" {
			return "sk-zen-stored", true
		}
		return "", false
	}
	r, err := c.Resolve("zen/big-pickle")
	if err != nil {
		t.Fatal(err)
	}
	if r.APIKey != "sk-zen-stored" {
		t.Fatalf("named provider should use the stored key, got %q", r.APIKey)
	}
}

func TestResolveNamedQualified(t *testing.T) {
	t.Setenv("OPENCODE_API_KEY", "sk-zen")
	c := Default()
	c.Providers = map[string]ProviderDef{
		"zen": {
			API:     "openai",
			BaseURL: "https://opencode.ai/zen/v1",
			APIKey:  "{env:OPENCODE_API_KEY}",
			Models:  map[string]ModelDef{"big-pickle": {Reasoning: "medium", MaxTokens: 32000}},
		},
	}
	r, err := c.Resolve("zen/big-pickle")
	if err != nil {
		t.Fatal(err)
	}
	if r.API != "openai" || r.BaseURL != "https://opencode.ai/zen/v1" || r.APIKey != "sk-zen" {
		t.Fatalf("qualified resolve = %+v", r)
	}
	if r.Model != "big-pickle" || r.Reasoning != "medium" || r.MaxTokens != 32000 {
		t.Fatalf("model settings = %+v", r)
	}
}

func TestResolveCarriesCost(t *testing.T) {
	t.Setenv("OPENCODE_API_KEY", "sk-zen")
	c := Default()
	c.Providers = map[string]ProviderDef{
		"zen": {
			API:    "openai",
			APIKey: "{env:OPENCODE_API_KEY}",
			Models: map[string]ModelDef{"big-pickle": {Cost: &Cost{Input: 3, Output: 15}}},
		},
	}
	r, err := c.Resolve("zen/big-pickle")
	if err != nil {
		t.Fatal(err)
	}
	if r.Cost == nil || r.Cost.Input != 3 || r.Cost.Output != 15 {
		t.Fatalf("resolved cost = %+v", r.Cost)
	}
	// A model with no cost leaves the field nil so the footer stays token-only.
	c.Providers["zen"].Models["free"] = ModelDef{}
	r2, _ := c.Resolve("zen/free")
	if r2.Cost != nil {
		t.Errorf("unpriced model should resolve with nil cost, got %+v", r2.Cost)
	}
}

func TestResolveBareSearchesProviders(t *testing.T) {
	t.Setenv("OPENCODE_API_KEY", "sk-zen")
	c := Default()
	c.Providers = map[string]ProviderDef{
		"zen": {
			API:    "openai",
			APIKey: "{env:OPENCODE_API_KEY}",
			Models: map[string]ModelDef{"big-pickle": {Reasoning: "low"}},
		},
	}
	r, err := c.Resolve("big-pickle")
	if err != nil {
		t.Fatal(err)
	}
	if r.API != "openai" || r.Model != "big-pickle" || r.Reasoning != "low" {
		t.Fatalf("bare resolve = %+v", r)
	}
}

func TestResolveLevelSuffix(t *testing.T) {
	t.Setenv("OPENCODE_API_KEY", "sk-zen")
	c := Default()
	c.Providers = map[string]ProviderDef{
		"zen": {API: "openai", APIKey: "{env:OPENCODE_API_KEY}", Models: map[string]ModelDef{"big-pickle": {Reasoning: "low"}}},
	}
	r, err := c.Resolve("zen/big-pickle:high")
	if err != nil {
		t.Fatal(err)
	}
	if r.Reasoning != "high" {
		t.Fatalf("level suffix ignored: %+v", r)
	}
}

func TestResolveUnknownProvider(t *testing.T) {
	c := Default()
	if _, err := c.Resolve("nope/model"); err == nil {
		t.Error("unknown provider should error")
	}
}

func TestResolveBareFallsBackToDefault(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-default")
	c := Default()
	// A bare name not in any provider map is treated as a default-provider model.
	r, err := c.Resolve("some-other-model")
	if err != nil {
		t.Fatal(err)
	}
	if r.API != "anthropic" || r.Model != "some-other-model" {
		t.Fatalf("fallback resolve = %+v", r)
	}
}

func TestModelsListing(t *testing.T) {
	c := Default()
	c.Providers = map[string]ProviderDef{
		"zen": {Models: map[string]ModelDef{"b-model": {}, "a-model": {Reasoning: "high"}}},
	}
	got := c.Models()
	if len(got) != 3 {
		t.Fatalf("want 3 models, got %d: %+v", len(got), got)
	}
	if !got[0].Default || got[0].Provider != "anthropic" {
		t.Errorf("first should be default: %+v", got[0])
	}
	// Named models sorted by model id within the provider.
	if got[1].Model != "a-model" || got[2].Model != "b-model" {
		t.Errorf("named models not sorted: %+v", got[1:])
	}
}
