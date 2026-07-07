// Package image loads a local image file into the base64 form the model
// providers expect, downscaling oversized PNG and JPEG images so a phone-sized
// screenshot does not blow the token budget.
package image

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	// Register the GIF decoder so DetectContentType-classified GIFs still load.
	_ "image/gif"
)

// MaxDim is the longest edge, in pixels, kept after downscaling. It matches the
// size above which the Anthropic API resizes anyway, so sending more is waste.
const MaxDim = 1568

// MaxBytes caps the encoded payload so a huge file cannot be attached by
// mistake. It applies to formats we do not re-encode (GIF, WebP).
const MaxBytes = 8 << 20 // 8 MiB

// supported maps a detected MIME type to whether kaku will attach it.
var supported = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// IsImagePath reports whether a path looks like an image by extension. The TUI
// uses it to decide whether an @mention should attach rather than inline.
func IsImagePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// Load reads an image file and returns its MIME type and standard-base64 data,
// ready for provider.Image. PNG and JPEG larger than MaxDim on a side are
// downscaled and re-encoded; other formats pass through, subject to MaxBytes.
func Load(path string) (mediaType, data string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	if len(raw) == 0 {
		return "", "", fmt.Errorf("image %s is empty", path)
	}
	mediaType = http.DetectContentType(raw)
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	if !supported[mediaType] {
		return "", "", fmt.Errorf("unsupported image type %q for %s", mediaType, path)
	}

	if mediaType == "image/png" || mediaType == "image/jpeg" {
		if shrunk, ok := downscale(raw, mediaType); ok {
			raw = shrunk
		}
	}
	if len(raw) > MaxBytes {
		return "", "", fmt.Errorf("image %s is %d bytes, over the %d limit", path, len(raw), MaxBytes)
	}
	return mediaType, base64.StdEncoding.EncodeToString(raw), nil
}

// downscale decodes a PNG or JPEG, and if either side exceeds MaxDim, resizes
// it to fit and re-encodes in the same format. It returns ok == false when the
// image is already small enough or cannot be decoded, so the caller keeps the
// original bytes.
func downscale(raw []byte, mediaType string) ([]byte, bool) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, false
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= MaxDim && h <= MaxDim {
		return nil, false
	}
	nw, nh := fit(w, h, MaxDim)
	dst := resize(img, nw, nh)
	var buf bytes.Buffer
	switch mediaType {
	case "image/png":
		if err := png.Encode(&buf, dst); err != nil {
			return nil, false
		}
	case "image/jpeg":
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
			return nil, false
		}
	default:
		return nil, false
	}
	return buf.Bytes(), true
}

// fit scales w and h down so the longest edge is max, preserving aspect ratio.
func fit(w, h, max int) (int, int) {
	if w >= h {
		return max, maxInt(1, h*max/w)
	}
	return maxInt(1, w*max/h), max
}

// resize does a simple box-average downscale into a w by h RGBA image. It is
// only ever called to shrink, so averaging source pixels per destination cell
// gives a clean result without a third-party resampler.
func resize(src image.Image, w, h int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0 := b.Min.Y + y*sh/h
		y1 := b.Min.Y + (y+1)*sh/h
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < w; x++ {
			x0 := b.Min.X + x*sw/w
			x1 := b.Min.X + (x+1)*sw/w
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var rs, gs, bs, as, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					r, g, bl, a := src.At(sx, sy).RGBA()
					rs += uint64(r)
					gs += uint64(g)
					bs += uint64(bl)
					as += uint64(a)
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = uint8(rs / n >> 8)
			dst.Pix[i+1] = uint8(gs / n >> 8)
			dst.Pix[i+2] = uint8(bs / n >> 8)
			dst.Pix[i+3] = uint8(as / n >> 8)
		}
	}
	return dst
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
