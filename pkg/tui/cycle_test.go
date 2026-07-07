package tui

import (
	"errors"
	"testing"

	"github.com/tamnd/kaku/pkg/engine"
)

func newCycleModel(cycle []string, current string) *model {
	m := &model{rt: Runtime{
		Agent:      &engine.Agent{Model: current},
		Model:      current,
		ModelCycle: cycle,
	}, themes: LoadThemes(), themeName: "dark"}
	m.st = newStyles(builtinThemes["dark"])
	// switchModel sets rt.Model from Agent.Model, so mirror the ref on switch.
	m.rt.SwitchModel = func(ref string) error { m.rt.Agent.Model = ref; return nil }
	return m
}

func TestCycleModelForwardWraps(t *testing.T) {
	m := newCycleModel([]string{"a", "b", "c"}, "a")
	m.cycleModel(1)
	if m.rt.Model != "b" {
		t.Errorf("after one forward, model = %q, want b", m.rt.Model)
	}
	m.cycleModel(1)
	m.cycleModel(1)
	if m.rt.Model != "a" {
		t.Errorf("wrap-around failed, model = %q, want a", m.rt.Model)
	}
}

func TestCycleModelBackwardWraps(t *testing.T) {
	m := newCycleModel([]string{"a", "b", "c"}, "a")
	m.cycleModel(-1)
	if m.rt.Model != "c" {
		t.Errorf("backward from first should wrap to last, model = %q, want c", m.rt.Model)
	}
}

func TestCycleModelUnknownCurrentStartsAtZero(t *testing.T) {
	m := newCycleModel([]string{"x", "y"}, "nope")
	m.cycleModel(1)
	if m.rt.Model != "x" {
		t.Errorf("model = %q, want x", m.rt.Model)
	}
}

func TestCycleReasoning(t *testing.T) {
	applied := ""
	m := &model{rt: Runtime{
		Agent:        &engine.Agent{},
		SetReasoning: func(l string) error { applied = l; return nil },
	}, themes: LoadThemes(), themeName: "dark", reasoning: "off"}
	m.st = newStyles(builtinThemes["dark"])
	m.cycleReasoning()
	if m.reasoning != "minimal" || applied != "minimal" {
		t.Errorf("reasoning = %q applied = %q, want minimal", m.reasoning, applied)
	}
}

func TestSetReasoningUnknownErrors(t *testing.T) {
	m := &model{rt: Runtime{
		Agent:        &engine.Agent{},
		SetReasoning: func(l string) error { return errors.New("bad level") },
	}, themes: LoadThemes(), themeName: "dark"}
	m.st = newStyles(builtinThemes["dark"])
	m.setReasoning("bogus")
	if m.dialog == nil || m.dialog.kind != dlgError {
		t.Fatalf("expected an error dialog, got %+v", m.dialog)
	}
}
