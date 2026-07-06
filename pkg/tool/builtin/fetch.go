package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tamnd/kaku/pkg/tool"
)

const fetchMaxBody = 2 * 1024 * 1024

var (
	reScript = regexp.MustCompile(`(?is)<script\b.*?</script>`)
	reStyle  = regexp.MustCompile(`(?is)<style\b.*?</style>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]*>`)
	reBlank  = regexp.MustCompile(`\n{3,}`)
	reSpaces = regexp.MustCompile(`[ \t]+`)
)

func fetchTool() tool.Tool {
	return tool.Func{
		ToolName: "fetch",
		Desc:     "Fetch a URL over HTTP GET and return the response body, reading at most 2MB with a 30 second timeout. HTML pages are reduced to readable text with scripts, styles, and tags stripped.",
		Safe:     true,
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "url": {"type": "string", "description": "The URL to fetch, including the scheme."}
  },
  "required": ["url"]
}`),
		Fn: func(ctx context.Context, input json.RawMessage) (string, error) {
			var in struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return "", fmt.Errorf("fetch: bad input: %w", err)
			}
			if in.URL == "" {
				return "", errors.New("fetch: url is required")
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
			if err != nil {
				return "", fmt.Errorf("fetch: %w", err)
			}
			req.Header.Set("User-Agent", "kaku/0.1")

			client := &http.Client{Timeout: 30 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				return "", fmt.Errorf("fetch: %w", err)
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(io.LimitReader(resp.Body, fetchMaxBody))
			if err != nil {
				return "", fmt.Errorf("fetch: %w", err)
			}
			text := string(body)
			if strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
				text = stripHTML(text)
			}
			if resp.StatusCode != http.StatusOK {
				prefix := fmt.Sprintf("status %d", resp.StatusCode)
				final := resp.Request.URL.String()
				if final != in.URL {
					prefix += " (final URL: " + final + ")"
				}
				text = prefix + "\n" + text
			}
			return text, nil
		},
	}
}

// stripHTML turns an HTML document into mostly-readable plain text. It is a
// regexp pass, not a parser, which is enough for the model to read a page.
func stripHTML(s string) string {
	s = reScript.ReplaceAllString(s, " ")
	s = reStyle.ReplaceAllString(s, " ")
	s = reTag.ReplaceAllString(s, "\n")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(reSpaces.ReplaceAllString(l, " "))
	}
	s = strings.Join(lines, "\n")
	s = reBlank.ReplaceAllString(s, "\n\n")
	// Drop leading and trailing blank runs left over from stripped markup.
	return strings.Trim(s, "\n ")
}
