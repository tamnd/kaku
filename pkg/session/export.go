package session

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"

	"github.com/tamnd/kaku/pkg/provider"
)

// Export renders session id in the given format and writes it to out. An empty
// out (or "-") writes to stdout. Formats: md (default), html, json. json is the
// raw messages; md is a readable transcript; html wraps the md render in a
// self-contained page with no external assets.
func (st *Store) Export(id, format, out string) error {
	s, err := st.Open(id)
	if err != nil {
		return err
	}
	defer s.Close()

	var body string
	switch format {
	case "", "md", "markdown":
		body = renderMarkdown(s)
	case "json":
		data, err := json.MarshalIndent(s.Messages(), "", "  ")
		if err != nil {
			return err
		}
		body = string(data) + "\n"
	case "html":
		body = renderHTML(s)
	default:
		return fmt.Errorf("unknown export format %q (want md, html, or json)", format)
	}

	if out == "" || out == "-" {
		_, err := os.Stdout.WriteString(body)
		return err
	}
	return os.WriteFile(out, []byte(body), 0o644)
}

// Share writes a self-contained HTML copy of session id to a file and returns
// its absolute path. With out empty the file lands in ~/.kaku/shares/<id>.html,
// a stable spot you can point a static host at; out overrides the path. The
// page carries no external assets, so it reads the same wherever it is opened.
func (st *Store) Share(id, out string) (string, error) {
	s, err := st.Open(id)
	if err != nil {
		return "", err
	}
	defer s.Close()

	if out == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir := filepath.Join(home, ".kaku", "shares")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		out = filepath.Join(dir, id+".html")
	}
	if err := os.WriteFile(out, []byte(renderHTML(s)), 0o644); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(out)
	if err != nil {
		return out, nil
	}
	return abs, nil
}

// sessionTitle returns a display title, falling back to the id.
func sessionTitle(s *Session) string {
	if s.title != "" {
		return s.title
	}
	return s.id
}

// renderMarkdown writes the transcript as headed sections. Thinking is omitted;
// tool calls and their results become fenced blocks.
func renderMarkdown(s *Session) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", sessionTitle(s))
	for _, m := range s.Messages() {
		switch m.Role {
		case provider.RoleUser:
			b.WriteString("## user\n\n")
		case provider.RoleAssistant:
			b.WriteString("## kaku\n\n")
		default:
			fmt.Fprintf(&b, "## %s\n\n", m.Role)
		}
		for _, blk := range m.Content {
			switch blk.Type {
			case provider.BlockText:
				if t := strings.TrimSpace(blk.Text); t != "" {
					b.WriteString(t)
					b.WriteString("\n\n")
				}
			case provider.BlockToolUse:
				fmt.Fprintf(&b, "```%s\n%s\n```\n\n", blk.Name, strings.TrimSpace(string(blk.Input)))
			case provider.BlockToolResult:
				if t := strings.TrimSpace(blk.Text); t != "" {
					fmt.Fprintf(&b, "```\n%s\n```\n\n", t)
				}
			}
		}
	}
	return b.String()
}

const htmlTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
body { margin: 0 auto; max-width: 48rem; padding: 2rem 1rem;
  font: 14px/1.6 ui-monospace, SFMono-Regular, Menlo, monospace;
  color: #1a1a1a; background: #fafafa; }
pre { white-space: pre-wrap; word-wrap: break-word; margin: 0; }
</style>
</head>
<body>
<pre>%s</pre>
</body>
</html>
`

// renderHTML wraps the markdown transcript in a minimal standalone page.
func renderHTML(s *Session) string {
	return fmt.Sprintf(htmlTemplate, html.EscapeString(sessionTitle(s)), html.EscapeString(renderMarkdown(s)))
}
