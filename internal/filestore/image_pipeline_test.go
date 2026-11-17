package filestore

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func pipelinePNG(t *testing.T) []byte {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	source.SetNRGBA(1, 1, color.NRGBA{R: 200, G: 30, A: 128})
	var out bytes.Buffer
	if err := png.Encode(&out, source); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestImageDecodeOwnsFormatAndPixelBudget(t *testing.T) {
	data := pipelinePNG(t)
	for _, tc := range []struct {
		contentType string
		pixels      int64
		valid       bool
	}{
		{"image/png", 6, true},
		{"image/png", 5, false},
		{"image/png", 0, false},
		{"image/png", -1, false},
		{"image/jpeg", 100, false},
		{"image/webp", 100, false},
		{"application/octet-stream", 100, false},
	} {
		decoded, _, err := decodeImage(data, tc.contentType, tc.pixels)
		if tc.valid {
			if err != nil || decoded == nil || decoded.Bounds() != image.Rect(0, 0, 3, 2) {
				t.Fatalf("valid decode=%v, %v", decoded, err)
			}
		} else if !errors.Is(err, ErrInvalid) || decoded != nil {
			t.Errorf("type=%s budget=%d: decoded=%v err=%v", tc.contentType, tc.pixels, decoded, err)
		}
	}
}

func TestThumbnailByteAndReaderPathsHaveIdenticalBoundsAndOutput(t *testing.T) {
	data := pipelinePNG(t)
	original := bytes.Clone(data)
	service := &Service{options: Options{MaximumBytes: 1 << 20, MaximumImagePixels: 100}}
	for _, size := range []string{"2x2", "2x2t", "2x2b", "2x2f", "0x2", "2x0"} {
		direct, err := service.RenderThumbnailData(data, "image/png", size)
		if err != nil {
			t.Fatal(err)
		}
		streamed, err := service.RenderThumbnailReader(bytes.NewReader(data), "image/png", size)
		if err != nil || !bytes.Equal(direct, streamed) {
			t.Fatalf("%s byte/reader mismatch: %v", size, err)
		}
	}
	if !bytes.Equal(original, data) {
		t.Fatal("borrowed input was modified")
	}
	service.options.MaximumBytes = int64(len(data) - 1)
	if _, err := service.RenderThumbnailData(data, "image/png", "2x2"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("encoded byte limit bypassed: %v", err)
	}
	var absent *Service
	if _, err := absent.RenderThumbnailData(data, "image/png", "2x2"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil service: %v", err)
	}
}
