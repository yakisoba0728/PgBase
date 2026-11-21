package filestore

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math/rand"
	"testing"
)

// referenceThumbnailResize is buildThumbnail's original per-pixel loop, kept
// verbatim as the oracle the Pix fast paths must reproduce byte for byte.
func referenceThumbnailResize(source image.Image, newWidth, newHeight int) *image.NRGBA {
	width, height := source.Bounds().Dx(), source.Bounds().Dy()
	thumbnail := image.NewNRGBA(image.Rect(0, 0, newWidth, newHeight))
	for y := range newHeight {
		for x := range newWidth {
			thumbnail.Set(x, y, source.At(source.Bounds().Min.X+x*width/newWidth, source.Bounds().Min.Y+y*height/newHeight))
		}
	}
	return thumbnail
}

// referenceVariantResize is renderThumbnailData's original per-pixel loop,
// kept verbatim as the oracle for variantSampleGrid plus sampleNearest.
func referenceVariantResize(source image.Image, outW, outH int, mode string) *image.NRGBA {
	sw, sh := source.Bounds().Dx(), source.Bounds().Dy()
	out := image.NewNRGBA(image.Rect(0, 0, outW, outH))
	for y := 0; y < outH; y++ {
		for x := 0; x < outW; x++ {
			sx, sy := x*sw/outW, y*sh/outH
			if mode != "f" && sw*outH != sh*outW {
				if sw*outH > sh*outW {
					cropW := sh * outW / outH
					offset := (sw - cropW) / 2
					sx = offset + x*cropW/outW
				} else {
					cropH := sw * outH / outW
					offset := (sh - cropH) / 2
					if mode == "t" {
						offset = 0
					} else if mode == "b" {
						offset = sh - cropH
					}
					sy = offset + y*cropH/outH
				}
			}
			out.Set(x, y, source.At(source.Bounds().Min.X+sx, source.Bounds().Min.Y+sy))
		}
	}
	return out
}

func randomBytes(random *rand.Rand, buffer []byte) {
	for i := range buffer {
		buffer[i] = byte(random.Intn(256))
	}
}

// resampleSources returns one random image per decoder type the fast paths
// cover, plus types that must keep the generic path. Every image is cut with
// SubImage at an odd origin so non-zero bounds and chroma phase are exercised.
func resampleSources(random *rand.Rand, width, height int) map[string]image.Image {
	full := image.Rect(-3, 5, width+4, height+7)
	view := image.Rect(-2, 6, -2+width, 6+height)
	sources := map[string]image.Image{}

	nrgba := image.NewNRGBA(full)
	randomBytes(random, nrgba.Pix)
	sources["nrgba"] = nrgba.SubImage(view)

	// Arbitrary bytes include colour channels above alpha, which no valid
	// premultiplied image holds; the conversion must still match exactly.
	rgba := image.NewRGBA(full)
	randomBytes(random, rgba.Pix)
	for i := 3; i < len(rgba.Pix); i += 16 {
		rgba.Pix[i] = 0
	}
	for i := 7; i < len(rgba.Pix); i += 16 {
		rgba.Pix[i] = 0xff
	}
	sources["rgba"] = rgba.SubImage(view)

	gray := image.NewGray(full)
	randomBytes(random, gray.Pix)
	sources["gray"] = gray.SubImage(view)

	for _, ratio := range []image.YCbCrSubsampleRatio{
		image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio422, image.YCbCrSubsampleRatio420,
		image.YCbCrSubsampleRatio440, image.YCbCrSubsampleRatio411, image.YCbCrSubsampleRatio410,
	} {
		ycbcr := image.NewYCbCr(full, ratio)
		randomBytes(random, ycbcr.Y)
		randomBytes(random, ycbcr.Cb)
		randomBytes(random, ycbcr.Cr)
		sources["ycbcr-"+ratio.String()] = ycbcr.SubImage(view)

		nycbcra := image.NewNYCbCrA(full, ratio)
		randomBytes(random, nycbcra.Y)
		randomBytes(random, nycbcra.Cb)
		randomBytes(random, nycbcra.Cr)
		randomBytes(random, nycbcra.A)
		for i := 0; i < len(nycbcra.A); i += 7 {
			nycbcra.A[i] = 0
		}
		for i := 3; i < len(nycbcra.A); i += 7 {
			nycbcra.A[i] = 0xff
		}
		sources["nycbcra-"+ratio.String()] = nycbcra.SubImage(view)
	}

	palette := make(color.Palette, 1+random.Intn(256))
	for i := range palette {
		switch i % 3 {
		case 0:
			palette[i] = color.NRGBA{uint8(random.Intn(256)), uint8(random.Intn(256)), uint8(random.Intn(256)), uint8(random.Intn(256))}
		case 1:
			a := uint8(random.Intn(256))
			palette[i] = color.RGBA{uint8(random.Intn(int(a) + 1)), uint8(random.Intn(int(a) + 1)), uint8(random.Intn(int(a) + 1)), a}
		default:
			palette[i] = color.Gray{uint8(random.Intn(256))}
		}
	}
	paletted := image.NewPaletted(full, palette)
	for i := range paletted.Pix {
		paletted.Pix[i] = uint8(random.Intn(len(palette)))
	}
	sources["paletted"] = paletted.SubImage(view)

	// Generic-path types.
	cmyk := image.NewCMYK(full)
	randomBytes(random, cmyk.Pix)
	sources["cmyk"] = cmyk.SubImage(view)
	nrgba64 := image.NewNRGBA64(full)
	randomBytes(random, nrgba64.Pix)
	sources["nrgba64"] = nrgba64.SubImage(view)
	gray16 := image.NewGray16(full)
	randomBytes(random, gray16.Pix)
	sources["gray16"] = gray16.SubImage(view)
	return sources
}

func assertSameNRGBA(t *testing.T, label string, got, want *image.NRGBA) {
	t.Helper()
	if got.Rect != want.Rect || got.Stride != want.Stride || !bytes.Equal(got.Pix, want.Pix) {
		for i := range want.Pix {
			if i >= len(got.Pix) || got.Pix[i] != want.Pix[i] {
				t.Fatalf("%s: rect=%v/%v stride=%d/%d first difference at byte %d", label, got.Rect, want.Rect, got.Stride, want.Stride, i)
			}
		}
		t.Fatalf("%s: rect=%v/%v stride=%d/%d len=%d/%d", label, got.Rect, want.Rect, got.Stride, want.Stride, len(got.Pix), len(want.Pix))
	}
}

func TestSampleNearestMatchesInterfaceResizeForEveryType(t *testing.T) {
	random := rand.New(rand.NewSource(20261122))
	sizes := [][2]int{{1, 1}, {1, 9}, {9, 1}, {2, 3}, {7, 5}, {17, 31}, {64, 48}, {33, 33}}
	outputs := [][2]int{{1, 1}, {1, 7}, {7, 1}, {3, 2}, {5, 5}, {16, 9}, {40, 61}, {128, 96}}
	modes := []string{"c", "t", "b", "f"}
	for _, size := range sizes {
		sources := resampleSources(random, size[0], size[1])
		for name, source := range sources {
			if source.Bounds().Dx() != size[0] || source.Bounds().Dy() != size[1] {
				t.Fatalf("%s: bounds %v, want %dx%d", name, source.Bounds(), size[0], size[1])
			}
			for _, output := range outputs {
				label := fmt.Sprintf("%s %dx%d->%dx%d", name, size[0], size[1], output[0], output[1])
				xs, ys := thumbnailSampleGrid(size[0], size[1], output[0], output[1])
				assertSameNRGBA(t, "thumbnail "+label, sampleNearest(source, xs, ys), referenceThumbnailResize(source, output[0], output[1]))
				for _, mode := range modes {
					xs, ys := variantSampleGrid(size[0], size[1], output[0], output[1], mode)
					assertSameNRGBA(t, "variant "+mode+" "+label, sampleNearest(source, xs, ys), referenceVariantResize(source, output[0], output[1], mode))
				}
			}
		}
	}
}

func TestSampleNearestKeepsGenericPathForPalettesWithoutConversion(t *testing.T) {
	source := image.NewPaletted(image.Rect(0, 0, 3, 2), color.Palette{color.Gray{10}, nil})
	xs, ys := thumbnailSampleGrid(3, 2, 3, 2)
	assertSameNRGBA(t, "nil-entry palette", sampleNearest(source, xs, ys), referenceThumbnailResize(source, 3, 2))
}

func TestRenderThumbnailDataMatchesInterfaceResizeEndToEnd(t *testing.T) {
	service := &Service{options: Options{MaximumBytes: 64 << 20, MaximumImagePixels: 64 << 20}}
	random := rand.New(rand.NewSource(7))
	sources := resampleSources(random, 37, 23)
	for _, name := range []string{"nrgba", "rgba", "gray", "paletted", "ycbcr-YCbCrSubsampleRatio420"} {
		source := sources[name]
		var encoded bytes.Buffer
		if err := encodeThumbnail(&encoded, source, "png"); err != nil {
			t.Fatal(err)
		}
		decoded, _, err := decodeImage(encoded.Bytes(), "image/png", service.options.MaximumImagePixels)
		if err != nil {
			t.Fatal(err)
		}
		for _, variant := range []string{"16x16", "50x10", "10x50t", "10x50b", "64x64f", "0x40"} {
			got, err := service.RenderThumbnailData(encoded.Bytes(), "image/png", variant)
			if err != nil {
				t.Fatalf("%s %s: %v", name, variant, err)
			}
			m := variantPattern.FindStringSubmatch(variant)
			outW, outH := expectedVariantSize(decoded.Bounds().Dx(), decoded.Bounds().Dy(), m[1], m[2], m[3])
			var want bytes.Buffer
			if err := encodeThumbnail(&want, referenceVariantResize(decoded, outW, outH, m[3]), "png"); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want.Bytes()) {
				t.Fatalf("%s %s: encoded output differs from the interface-based resize", name, variant)
			}
		}
	}
}

// expectedVariantSize repeats renderThumbnailData's output-size arithmetic.
func expectedVariantSize(sw, sh int, width, height, mode string) (int, int) {
	var w, h int
	_, _ = fmt.Sscan(width, &w)
	_, _ = fmt.Sscan(height, &h)
	if w == 0 {
		w = max(1, sw*h/sh)
	}
	if h == 0 {
		h = max(1, sh*w/sw)
	}
	if mode == "f" {
		if sw*h > sh*w {
			return w, max(1, sh*w/sw)
		}
		return max(1, sw*h/sh), h
	}
	return w, h
}

func BenchmarkVariantResize4096(b *testing.B) {
	random := rand.New(rand.NewSource(1))
	const sourceSize, outputSize = 4096, 4096
	nrgba := image.NewNRGBA(image.Rect(0, 0, sourceSize, sourceSize))
	randomBytes(random, nrgba.Pix)
	ycbcr := image.NewYCbCr(image.Rect(0, 0, sourceSize, sourceSize), image.YCbCrSubsampleRatio420)
	randomBytes(random, ycbcr.Y)
	randomBytes(random, ycbcr.Cb)
	randomBytes(random, ycbcr.Cr)
	for _, source := range []struct {
		name  string
		image image.Image
	}{{"nrgba", nrgba}, {"ycbcr420", ycbcr}} {
		b.Run(source.name+"/interface", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				referenceVariantResize(source.image, outputSize, outputSize, "c")
			}
		})
		b.Run(source.name+"/pix", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				xs, ys := variantSampleGrid(sourceSize, sourceSize, outputSize, outputSize, "c")
				sampleNearest(source.image, xs, ys)
			}
		})
	}
}
