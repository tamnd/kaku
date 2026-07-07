package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/tamnd/kaku/pkg/image"
	"github.com/tamnd/kaku/pkg/provider"
)

// imageMention matches an @path token the same way the file expander does, so
// the two agree on what counts as a mention.
var imageMention = regexp.MustCompile(`@([A-Za-z0-9._~/-]+)`)

// attachImageMentions loads every @path that points at an image file, appends
// each as a pending image block, and removes those tokens from raw so the text
// expander does not try to inline binary contents. Tokens that are not images,
// or that fail to load, are left in place.
func (m *model) attachImageMentions(raw string) string {
	return imageMention.ReplaceAllStringFunc(raw, func(tok string) string {
		path := strings.TrimSuffix(tok[1:], ".")
		if !image.IsImagePath(path) {
			return tok
		}
		full := path
		if strings.HasPrefix(full, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				full = filepath.Join(home, full[2:])
			}
		} else if !filepath.IsAbs(full) {
			full = filepath.Join(m.rt.Dir, full)
		}
		mediaType, data, err := image.Load(full)
		if err != nil {
			m.entries = append(m.entries, entry{kind: "error", text: "attach " + path + ": " + err.Error()})
			return tok
		}
		m.pendingImages = append(m.pendingImages, provider.Image(mediaType, data))
		m.pendingLabels = append(m.pendingLabels, filepath.Base(path))
		return ""
	})
}

// pasteImage grabs an image off the system clipboard and attaches it. It
// returns a note for the transcript: an empty string means nothing was on the
// clipboard, so the caller can stay quiet.
func (m *model) pasteImage() string {
	mediaType, data, err := clipboardImage()
	if err != nil {
		return ""
	}
	m.pendingImages = append(m.pendingImages, provider.Image(mediaType, data))
	label := fmt.Sprintf("pasted-%d", len(m.pendingImages))
	m.pendingLabels = append(m.pendingLabels, label)
	return "attached image from clipboard"
}

// clipboardImage reads an image off the clipboard using a platform helper,
// writing it to a temp file that image.Load then encodes. It returns an error
// when no helper is available or the clipboard holds no image.
func clipboardImage() (mediaType, data string, err error) {
	tmp, err := os.CreateTemp("", "kaku-clip-*.png")
	if err != nil {
		return "", "", err
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)

	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// pngpaste writes the clipboard image to a file, exit non-zero if none.
		c = exec.Command("pngpaste", path)
	default:
		// wl-paste (Wayland) or xclip (X11); try wl-paste first.
		if _, err := exec.LookPath("wl-paste"); err == nil {
			c = exec.Command("wl-paste", "--type", "image/png")
		} else {
			c = exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o")
		}
	}
	if _, err := exec.LookPath(c.Path); err != nil && !filepath.IsAbs(c.Path) {
		return "", "", fmt.Errorf("no clipboard image helper")
	}
	if c.Args[0] == "pngpaste" {
		if err := c.Run(); err != nil {
			return "", "", err
		}
	} else {
		out, err := c.Output()
		if err != nil {
			return "", "", err
		}
		if len(out) == 0 {
			return "", "", fmt.Errorf("clipboard holds no image")
		}
		if err := os.WriteFile(path, out, 0o600); err != nil {
			return "", "", err
		}
	}
	return image.Load(path)
}

// imageChips renders attached-image labels as bracketed tags for the transcript
// and the composer hint, e.g. "[img: a.png] [img: b.png]".
func imageChips(labels []string) string {
	if len(labels) == 0 {
		return ""
	}
	tags := make([]string, len(labels))
	for i, l := range labels {
		tags[i] = "[img: " + l + "]"
	}
	return strings.Join(tags, " ")
}
