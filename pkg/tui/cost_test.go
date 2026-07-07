package tui

import (
	"testing"

	"github.com/tamnd/kaku/pkg/engine"
	"github.com/tamnd/kaku/pkg/provider"
)

func TestEstimatedCost(t *testing.T) {
	m := &model{rt: Runtime{
		Agent: &engine.Agent{},
		Cost:  func() (float64, float64, bool) { return 3, 15, true },
	}}
	// 1M input at $3 + 0.5M output at $15 = 3 + 7.5 = $10.5000
	got := m.estimatedCost(provider.Usage{InputTokens: 1_000_000, OutputTokens: 500_000})
	if got != "$10.5000" {
		t.Errorf("estimatedCost = %q, want $10.5000", got)
	}
}

func TestEstimatedCostHiddenWithoutPrice(t *testing.T) {
	m := &model{rt: Runtime{Agent: &engine.Agent{}}}
	if got := m.estimatedCost(provider.Usage{InputTokens: 100}); got != "" {
		t.Errorf("no Cost hook should hide the estimate, got %q", got)
	}
	m.rt.Cost = func() (float64, float64, bool) { return 0, 0, false }
	if got := m.estimatedCost(provider.Usage{InputTokens: 100}); got != "" {
		t.Errorf("ok=false should hide the estimate, got %q", got)
	}
}
