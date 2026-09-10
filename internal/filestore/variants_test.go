package filestore

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestVariantGrammarIsBounded(t *testing.T) {
	for _, value := range []Variant{"100x100", "100x50t", "100x50b", "100x50f", "0x80", "80x0"} {
		if !validVariant(value) {
			t.Errorf("%q rejected", value)
		}
	}
	for _, value := range []Variant{"0x0", "01x20", "50000x20", "20X20", "20x20z", "../20x20"} {
		if validVariant(value) {
			t.Errorf("%q accepted", value)
		}
	}
}

func TestRenderVariantSupportsWebP(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString("UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA==")
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const key = "objects/00000000-0000-4000-8000-000000000003"
	if err := store.Put(context.Background(), key, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	result, err := (&Service{objects: store, options: Options{MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000}}).renderVariant(context.Background(), File{ObjectKey: key, ContentType: "image/webp"}, "16x16")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 16 || decoded.Bounds().Dy() != 16 {
		t.Fatalf("bounds=%v", decoded.Bounds())
	}
}

func TestRenderVariantSupportsGIF(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gifSource := image.NewPaletted(image.Rect(0, 0, 2, 1), color.Palette{color.Black, color.White})
	gifSource.SetColorIndex(1, 0, 1)
	var gifData bytes.Buffer
	if err := gif.Encode(&gifData, gifSource, nil); err != nil {
		t.Fatal(err)
	}
	const key = "objects/00000000-0000-4000-8000-000000000002"
	if err := store.Put(context.Background(), key, bytes.NewReader(gifData.Bytes())); err != nil {
		t.Fatal(err)
	}
	service := &Service{objects: store, options: Options{MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000}}
	result, err := service.renderVariant(context.Background(), File{ObjectKey: key, ContentType: "image/gif"}, "1x1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jpeg.DecodeConfig(bytes.NewReader(result)); err != nil {
		t.Fatalf("variant is not a decodable JPEG: %v", err)
	}
}

func TestRenderVariantDimensionsAndVerticalCrop(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := 0; y < 100; y++ {
		shade := uint8(y * 2)
		for x := 0; x < 200; x++ {
			source.Set(x, y, color.RGBA{R: shade, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	const key = "objects/00000000-0000-4000-8000-000000000001"
	if err := store.Put(context.Background(), key, bytes.NewReader(encoded.Bytes())); err != nil {
		t.Fatal(err)
	}
	service := &Service{objects: store, options: Options{MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000}}
	file := File{ObjectKey: key, ContentType: "image/png"}
	for variant, want := range map[string][2]int{"50x50": {50, 50}, "50x50t": {50, 50}, "50x50b": {50, 50}, "50x50f": {50, 25}, "0x25": {50, 25}, "25x0": {25, 12}} {
		data, err := service.renderVariant(context.Background(), file, variant)
		if err != nil {
			t.Fatalf("%s: %v", variant, err)
		}
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if config.Width != want[0] || config.Height != want[1] {
			t.Fatalf("%s dimensions=%dx%d want %dx%d", variant, config.Width, config.Height, want[0], want[1])
		}
	}
}
