package filestore

import (
	"bytes"
	"context"
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
