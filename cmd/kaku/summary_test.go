package main

import "testing"

func TestResourceSummary(t *testing.T) {
	got := resourceSummary(3, 1, 0, 2)
	want := "3 skills · 1 agent · 0 MCP servers · 2 memory files"
	if got != want {
		t.Errorf("resourceSummary = %q, want %q", got, want)
	}
}

func TestPlural(t *testing.T) {
	cases := map[string]string{
		plural(0, "skill"): "0 skills",
		plural(1, "skill"): "1 skill",
		plural(2, "skill"): "2 skills",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("plural = %q, want %q", got, want)
		}
	}
}
