package filestore

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"golang.org/x/image/webp"
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
	decoded, err := png.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 16 || decoded.Bounds().Dy() != 16 {
		t.Fatalf("bounds=%v", decoded.Bounds())
	}
}

func TestRenderThumbnailDataExtractsAnimatedWebPFirstFrame(t *testing.T) {
	still, err := base64.StdEncoding.DecodeString("UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA==")
	if err != nil {
		t.Fatal(err)
	}
	config, err := webp.DecodeConfig(bytes.NewReader(still))
	if err != nil {
		t.Fatal(err)
	}
	animated := animatedWebPFixture(t, still, 2, 4, config.Width+4, config.Height+6, false, nil)
	if _, err := webp.Decode(bytes.NewReader(animated)); err == nil {
		t.Fatal("x/image/webp unexpectedly decoded ANMF fixture; regression no longer exercises fallback")
	}
	service := &Service{options: Options{MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000}}
	result, err := service.RenderThumbnailData(animated, "image/webp", "0x16")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dy() != 16 || decoded.Bounds().Dx() < 1 {
		t.Fatalf("bounds=%v", decoded.Bounds())
	}
	first, err := decodeAnimatedWebPFirstFrame(animated, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, a := first.At(0, 0).RGBA()
	if r != 0 || g != 0xffff || b != 0 || a != 0xffff {
		t.Fatalf("background=(%d,%d,%d,%d), want opaque green", r, g, b, a)
	}
	if _, _, _, frameAlpha := first.At(2, 4).RGBA(); frameAlpha != 0xffff {
		t.Fatalf("frame alpha=%d", frameAlpha)
	}
	broken := append([]byte(nil), animated...)
	// ANMF starts after RIFF(12), VP8X(18), and ANIM(14); make its X exceed the canvas.
	broken[52], broken[53], broken[54] = 0xff, 0xff, 0xff
	if _, err := decodeAnimatedWebPFirstFrame(broken, 1_000_000); !errors.Is(err, ErrInvalid) {
		t.Fatalf("out-of-canvas frame error=%v", err)
	}
	mismatched := append([]byte(nil), animated...)
	mismatched[58]++ // Declared ANMF width no longer matches the embedded VP8L header.
	if _, err := decodeAnimatedWebPFirstFrame(mismatched, 1_000_000); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched frame dimensions error=%v", err)
	}
	if _, err := decodeAnimatedWebPFirstFrame(animated, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("canvas pixel bound error=%v", err)
	}
}

func TestAnimatedWebPVP8LAlphaBlendsWithBackground(t *testing.T) {
	transparent, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		noBlend bool
		alpha   uint32
		green   uint32
	}{
		{"blend", false, 0xffff, 0xffff},
		{"replace", true, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			animated := animatedWebPFixture(t, transparent, 2, 0, 4, 1, test.noBlend, nil)
			decoded, err := decodeAnimatedWebPFirstFrame(animated, 1_000_000)
			if err != nil {
				t.Fatal(err)
			}
			_, green, _, alpha := decoded.At(2, 0).RGBA()
			if alpha != test.alpha || green != test.green {
				t.Fatalf("pixel green=%d alpha=%d, want green=%d alpha=%d", green, alpha, test.green, test.alpha)
			}
		})
	}
}

func TestAnimatedWebPALPHSubchunkPreservesAlpha(t *testing.T) {
	lossy, err := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	// ALPH flags=0 means uncompressed scan-order alpha; the one pixel is half opaque.
	animated := animatedWebPFixture(t, lossy, 0, 0, 1, 1, true, []byte{0, 128})
	decoded, err := decodeAnimatedWebPFirstFrame(animated, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, alpha := decoded.At(0, 0).RGBA()
	if alpha != 128*257 {
		t.Fatalf("alpha=%d want %d", alpha, 128*257)
	}
}

func animatedWebPFixture(t *testing.T, still []byte, x, y, canvasWidth, canvasHeight int, noBlend bool, alpha []byte) []byte {
	t.Helper()
	chunks, err := parseWebPChunks(still)
	if err != nil || len(chunks) != 1 || chunks[0].tag != "VP8L" && chunks[0].tag != "VP8 " {
		t.Fatalf("still fixture chunks=%v err=%v", chunks, err)
	}
	frameConfig, err := webp.DecodeConfig(bytes.NewReader(still))
	if err != nil {
		t.Fatal(err)
	}
	vp8x := make([]byte, 10)
	vp8x[0] = 0x02
	putUint24(vp8x[4:7], uint32(canvasWidth-1))
	putUint24(vp8x[7:10], uint32(canvasHeight-1))
	anim := []byte{0, 255, 0, 255, 0, 0} // BGRA green, infinite loop.
	frame := make([]byte, 16)
	putUint24(frame[0:3], uint32(x/2))
	putUint24(frame[3:6], uint32(y/2))
	putUint24(frame[6:9], uint32(frameConfig.Width-1))
	putUint24(frame[9:12], uint32(frameConfig.Height-1))
	if noBlend {
		frame[15] = 0x02
	}
	if alpha != nil {
		frame = appendWebPChunk(frame, "ALPH", alpha)
	}
	frame = appendWebPChunk(frame, chunks[0].tag, chunks[0].data)
	payload := append([]byte("WEBP"), nil...)
	payload = appendWebPChunk(payload, "VP8X", vp8x)
	payload = appendWebPChunk(payload, "ANIM", anim)
	payload = appendWebPChunk(payload, "ANMF", frame)
	result := make([]byte, 8, len(payload)+8)
	copy(result, "RIFF")
	binary.LittleEndian.PutUint32(result[4:8], uint32(len(payload)))
	return append(result, payload...)
}

func TestWebPThumbnailEncodingPreservesAlpha(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 0})
	source.SetNRGBA(1, 0, color.NRGBA{B: 255, A: 255})
	var result bytes.Buffer
	if err := encodeThumbnail(&result, source, "webp"); err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(&result)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, transparentAlpha := decoded.At(0, 0).RGBA()
	_, _, _, opaqueAlpha := decoded.At(1, 0).RGBA()
	if transparentAlpha != 0 || opaqueAlpha != 0xffff {
		t.Fatalf("alpha=(%d,%d), want (0,65535)", transparentAlpha, opaqueAlpha)
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
	if _, err := png.DecodeConfig(bytes.NewReader(result)); err != nil {
		t.Fatalf("variant is not a decodable PNG: %v", err)
	}
}

func TestRenderThumbnailDataPreservesGIFTransparencyAsPNG(t *testing.T) {
	source := image.NewPaletted(image.Rect(0, 0, 2, 1), color.Palette{color.NRGBA{0, 0, 0, 0}, color.NRGBA{255, 0, 0, 255}})
	source.SetColorIndex(1, 0, 1)
	var encoded bytes.Buffer
	if err := gif.Encode(&encoded, source, &gif.Options{NumColors: 2}); err != nil {
		t.Fatal(err)
	}
	service := &Service{options: Options{MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000}}
	result, err := service.RenderThumbnailData(encoded.Bytes(), "image/gif", "2x1f")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatalf("output is not PNG: %v", err)
	}
	_, _, _, transparentAlpha := decoded.At(0, 0).RGBA()
	_, _, _, opaqueAlpha := decoded.At(1, 0).RGBA()
	if transparentAlpha != 0 || opaqueAlpha != 0xffff {
		t.Fatalf("alpha=(%d,%d), want (0,65535)", transparentAlpha, opaqueAlpha)
	}
}

func TestBuildThumbnailPreservesGIFTransparencyAsPNG(t *testing.T) {
	source := image.NewPaletted(image.Rect(0, 0, 2, 1), color.Palette{color.NRGBA{0, 0, 0, 0}, color.NRGBA{255, 0, 0, 255}})
	source.SetColorIndex(1, 0, 1)
	var encoded bytes.Buffer
	if err := gif.Encode(&encoded, source, &gif.Options{NumColors: 2}); err != nil {
		t.Fatal(err)
	}
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const key = "objects/00000000-0000-4000-8000-000000000004"
	if err := store.Put(context.Background(), key, bytes.NewReader(encoded.Bytes())); err != nil {
		t.Fatal(err)
	}
	service := &Service{objects: store, options: Options{MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000, ThumbnailDimension: 2}}
	result, err := service.buildThumbnail(context.Background(), File{ObjectKey: key, ContentType: "image/gif"})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, alpha := decoded.At(0, 0).RGBA()
	if alpha != 0 {
		t.Fatalf("transparent pixel alpha=%d", alpha)
	}
}

func TestRenderThumbnailDataNormalizesEXIFOrientation(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			if x < 20 {
				source.Set(x, y, color.RGBA{R: 255, A: 255})
			} else {
				source.Set(x, y, color.RGBA{B: 255, A: 255})
			}
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	data := appendJPEGEXIFOrientation(encoded.Bytes(), 6)
	service := &Service{options: Options{MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000}}
	result, err := service.RenderThumbnailData(data, "image/jpeg", "0x40")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 20 || decoded.Bounds().Dy() != 40 {
		t.Fatalf("bounds=%v, want 20x40", decoded.Bounds())
	}
	topR, _, topB, _ := decoded.At(10, 5).RGBA()
	bottomR, _, bottomB, _ := decoded.At(10, 35).RGBA()
	if topR <= topB || bottomB <= bottomR {
		t.Fatalf("orientation pixels top=(%d,%d) bottom=(%d,%d)", topR, topB, bottomR, bottomB)
	}
}

func TestOrientImageAllEXIFOrientations(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	labels := []uint8{1, 2, 3, 4, 5, 6}
	for i, label := range labels {
		source.SetNRGBA(i%3, i/3, color.NRGBA{R: label, A: 255})
	}
	wants := map[int]struct {
		width, height int
		pixels        []uint8
	}{
		1: {3, 2, []uint8{1, 2, 3, 4, 5, 6}},
		2: {3, 2, []uint8{3, 2, 1, 6, 5, 4}},
		3: {3, 2, []uint8{6, 5, 4, 3, 2, 1}},
		4: {3, 2, []uint8{4, 5, 6, 1, 2, 3}},
		5: {2, 3, []uint8{1, 4, 2, 5, 3, 6}},
		6: {2, 3, []uint8{4, 1, 5, 2, 6, 3}},
		7: {2, 3, []uint8{6, 3, 5, 2, 4, 1}},
		8: {2, 3, []uint8{3, 6, 2, 5, 1, 4}},
	}
	for orientation := 1; orientation <= 8; orientation++ {
		got := orientImage(source, orientation)
		want := wants[orientation]
		if got.Bounds().Dx() != want.width || got.Bounds().Dy() != want.height {
			t.Fatalf("orientation %d bounds=%v", orientation, got.Bounds())
		}
		for i, label := range want.pixels {
			r, _, _, _ := got.At(i%want.width, i/want.width).RGBA()
			if uint8(r>>8) != label {
				t.Fatalf("orientation %d pixel %d=%d want %d", orientation, i, r>>8, label)
			}
		}
	}
}

func TestEXIFParserRejectsMalformedAndOversizedIFDs(t *testing.T) {
	oversized := make([]byte, 10)
	copy(oversized, "II")
	binary.LittleEndian.PutUint16(oversized[2:4], 42)
	binary.LittleEndian.PutUint32(oversized[4:8], 8)
	binary.LittleEndian.PutUint16(oversized[8:10], maximumEXIFEntries+1)
	if got := tiffOrientation(oversized); got != 1 {
		t.Fatalf("oversized IFD orientation=%d", got)
	}
	for _, data := range [][]byte{{0xff, 0xd8, 0xff, 0xe1, 0xff, 0xff}, {0xff, 0xd8, 0xff, 0xe1, 0, 1}} {
		if got := jpegEXIFOrientation(data); got != 1 {
			t.Fatalf("malformed orientation=%d", got)
		}
	}
}

func TestRenderThumbnailReaderEnforcesByteAndPixelBounds(t *testing.T) {
	service := &Service{options: Options{MaximumBytes: 8, MaximumImagePixels: 1_000_000}}
	if _, err := service.RenderThumbnailReader(bytes.NewReader(make([]byte, 9)), "image/jpeg", "1x1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("byte bound error=%v", err)
	}
	source := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	service.options.MaximumBytes = 1 << 20
	service.options.MaximumImagePixels = 1
	if _, err := service.RenderThumbnailData(encoded.Bytes(), "image/png", "1x1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("pixel bound error=%v", err)
	}
}

func appendJPEGEXIFOrientation(jpegData []byte, orientation uint16) []byte {
	tiff := make([]byte, 26)
	copy(tiff, "II")
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)
	binary.LittleEndian.PutUint16(tiff[8:10], 1)
	binary.LittleEndian.PutUint16(tiff[10:12], 0x0112)
	binary.LittleEndian.PutUint16(tiff[12:14], 3)
	binary.LittleEndian.PutUint32(tiff[14:18], 1)
	binary.LittleEndian.PutUint16(tiff[18:20], orientation)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	segment := []byte{0xff, 0xe1, 0, 0}
	binary.BigEndian.PutUint16(segment[2:4], uint16(len(payload)+2))
	result := append([]byte{}, jpegData[:2]...)
	result = append(result, segment...)
	result = append(result, payload...)
	return append(result, jpegData[2:]...)
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
