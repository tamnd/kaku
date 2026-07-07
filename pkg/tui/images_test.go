package tui

import (
	"bytes"
	"image"
	imgcolor "image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/kaku/pkg/engine"
)

func writeTestPNG(t *testing.T, dir, name string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, imgcolor.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAttachImageMentions(t *testing.T) {
	dir := t.TempDir()
	writeTestPNG(t, dir, "shot.png")
	m := &model{rt: Runtime{Agent: &engine.Agent{}, Dir: dir}}

	cleaned := m.attachImageMentions("look at @shot.png and @notes.md")
	if strings.Contains(cleaned, "@shot.png") {
		t.Errorf("image token should be stripped, got %q", cleaned)
	}
	if !strings.Contains(cleaned, "@notes.md") {
		t.Errorf("non-image mention should survive, got %q", cleaned)
	}
	if len(m.pendingImages) != 1 {
		t.Fatalf("want one attached image, got %d", len(m.pendingImages))
	}
	if m.pendingImages[0].Type != "image" || m.pendingImages[0].MediaType != "image/png" {
		t.Fatalf("attached block = %+v", m.pendingImages[0])
	}
	if len(m.pendingLabels) != 1 || m.pendingLabels[0] != "shot.png" {
		t.Fatalf("labels = %v", m.pendingLabels)
	}
}

func TestAttachImageMentionsMissingFileKeepsToken(t *testing.T) {
	dir := t.TempDir()
	m := &model{rt: Runtime{Agent: &engine.Agent{}, Dir: dir}}
	cleaned := m.attachImageMentions("see @gone.png")
	if !strings.Contains(cleaned, "@gone.png") {
		t.Errorf("a mention that fails to load should stay in place, got %q", cleaned)
	}
	if len(m.pendingImages) != 0 {
		t.Errorf("nothing should attach for a missing file")
	}
}

func TestImageChips(t *testing.T) {
	if got := imageChips(nil); got != "" {
		t.Errorf("no labels should render empty, got %q", got)
	}
	got := imageChips([]string{"a.png", "b.jpg"})
	if got != "[img: a.png] [img: b.jpg]" {
		t.Errorf("chips = %q", got)
	}
}
