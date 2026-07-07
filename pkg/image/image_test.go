package image

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// writePNG makes a w by h PNG on disk and returns its path.
func writePNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "img.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func decodeDims(t *testing.T, b64 string) (int, int) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Width, cfg.Height
}

func TestLoadSmallPassesThrough(t *testing.T) {
	path := writePNG(t, 64, 48)
	mt, data, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if mt != "image/png" {
		t.Fatalf("media type = %q, want image/png", mt)
	}
	if w, h := decodeDims(t, data); w != 64 || h != 48 {
		t.Fatalf("small image should keep its size, got %dx%d", w, h)
	}
}

func TestLoadDownscalesLarge(t *testing.T) {
	path := writePNG(t, MaxDim*2, MaxDim) // 3136 x 1568, wide
	_, data, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	w, h := decodeDims(t, data)
	if w > MaxDim || h > MaxDim {
		t.Fatalf("downscaled image %dx%d should fit within %d", w, h, MaxDim)
	}
	if w != MaxDim {
		t.Errorf("longest edge should scale to %d, got width %d", MaxDim, w)
	}
	// A 2:1 image should stay roughly 2:1 after scaling.
	if h < MaxDim/2-2 || h > MaxDim/2+2 {
		t.Errorf("aspect ratio not preserved: height %d", h)
	}
}

func TestLoadRejectsNonImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.png")
	if err := os.WriteFile(path, []byte("this is plain text, not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path); err == nil {
		t.Error("a text file should not load as an image")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, _, err := Load(filepath.Join(t.TempDir(), "nope.png")); err == nil {
		t.Error("a missing file should error")
	}
}

func TestIsImagePath(t *testing.T) {
	for _, p := range []string{"a.png", "b.JPG", "c.jpeg", "d.gif", "e.webp"} {
		if !IsImagePath(p) {
			t.Errorf("%q should be an image path", p)
		}
	}
	for _, p := range []string{"main.go", "README.md", "noext"} {
		if IsImagePath(p) {
			t.Errorf("%q should not be an image path", p)
		}
	}
}
