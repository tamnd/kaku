package tui

import (
	"strings"
	"testing"

	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/provider"
)

func TestFormatCount(t *testing.T) {
	cases := map[int]string{
		0:         "0",
		936:       "936",
		1000:      "1K",
		1200:      "1.2K",
		42000:     "42K",
		1_000_000: "1M",
		1_200_000: "1.2M",
	}
	for in, want := range cases {
		if got := formatCount(in); got != want {
			t.Errorf("formatCount(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatCost(t *testing.T) {
	if got := formatCost(0.4237); got != "$0.42" {
		t.Errorf("formatCost = %q, want $0.42", got)
	}
	if got := formatCost(0); got != "$0.00" {
		t.Errorf("formatCost(0) = %q, want $0.00", got)
	}
}

// chromeModel builds a model wired with a context-carrying model choice and a
// message history, enough to exercise the header and gauge.
func chromeModel(ctxLimit, msgChars int) *model {
	m := &model{}
	m.st = newStyles(builtinThemes["dark"])
	m.themeName = "dark"
	m.rt.Dir = "/work/deep/project/pkg/tui"
	m.rt.Model = "gemini"
	m.rt.Models = []ModelChoice{{Ref: "gemini", Context: ctxLimit}}
	m.rt.Agent = &engine.Agent{Model: "gemini"}
	if msgChars > 0 {
		m.rt.Agent.Messages = []provider.Message{
			{Content: []provider.Block{{Text: strings.Repeat("x", msgChars)}}},
		}
	}
	return m
}

func TestContextLimit(t *testing.T) {
	if got := chromeModel(128000, 0).contextLimit(); got != 128000 {
		t.Errorf("contextLimit = %d, want 128000", got)
	}
	if got := chromeModel(0, 0).contextLimit(); got != 0 {
		t.Errorf("contextLimit with unknown = %d, want 0", got)
	}
}

func TestContextGauge(t *testing.T) {
	// ~4 chars per token; 40000 chars is ~10000 tokens, ~10% of 100000.
	m := chromeModel(100_000, 40_000)
	if g := m.contextGauge(); !strings.Contains(g, "%") {
		t.Errorf("gauge missing percent: %q", g)
	}
	// Unknown limit hides the gauge.
	if g := chromeModel(0, 40_000).contextGauge(); g != "" {
		t.Errorf("gauge should be empty when limit unknown, got %q", g)
	}
	// Past 80% the gauge warns.
	warn := chromeModel(1000, 40_000).contextGauge() // way over
	if !strings.Contains(warn, "!") {
		t.Errorf("expected a warning marker past threshold: %q", warn)
	}
}

func TestHeaderFitsAndCarriesStats(t *testing.T) {
	m := chromeModel(100_000, 4_000)
	h := m.header(100)
	if !strings.Contains(h, wordmark) {
		t.Errorf("header missing wordmark: %q", h)
	}
	if !strings.Contains(h, "tokens") {
		t.Errorf("header missing token stat: %q", h)
	}
	// A very narrow width yields no header so the footer keeps the stats.
	if m.header(10) != "" {
		t.Error("expected empty header on a narrow terminal")
	}
}

func TestPrettyCwdTrimsDeepPath(t *testing.T) {
	m := chromeModel(0, 0)
	got := m.prettyCwd()
	if strings.Count(got, "/") > 2 || !strings.Contains(got, "...") {
		t.Errorf("prettyCwd did not trim a deep path: %q", got)
	}
}
