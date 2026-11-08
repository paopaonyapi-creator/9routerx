// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

import (
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
)

func TestImageSizeGIF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hero.gif")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := image.NewPaletted(image.Rect(0, 0, 73, 19), color.Palette{color.Black, color.White})
	if err := gif.Encode(f, fixture, nil); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	w, h, ok := imageSize(path)
	if !ok || w != 73 || h != 19 {
		t.Fatalf("GIF dimensions = %d,%d,%v, want 73,19,true", w, h, ok)
	}
}
