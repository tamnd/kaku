package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/tamnd/kaku/pkg/compact"
	"github.com/tamnd/kaku/pkg/provider"
)

// wordmark is the header brand. Kept as a constant so 07's logo work has one
// place to grow from.
const wordmark = "kaku"

// contextWarnAt is the context-usage fraction past which the gauge flips to a
// warning, a nudge toward /compact (2087/ux/04).
const contextWarnAt = 0.80

// formatCount renders a token count adaptively: 936, 42K, 1.2M, dropping a
// trailing .0 so round numbers stay clean (2087/ux/04).
func formatCount(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return trimDotZero(float64(n)/1000) + "K"
	default:
		return trimDotZero(float64(n)/1_000_000) + "M"
	}
}

// trimDotZero formats a number with one decimal, then drops a trailing ".0".
func trimDotZero(f float64) string {
	s := fmt.Sprintf("%.1f", f)
	return strings.TrimSuffix(s, ".0")
}

// formatCost renders a spend as two decimals for a glance ($0.42); the raw
// four-decimal form is too noisy for the header and footer (2087/ux/04).
func formatCost(f float64) string {
	return fmt.Sprintf("$%.2f", f)
}

// contextLimit returns the active model's context-window size, or 0 when it is
// unknown, by matching the current model against the picker list (2087/ux/04).
func (m *model) contextLimit() int {
	for _, c := range m.rt.Models {
		if c.Ref == m.rt.Model && c.Context > 0 {
			return c.Context
		}
	}
	return 0
}

// contextGauge renders context-window pressure as a percentage of the model's
// limit, prefixed with ~ because the token count is estimated, and with a
// warning glyph once usage crosses the threshold. Returns "" when the limit is
// unknown (2087/ux/04).
func (m *model) contextGauge() string {
	limit := m.contextLimit()
	if limit <= 0 {
		return ""
	}
	used := compact.EstimateTokens(m.rt.Agent.Messages)
	frac := float64(used) / float64(limit)
	if frac > 1 {
		frac = 1
	}
	pct := fmt.Sprintf("~%d%%", int(frac*100+0.5))
	if frac >= contextWarnAt {
		return m.st.warnTag.Render("! "+pct) + m.st.dim.Render(" context")
	}
	return m.st.dim.Render(pct + " context")
}

// header renders the one-line chrome above the transcript: the wordmark, a
// filler of repeated slashes consuming slack, then right-aligned live stats
// (cwd, context gauge, token count), truncated to fit (2087/ux/04).
func (m *model) header(width int) string {
	if width < 20 {
		return "" // too narrow for a header; the footer still carries stats
	}
	brand := m.st.user.Render(wordmark)

	var stats []string
	if cwd := m.prettyCwd(); cwd != "" {
		stats = append(stats, m.st.dim.Render(cwd))
	}
	if g := m.contextGauge(); g != "" {
		stats = append(stats, g)
	}
	u := m.rt.Agent.Usage
	stats = append(stats, m.st.dim.Render(formatCount(u.InputTokens+u.OutputTokens)+" tokens"))
	right := strings.Join(stats, m.st.dim.Render(" • "))

	// Fill the gap between brand and stats with slashes, guarding against a
	// negative width on a narrow line.
	gap := width - lipgloss.Width(brand) - lipgloss.Width(right) - 2
	fill := ""
	if gap > 0 {
		fill = m.st.dim.Render(" " + strings.Repeat("╱", gap) + " ")
	} else {
		fill = " "
	}
	return brand + fill + right
}

// prettyCwd home-shortens the working dir and keeps only the last few segments
// so a deep path never blows the header (2087/ux/04). Unlike prettyPath it does
// not relativize against the working dir, since the working dir is the subject.
func (m *model) prettyCwd() string {
	p := m.rt.Dir
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		p = "~" + strings.TrimPrefix(p, home)
	}
	segs := strings.Split(p, "/")
	if len(segs) > 3 {
		return ".../" + strings.Join(segs[len(segs)-2:], "/")
	}
	return p
}

// footerStatus builds the persistent status footer from the live session state,
// using the adaptive formatters (2087/ux/04). Split out of View so the format is
// testable and the chrome has one owner.
func (m *model) footerStatus(u provider.Usage) string {
	think := ""
	if m.reasoning != "" && m.reasoning != "off" {
		think = " · think:" + m.reasoning
	}
	tokens := fmt.Sprintf("%s in / %s out", formatCount(u.InputTokens), formatCount(u.OutputTokens))
	status := fmt.Sprintf("%s · %s%s · %s", m.rt.Model, m.rt.Mode, think, tokens)
	if c := m.estimatedCost(u); c != "" {
		status += " · " + c
	}
	if m.state == stateRunning {
		status = m.spin.View() + " working, esc interrupts · " + status
	}
	return status
}
