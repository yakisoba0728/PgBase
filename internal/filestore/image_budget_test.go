package filestore

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestColorModelBytesPerPixel(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model color.Model
		want  int64
	}{
		{"rgba", color.RGBAModel, 4},
		{"nrgba", color.NRGBAModel, 4},
		{"rgba64", color.RGBA64Model, 8},
		{"nrgba64", color.NRGBA64Model, 8},
		{"gray", color.GrayModel, 1},
		{"gray16", color.Gray16Model, 2},
		{"alpha", color.AlphaModel, 1},
		{"alpha16", color.Alpha16Model, 2},
		{"ycbcr", color.YCbCrModel, 3},
		{"nycbcra", color.NYCbCrAModel, 4},
		{"cmyk", color.CMYKModel, 4},
		{"paletted", color.Palette(palette.Plan9), 1},
		{"unknown", color.ModelFunc(func(c color.Color) color.Color { return c }), 8},
		{"nil", nil, 8},
	} {
		if got := colorModelBytesPerPixel(tc.model); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestEstimateImageMemoryCoversDecoderAllocations runs the real decoders and
// checks the reservation against two measurements: every byte allocated up to
// the end of decoding (TotalAlloc, garbage included, so an upper bound on what
// the decoder can hold), and the heap live at the render's high point, where
// the source, its resampled copy and the encoded output coexist. Per-pixel
// colour boxing on the generic resample path is collectable garbage and is
// deliberately not counted.
func TestEstimateImageMemoryCoversDecoderAllocations(t *testing.T) {
	const width, height = 1600, 1200
	rect := image.Rect(0, 0, width, height)
	fill := func(m interface{ Set(int, int, color.Color) }) {
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				m.Set(x, y, color.NRGBA64{R: uint16(x * 40), G: uint16(y * 50), B: uint16(x * y), A: 0xffff - uint16(y)})
			}
		}
	}
	encodePNG := func(m image.Image) []byte {
		var b bytes.Buffer
		if err := png.Encode(&b, m); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	encodeJPEG := func(m image.Image) []byte {
		var b bytes.Buffer
		if err := jpeg.Encode(&b, m, &jpeg.Options{Quality: 90}); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	nrgba, nrgba64, rgba, gray, gray16 := image.NewNRGBA(rect), image.NewNRGBA64(rect), image.NewRGBA(rect), image.NewGray(rect), image.NewGray16(rect)
	paletted := image.NewPaletted(rect, palette.Plan9)
	for _, m := range []interface{ Set(int, int, color.Color) }{nrgba, nrgba64, rgba, gray, gray16, paletted} {
		fill(m)
	}
	var gifData bytes.Buffer
	if err := gif.Encode(&gifData, paletted, nil); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, contentType string
		data              []byte
	}{
		{"png-nrgba", "image/png", encodePNG(nrgba)},
		{"png-nrgba64", "image/png", encodePNG(nrgba64)},
		{"png-rgba", "image/png", encodePNG(rgba)},
		{"png-gray", "image/png", encodePNG(gray)},
		{"png-gray16", "image/png", encodePNG(gray16)},
		{"png-paletted", "image/png", encodePNG(paletted)},
		{"jpeg-ycbcr", "image/jpeg", encodeJPEG(rgba)},
		{"jpeg-gray", "image/jpeg", encodeJPEG(gray)},
		{"gif", "image/gif", gifData.Bytes()},
	}
	for _, tc := range cases {
		for _, size := range [][2]int{{320, 240}, {2048, 2048}, {75, 100}} {
			outW, outH := size[0], size[1]
			service := &Service{options: Options{MaximumBytes: 64 << 20, MaximumImagePixels: 40_000_000, ThumbnailDimension: 320}, imageBudget: newImageMemoryBudget(1 << 40)}
			runtime.GC()
			var before, decoded, rendered runtime.MemStats
			runtime.ReadMemStats(&before)
			_, err := service.renderDecodedImage(context.Background(), tc.data, tc.contentType, fixedOutput(int64(outW*outH)), false, func(source image.Image, format string) ([]byte, error) {
				runtime.ReadMemStats(&decoded)
				xs, ys := variantSampleGrid(source.Bounds().Dx(), source.Bounds().Dy(), outW, outH, "")
				out := sampleNearest(source, xs, ys)
				var buffer bytes.Buffer
				err := encodeThumbnail(&buffer, out, format)
				runtime.GC()
				runtime.ReadMemStats(&rendered)
				runtime.KeepAlive(source)
				runtime.KeepAlive(out)
				return buffer.Bytes(), err
			})
			if err != nil {
				t.Fatalf("%s %dx%d: %v", tc.name, outW, outH, err)
			}
			_, peak, _ := service.imageBudget.snapshot()
			if allocated := int64(decoded.TotalAlloc - before.TotalAlloc); allocated > peak {
				t.Errorf("%s %dx%d: decoding allocated %d bytes, estimate reserved only %d", tc.name, outW, outH, allocated, peak)
			}
			if live := int64(rendered.HeapAlloc) - int64(before.HeapAlloc); live > peak {
				t.Errorf("%s %dx%d: %d bytes live while rendering, estimate reserved only %d", tc.name, outW, outH, live, peak)
			}
		}
	}
}

func TestEstimateImageMemoryFormatLayouts(t *testing.T) {
	const width, height = 1000, 600
	pixels := int64(width * height)
	output := int64(320 * 320)
	base := imageDecodeSlackBytes + output*imageOutputBytesPerPixel

	ihdr := func(depth, colorType, interlace byte) []byte {
		data := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR"), make([]byte, 17)...)
		binary.BigEndian.PutUint32(data[16:], width)
		binary.BigEndian.PutUint32(data[20:], height)
		data[24], data[25], data[28] = depth, colorType, interlace
		return data
	}
	row := func(bits int64) int64 { return 2 * (1 + bits*width/8 + 1) }
	for _, tc := range []struct {
		name                        string
		depth, colorType, interlace byte
		want                        int64
	}{
		{"gray8 (NRGBA with tRNS)", 8, 0, 0, 4*pixels + row(8)},
		{"gray16 (NRGBA64 with tRNS)", 16, 0, 0, 8*pixels + row(16)},
		{"rgb8", 8, 2, 0, 4*pixels + row(24)},
		{"paletted", 8, 3, 0, pixels + row(8)},
		{"rgba16", 16, 6, 0, 8*pixels + row(64)},
		{"rgba16 interlaced", 16, 6, 1, 16*pixels + row(64)},
	} {
		got := estimateImageMemory(ihdr(tc.depth, tc.colorType, tc.interlace), "png", image.Config{Width: width, Height: height}, output)
		if got != base+tc.want {
			t.Errorf("png %s: got %d, want %d", tc.name, got, base+tc.want)
		}
	}

	// Synthetic SOF headers: image/jpeg's buffers follow the component layout.
	sof := func(marker byte, adobeRGB bool, components ...[3]byte) []byte {
		data := []byte{0xff, 0xd8}
		if adobeRGB {
			data = append(data, 0xff, 0xee, 0, 14, 'A', 'd', 'o', 'b', 'e', 0, 100, 0, 0, 0, 0, 0)
		}
		h, w := height, width
		segment := []byte{8, byte(h >> 8), byte(h), byte(w >> 8), byte(w), byte(len(components))}
		for _, c := range components {
			segment = append(segment, c[0], c[1], c[2])
		}
		data = append(data, 0xff, marker, byte((len(segment)+2)>>8), byte(len(segment)+2))
		data = append(data, segment...)
		return append(data, 0xff, 0xda, 0, 2, 0x12, 0x34, 0xff, 0xd9)
	}
	config := image.Config{Width: width, Height: height}
	blocks := func(h, v int64) int64 { // MCU grid for a 2x2 maximum factor.
		return ((width + 15) / 16) * ((height + 15) / 16) * h * v
	}
	paddedPixels := int64(((width + 15) / 16) * 16 * ((height + 15) / 16) * 16)
	for _, tc := range []struct {
		name string
		data []byte
		want int64
	}{
		{"baseline 4:2:0", sof(0xc0, false, [3]byte{1, 0x22, 0}, [3]byte{2, 0x11, 1}, [3]byte{3, 0x11, 1}), paddedPixels + paddedPixels/2},
		{"progressive 4:2:0", sof(0xc2, false, [3]byte{1, 0x22, 0}, [3]byte{2, 0x11, 1}, [3]byte{3, 0x11, 1}), paddedPixels + paddedPixels/2 + 256*(blocks(2, 2)+2*blocks(1, 1))},
		{"progressive 4:4:4", sof(0xc2, false, [3]byte{1, 0x11, 0}, [3]byte{2, 0x11, 1}, [3]byte{3, 0x11, 1}), 3*pixels + 3*256*pixels/64},
		{"flex chroma", sof(0xc0, false, [3]byte{1, 0x22, 0}, [3]byte{2, 0x21, 1}, [3]byte{3, 0x11, 1}), 3 * paddedPixels},
		{"adobe RGB", sof(0xc0, true, [3]byte{1, 0x11, 0}, [3]byte{2, 0x11, 1}, [3]byte{3, 0x11, 1}), 3*pixels + 4*pixels},
		{"RGB component ids", sof(0xc0, false, [3]byte{'R', 0x11, 0}, [3]byte{'G', 0x11, 1}, [3]byte{'B', 0x11, 1}), 3*pixels + 4*pixels},
		{"CMYK", sof(0xc0, true, [3]byte{1, 0x11, 0}, [3]byte{2, 0x11, 1}, [3]byte{3, 0x11, 1}, [3]byte{4, 0x11, 1}), 3*pixels + pixels + 4*pixels},
		{"gray progressive", sof(0xc2, false, [3]byte{1, 0x22, 0}), pixels + 256*pixels/64},
	} {
		if got := estimateImageMemory(tc.data, "jpeg", config, output); got != base+tc.want {
			t.Errorf("jpeg %s: got %d, want %d", tc.name, got, base+tc.want)
		}
	}
	plain := sof(0xc0, false, [3]byte{1, 0x11, 0}, [3]byte{2, 0x11, 1}, [3]byte{3, 0x11, 1})
	rotated := appendJPEGEXIFOrientation(plain, 6)
	if diff := estimateImageMemory(rotated, "jpeg", config, output) - estimateImageMemory(plain, "jpeg", config, output); diff != 4*pixels {
		t.Errorf("EXIF orientation adds %d bytes, want an NRGBA copy of %d", diff, 4*pixels)
	}
	if got, want := estimateImageMemory([]byte("GIF89a"), "gif", config, output), base+2*pixels; got != want {
		t.Errorf("gif: got %d, want %d", got, want)
	}
	// An unparsable header falls back to the colour model with headroom.
	if got, want := estimateImageMemory(nil, "png", image.Config{Width: width, Height: height, ColorModel: color.NRGBA64Model}, output), base+8*3*pixels; got != want {
		t.Errorf("fallback: got %d, want %d", got, want)
	}
	if got := estimateImageMemory(nil, "png", image.Config{Width: 1 << 30, Height: 1 << 30}, 1<<40); got <= 0 {
		t.Errorf("estimate overflowed to %d", got)
	}
}

// uniformPNG16 streams a width x height 16-bit RGBA PNG of one colour without
// materializing the image, the shape the audit flagged: tiny on the wire,
// 8 bytes per pixel decoded.
func uniformPNG16(t testing.TB, width, height int) []byte {
	t.Helper()
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(tag string, data []byte) {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(data)))
		out.Write(length[:])
		out.WriteString(tag)
		out.Write(data)
		crc := crc32.NewIEEE()
		crc.Write([]byte(tag))
		crc.Write(data)
		binary.BigEndian.PutUint32(length[:], crc.Sum32())
		out.Write(length[:])
	}
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[0:], uint32(width))
	binary.BigEndian.PutUint32(header[4:], uint32(height))
	header[8], header[9] = 16, 6
	chunk("IHDR", header)
	var compressed bytes.Buffer
	writer, err := zlib.NewWriterLevel(&compressed, zlib.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	row := make([]byte, 1+8*width)
	for x := 0; x < width; x++ {
		copy(row[1+8*x:], []byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xff, 0xff})
	}
	for y := 0; y < height; y++ {
		if _, err := writer.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	chunk("IDAT", compressed.Bytes())
	chunk("IEND", nil)
	return out.Bytes()
}

func uploadThumbnailService(t *testing.T, budget int64, data []byte) (*Service, File) {
	t.Helper()
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const key = "objects/00000000-0000-4000-8000-0000000000b1"
	if err := store.Put(context.Background(), key, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	// Defaults of pgbase.FileOptions: 10 MiB files, 40 MP images, 320 px thumbnails.
	service := &Service{objects: store, options: Options{MaximumBytes: 10 << 20, MaximumImagePixels: 40_000_000, ThumbnailDimension: 320}, imageBudget: newImageMemoryBudget(budget)}
	return service, File{ObjectKey: key, ContentType: "image/png"}
}

func concurrentThumbnails(service *Service, file File, n int) []error {
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = service.buildThumbnail(context.Background(), file)
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

// TestSixteenBitPNGOverBudgetDecodesOneAtATime uses the audited shape at full
// size: 6000x6666 16-bit RGBA (about 320 MB decoded) compresses far below the
// 10 MiB field limit, and its estimate exceeds the default budget. Eight
// concurrent uploads all succeed, but each reserves the whole budget, so only
// one decodes at a time and the reservation never exceeds the budget.
func TestSixteenBitPNGOverBudgetDecodesOneAtATime(t *testing.T) {
	data := uniformPNG16(t, 6000, 6666)
	if len(data) > 10<<20 {
		t.Fatalf("encoded size %d exceeds the default field limit", len(data))
	}
	config, format, err := decodeImageConfig(bytes.NewReader(data), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	estimate := estimateImageMemory(data, format, config, 320*320)
	if estimate < 6000*6666*8 || estimate <= DefaultImageDecodeMemoryBytes {
		t.Fatalf("estimate %d does not cover the decoded image", estimate)
	}
	service, file := uploadThumbnailService(t, DefaultImageDecodeMemoryBytes, data)
	for i, err := range concurrentThumbnails(service, file, 8) {
		if err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
	}
	held, peak, waiting := service.imageBudget.snapshot()
	if held != 0 || waiting != 0 {
		t.Fatalf("held=%d waiting=%d after all uploads", held, waiting)
	}
	// Every holder reserved the whole budget, so two never overlapped.
	if peak != DefaultImageDecodeMemoryBytes {
		t.Fatalf("peak=%d, want exactly the budget %d", peak, DefaultImageDecodeMemoryBytes)
	}
}

// TestSixteenBitPNGConcurrentUploadsStayWithinBudget decodes the same shape
// for real with a budget that admits one image at a time.
func TestSixteenBitPNGConcurrentUploadsStayWithinBudget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
		admitted      int64
	}{
		{"scaled", 600, 667, 2},
		{"full", 6000, 6666, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "full" && testing.Short() {
				t.Skip("decodes 320 MB images")
			}
			data := uniformPNG16(t, tc.width, tc.height)
			config, format, err := decodeImageConfig(bytes.NewReader(data), "image/png")
			if err != nil {
				t.Fatal(err)
			}
			outW, outH := thumbnailOutputSize(config.Width, config.Height, 320)
			estimate := estimateImageMemory(data, format, config, int64(outW*outH))
			capacity := tc.admitted*estimate + estimate/2
			service, file := uploadThumbnailService(t, capacity, data)
			for i, err := range concurrentThumbnails(service, file, 8) {
				if err != nil {
					t.Fatalf("upload %d: %v", i, err)
				}
			}
			held, peak, waiting := service.imageBudget.snapshot()
			if held != 0 || waiting != 0 {
				t.Fatalf("held=%d waiting=%d after all uploads", held, waiting)
			}
			if peak > capacity || peak < estimate {
				t.Fatalf("peak=%d, want between %d and capacity %d", peak, estimate, capacity)
			}
		})
	}
}

func budgetTestService(capacity int64) *Service {
	return &Service{options: Options{MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000, ThumbnailDimension: 32}, imageBudget: newImageMemoryBudget(capacity)}
}

func smallPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 64, 48))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestImageBudgetSerializesConcurrentRenders(t *testing.T) {
	data := smallPNG(t)
	config, format, err := decodeImageConfig(bytes.NewReader(data), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	estimate := estimateImageMemory(data, format, config, 16*16)
	service := budgetTestService(3*estimate + estimate/2)
	var active, maximum atomic.Int64
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.renderDecodedImage(context.Background(), data, "image/png", fixedOutput(16*16), false, func(image.Image, string) ([]byte, error) {
				now := active.Add(1)
				for {
					seen := maximum.Load()
					if now <= seen || maximum.CompareAndSwap(seen, now) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				active.Add(-1)
				return []byte{1}, nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	held, peak, waiting := service.imageBudget.snapshot()
	if held != 0 || waiting != 0 {
		t.Fatalf("held=%d waiting=%d", held, waiting)
	}
	if peak > service.imageBudget.capacity || maximum.Load() > 3 {
		t.Fatalf("peak=%d of %d with %d concurrent renders", peak, service.imageBudget.capacity, maximum.Load())
	}
	if maximum.Load() < 2 {
		t.Fatalf("renders never overlapped (max %d); the budget admitted too little", maximum.Load())
	}
}

// TestImageBudgetRunsOversizedImageAlone checks that an image whose estimate
// exceeds the whole budget is rendered rather than refused, and that it runs
// alone: it waits for every other holder, and while it holds the budget
// nothing else is admitted, so the reserved estimates never sum past it.
func TestImageBudgetRunsOversizedImageAlone(t *testing.T) {
	data := smallPNG(t)
	service := budgetTestService(64 << 10)
	config, format, err := decodeImageConfig(bytes.NewReader(data), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if estimate := estimateImageMemory(data, format, config, 32*32); estimate <= service.imageBudget.capacity {
		t.Fatalf("estimate %d fits the %d budget; the test needs an oversized image", estimate, service.imageBudget.capacity)
	}
	waitQueued := func(n int) {
		for {
			if _, _, waiting := service.imageBudget.snapshot(); waiting == n {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}

	// Another holder keeps the oversized image waiting until it releases.
	hold, err := service.imageBudget.acquire(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := service.buildThumbnailData(context.Background(), data); done <- err }()
	waitQueued(1)
	select {
	case err := <-done:
		t.Fatalf("oversized image ran beside another holder: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	hold()
	if err := <-done; err != nil {
		t.Fatalf("oversized image: %v", err)
	}

	// While it holds the budget, a request of any size waits behind it.
	entered, leave := make(chan struct{}), make(chan struct{})
	rendered := make(chan error, 1)
	go func() {
		_, err := service.renderDecodedImage(context.Background(), data, "image/png", fixedOutput(32*32), false, func(image.Image, string) ([]byte, error) {
			close(entered)
			<-leave
			return []byte{1}, nil
		})
		rendered <- err
	}()
	<-entered
	if held, _, _ := service.imageBudget.snapshot(); held != service.imageBudget.capacity {
		t.Fatalf("oversized image holds %d, want the whole budget %d", held, service.imageBudget.capacity)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := service.imageBudget.acquire(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire beside an oversized holder: err=%v, want it to wait", err)
	}
	close(leave)
	if err := <-rendered; err != nil {
		t.Fatal(err)
	}
	// The lock-holding variant path renders oversized images too.
	if _, err := service.renderThumbnailData(context.Background(), data, "image/png", "16x16"); err != nil {
		t.Fatalf("variant: %v", err)
	}
	held, peak, waiting := service.imageBudget.snapshot()
	if held != 0 || waiting != 0 || peak != service.imageBudget.capacity {
		t.Fatalf("held=%d peak=%d waiting=%d, want peak at the budget %d", held, peak, waiting, service.imageBudget.capacity)
	}
}

// TestVariantReservationFollowsTheRenderedSize checks that a variant reserves
// memory for the output its spec yields for the source size, not for the
// largest output any spec could request.
func TestVariantReservationFollowsTheRenderedSize(t *testing.T) {
	data := smallPNG(t) // 64x48
	config, format, err := decodeImageConfig(bytes.NewReader(data), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		variant      string
		outputPixels int64
	}{
		{"16x16", 16 * 16},
		{"0x24", 32 * 24},
		{"40x0", 40 * 30},
		{"40x40f", 40 * 30},
		{"1000x1000", 1000 * 1000},
	} {
		service := budgetTestService(1 << 40)
		if _, err := service.renderThumbnailData(context.Background(), data, "image/png", tc.variant); err != nil {
			t.Fatalf("%s: %v", tc.variant, err)
		}
		if _, peak, _ := service.imageBudget.snapshot(); peak != estimateImageMemory(data, format, config, tc.outputPixels) {
			t.Errorf("%s: reserved %d, want the estimate for %d output pixels (%d)", tc.variant, peak, tc.outputPixels, estimateImageMemory(data, format, config, tc.outputPixels))
		}
	}
}

// buildThumbnailData runs the upload thumbnail pipeline on bytes for tests.
func (s *Service) buildThumbnailData(ctx context.Context, data []byte) ([]byte, error) {
	return s.renderDecodedImage(ctx, data, "image/png", fixedOutput(int64(s.options.ThumbnailDimension)*int64(s.options.ThumbnailDimension)), false, func(source image.Image, format string) ([]byte, error) {
		return []byte{1}, nil
	})
}

func TestImageBudgetWaitHonoursCancellationWithoutLeaking(t *testing.T) {
	data := smallPNG(t)
	service := budgetTestService(8 << 20)
	hold, err := service.imageBudget.acquire(context.Background(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := service.buildThumbnailData(ctx, data); done <- err }()
	for {
		if _, _, waiting := service.imageBudget.snapshot(); waiting == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled wait did not return")
	}
	hold()
	if held, _, waiting := service.imageBudget.snapshot(); held != 0 || waiting != 0 {
		t.Fatalf("held=%d waiting=%d after cancellation", held, waiting)
	}
	// An already canceled context never reserves.
	if _, err := service.buildThumbnailData(ctx, data); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if held, _, _ := service.imageBudget.snapshot(); held != 0 {
		t.Fatalf("held=%d", held)
	}
}

func TestLockHoldingRenderWaitIsBounded(t *testing.T) {
	previous := lockedImageBudgetWait
	lockedImageBudgetWait = 50 * time.Millisecond
	t.Cleanup(func() { lockedImageBudgetWait = previous })
	data := smallPNG(t)
	service := budgetTestService(8 << 20)
	hold, err := service.imageBudget.acquire(context.Background(), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := service.renderThumbnailData(context.Background(), data, "image/png", "16x16"); !errors.Is(err, ErrImageBusy) {
		t.Fatalf("err=%v, want ErrImageBusy", err)
	}
	if _, err := service.RenderThumbnailData(data, "image/png", "16x16"); !errors.Is(err, ErrImageBusy) {
		t.Fatalf("public err=%v, want ErrImageBusy", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("bounded waits took %v", elapsed)
	}
	// The caller's own deadline is reported as such, not as busy.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := service.renderThumbnailData(ctx, data, "image/png", "16x16"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want the caller's deadline", err)
	}
	hold()
	if _, err := service.renderThumbnailData(context.Background(), data, "image/png", "16x16"); err != nil {
		t.Fatal(err)
	}
	if held, _, waiting := service.imageBudget.snapshot(); held != 0 || waiting != 0 {
		t.Fatalf("held=%d waiting=%d", held, waiting)
	}
}

func TestImageBudgetReleasedOnPanicAndErrors(t *testing.T) {
	data := smallPNG(t)
	service := budgetTestService(8 << 20)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("render did not panic")
			}
		}()
		_, _ = service.renderDecodedImage(context.Background(), data, "image/png", fixedOutput(1), false, func(image.Image, string) ([]byte, error) {
			panic("encoder failure")
		})
	}()
	if _, err := service.renderDecodedImage(context.Background(), data, "image/png", fixedOutput(1), false, func(image.Image, string) ([]byte, error) {
		return nil, ErrInvalid
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
	// A valid header over a corrupt body fails inside the decoder.
	corrupt := append([]byte(nil), data...)
	for i := 40; i < len(corrupt)-12; i++ {
		corrupt[i] ^= 0x5a
	}
	if _, err := service.renderThumbnailData(context.Background(), corrupt, "image/png", "8x8"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("corrupt err=%v", err)
	}
	held, peak, waiting := service.imageBudget.snapshot()
	if held != 0 || waiting != 0 || peak == 0 {
		t.Fatalf("held=%d peak=%d waiting=%d", held, peak, waiting)
	}
}

func TestImageBudgetIsFIFO(t *testing.T) {
	budget := newImageMemoryBudget(100)
	first, err := budget.acquire(context.Background(), 60)
	if err != nil {
		t.Fatal(err)
	}
	order := make(chan int64, 2)
	waitQueued := func(n int) {
		for {
			if _, _, waiting := budget.snapshot(); waiting == n {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
	var wg sync.WaitGroup
	for i, weight := range []int64{80, 30} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := budget.acquire(context.Background(), weight)
			if err != nil {
				t.Error(err)
				return
			}
			order <- weight
			release()
		}()
		waitQueued(i + 1)
	}
	// 40 bytes are free, but the small request must not overtake the large
	// one; once the large one holds 80, the small one waits for its release.
	select {
	case weight := <-order:
		t.Fatalf("request of %d overtook the queue head", weight)
	case <-time.After(20 * time.Millisecond):
	}
	first()
	wg.Wait()
	if a, b := <-order, <-order; a != 80 || b != 30 {
		t.Fatalf("grant order %d, %d", a, b)
	}
	if held, peak, _ := budget.snapshot(); held != 0 || peak > 100 {
		t.Fatalf("held=%d peak=%d", held, peak)
	}
}

// fixedOutput reports the same output size whatever the source size.
func fixedOutput(pixels int64) func(int, int) int64 {
	return func(int, int) int64 { return pixels }
}

func (b *imageMemoryBudget) snapshot() (held, peak int64, waiting int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.held, b.peak, b.waiters.Len()
}
