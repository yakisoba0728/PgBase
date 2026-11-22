package filestore

import (
	"encoding/binary"
	"image"
	"image/color"
	"math"
)

// The estimate mirrors what the decode-orient-resample-encode pipeline
// allocates for one image, read from the standard library and x/image
// decoders it calls. It errs high: an underestimate would let concurrent
// work exceed the budget it is meant to enforce.
const (
	// Decoder tables, Huffman/LZW/zlib state, sample grids, palettes and the
	// PNG encoder's flate compressor (about 1 MiB of hash chains on its own).
	imageDecodeSlackBytes int64 = 4 << 20
	// sampleNearest's NRGBA output (4) plus the encoder's bytes.Buffer, which
	// may grow to twice an incompressible raw NRGBA payload before the size
	// check runs (8).
	imageOutputBytesPerPixel int64 = 12
	// orientImage materializes a full-size NRGBA copy.
	imageOrientationBytesPerPixel int64 = 4
	// Used for a format without its own parser.
	imageUnknownDecoderFactor int64 = 3
)

// colorModelBytesPerPixel is the storage of one decoded pixel for a model.
// Models the pipeline does not recognize get the widest standard layout.
func colorModelBytesPerPixel(model color.Model) int64 {
	if _, ok := model.(color.Palette); ok {
		return 1
	}
	switch model {
	case color.RGBAModel, color.NRGBAModel, color.CMYKModel, color.NYCbCrAModel:
		return 4
	case color.RGBA64Model, color.NRGBA64Model:
		return 8
	case color.GrayModel, color.AlphaModel:
		return 1
	case color.Gray16Model, color.Alpha16Model:
		return 2
	case color.YCbCrModel:
		// Subsampling is unknown from the model alone; assume 4:4:4.
		return 3
	default:
		return 8
	}
}

// estimateImageMemory returns the bytes one render of data may hold at once:
// the decoder's buffers, the EXIF-orientation copy when one will be made, and
// outputPixels of resampled and encoded output. format is the matched
// DecodeConfig format and config its result.
func estimateImageMemory(data []byte, format string, config image.Config, outputPixels int64) int64 {
	pixels := saturatingMul(int64(config.Width), int64(config.Height))
	total := saturatingAdd(imageDecodeSlackBytes, decodeMemory(data, format, config, pixels))
	if format == "jpeg" {
		if orientation := jpegEXIFOrientation(data); orientation >= 2 && orientation <= 8 {
			total = saturatingAdd(total, saturatingMul(pixels, imageOrientationBytesPerPixel))
		}
	}
	return saturatingAdd(total, saturatingMul(max(outputPixels, 1), imageOutputBytesPerPixel))
}

// decodeMemory estimates the decoder's own allocations. A header the format
// parser cannot follow is charged the format's worst case, never something
// smaller: the decoders accept streams a strict parser would refuse, and a
// lower fallback would let crafted bytes shrink the reservation.
func decodeMemory(data []byte, format string, config image.Config, pixels int64) int64 {
	switch format {
	case "png":
		if estimate, ok := pngDecodeMemory(data, pixels); ok {
			return estimate
		}
		return pngWorstDecodeMemory(config, pixels)
	case "jpeg":
		if estimate, ok := jpegDecodeMemory(data, config); ok {
			return estimate
		}
		return jpegWorstDecodeMemory(config, pixels)
	case "gif":
		// image/gif keeps only the first frame, a Paletted no larger than the
		// logical screen, and de-interlacing copies its Pix once.
		return saturatingMul(pixels, 2)
	case "webp":
		if estimate, ok := webpDecodeMemory(data, config, pixels); ok {
			return estimate
		}
		return webpWorstDecodeMemory(data, config, pixels)
	default:
		return saturatingMul(pixels, saturatingMul(colorModelBytesPerPixel(config.ColorModel), imageUnknownDecoderFactor))
	}
}

// pngWorstDecodeMemory is pngDecodeMemory's largest result for the image
// size: interlaced 16-bit RGBA.
func pngWorstDecodeMemory(config image.Config, pixels int64) int64 {
	row := saturatingAdd(2, saturatingMul(8, int64(config.Width)))
	return saturatingAdd(saturatingMul(pixels, 16), saturatingMul(row, 2))
}

// jpegWorstDecodeMemory bounds jpegDecodeMemory for every layout image/jpeg
// accepts at this size: four progressive components at full resolution,
// padded by up to one 32-pixel MCU per axis (luma, flex-mode chroma, the
// black plane and 4 bytes of coefficients per sample of each), the CMYK or
// RGB result, and an EXIF-orientation copy whether or not one is found.
func jpegWorstDecodeMemory(config image.Config, pixels int64) int64 {
	padded := saturatingMul(int64(config.Width)+31, int64(config.Height)+31)
	planes := saturatingMul(padded, 1+2+1+4*4)
	return saturatingAdd(planes, saturatingMul(pixels, 4), saturatingMul(pixels, imageOrientationBytesPerPixel))
}

// webpWorstDecodeMemory is webpDecodeMemory's largest branch, an animation,
// charged when the chunk walk fails: x/image/webp ignores bytes after the
// RIFF chunk that parseWebPChunks refuses, so an unparsed file may still
// decode.
func webpWorstDecodeMemory(data []byte, config image.Config, pixels int64) int64 {
	macroblocks := saturatingMul((int64(config.Width)+15)/16, (int64(config.Height)+15)/16)
	lossy := saturatingMul(macroblocks, 16*16+2*8*8+16)
	return saturatingAdd(saturatingMul(int64(len(data)), 3), lossy, saturatingMul(pixels, 12), saturatingMul(pixels, 4))
}

// pngDecodeMemory follows image/png: one image of the IHDR colour type and
// depth (8-bit grey and truecolour become NRGBA when a tRNS chunk is present,
// 16-bit ones NRGBA64), two filter rows, and for Adam7 a pass image next to the
// full-size destination.
func pngDecodeMemory(data []byte, pixels int64) (int64, bool) {
	if len(data) < 33 || string(data[12:16]) != "IHDR" {
		return 0, false
	}
	width := int64(binary.BigEndian.Uint32(data[16:20]))
	depth, colorType, interlace := int64(data[24]), data[25], data[28]
	var channels, bytesPerPixel int64
	switch colorType {
	case 0:
		channels = 1
	case 2:
		channels = 3
	case 3:
		channels, bytesPerPixel = 1, 1
	case 4:
		channels = 2
	case 6:
		channels = 4
	default:
		return 0, false
	}
	if bytesPerPixel == 0 {
		bytesPerPixel = 4
		if depth == 16 {
			bytesPerPixel = 8
		}
	}
	decoded := saturatingMul(pixels, bytesPerPixel)
	if interlace != 0 {
		decoded = saturatingMul(decoded, 2)
	}
	row := saturatingAdd(1, saturatingMul(saturatingMul(channels, depth), width)/8+1)
	return saturatingAdd(decoded, saturatingMul(row, 2)), true
}

type jpegComponent struct{ id, h, v int64 }

type jpegFrame struct {
	progressive bool
	adobeRGB    bool
	components  []jpegComponent
}

// jpegDecodeMemory follows image/jpeg: a Gray or YCbCr image padded to whole
// MCUs (4:4:4 in flex mode), a black plane plus a CMYK result for four
// components, an RGBA copy for RGB-coded images, and for progressive images
// one int32 coefficient block per 8x8 block of every component.
func jpegDecodeMemory(data []byte, config image.Config) (int64, bool) {
	frame, ok := parseJPEGFrame(data)
	if !ok {
		return 0, false
	}
	n := len(frame.components)
	if n != 1 && n != 3 && n != 4 {
		return 0, false
	}
	width, height := int64(config.Width), int64(config.Height)
	maxH, maxV := int64(1), int64(1)
	for _, component := range frame.components {
		maxH, maxV = max(maxH, component.h), max(maxV, component.v)
	}
	mxx := (width + 8*maxH - 1) / (8 * maxH)
	myy := (height + 8*maxV - 1) / (8 * maxV)
	paddedWidth, paddedHeight := 8*maxH*mxx, 8*maxV*myy
	var total int64
	if n == 1 {
		total = saturatingMul(paddedWidth, paddedHeight)
	} else {
		luma := saturatingMul(paddedWidth, paddedHeight)
		c0, c1, c2 := frame.components[0], frame.components[1], frame.components[2]
		flex := c1.h != c2.h || c1.v != c2.v || maxH != c0.h || maxV != c0.v
		if !flex {
			switch (maxH/c1.h)<<4 | (maxV / c1.v) {
			case 0x11, 0x12, 0x21, 0x22, 0x41, 0x42:
				flex = maxH%c1.h != 0 || maxV%c1.v != 0
			default:
				flex = true
			}
		}
		chroma := saturatingMul(luma, 2)
		if !flex {
			chroma = saturatingMul(saturatingMul(8*c1.h*mxx, 8*c1.v*myy), 2)
		}
		total = saturatingAdd(luma, chroma)
		pixels := saturatingMul(width, height)
		if n == 4 {
			c3 := frame.components[3]
			total = saturatingAdd(total, saturatingMul(8*c3.h*mxx, 8*c3.v*myy), saturatingMul(pixels, 4))
		} else if frame.adobeRGB || c0.id == 'R' && c1.id == 'G' && c2.id == 'B' {
			total = saturatingAdd(total, saturatingMul(pixels, 4))
		}
	}
	if frame.progressive {
		for _, component := range frame.components {
			blocks := saturatingMul(saturatingMul(mxx, myy), component.h*component.v)
			total = saturatingAdd(total, saturatingMul(blocks, 64*4))
		}
	}
	return total, true
}

// parseJPEGFrame reads the SOF component layout and whether any APP14 Adobe
// segment declares RGB coding. It walks every marker segment, skipping
// entropy-coded data, so late APP14 segments that image/jpeg still honours
// are seen too.
func parseJPEGFrame(data []byte) (jpegFrame, bool) {
	var frame jpegFrame
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return frame, false
	}
	found := false
	for offset := 2; ; {
		// image/jpeg silently skips extraneous bytes up to the next 0xff, treats
		// "\xff\x00" as extraneous too, and skips 0xff fill bytes before a
		// marker. Refusing the stream here instead would trade the real layout
		// for a guess the decoder never makes.
		for offset < len(data) && data[offset] != 0xff {
			offset++
		}
		if offset+1 >= len(data) {
			break
		}
		marker := data[offset+1]
		offset += 2
		if marker == 0 {
			continue
		}
		for marker == 0xff && offset < len(data) {
			marker = data[offset]
			offset++
		}
		if marker == 0xff || marker == 0xd9 {
			break
		}
		if marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if offset+2 > len(data) {
			break
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || length > len(data)-offset {
			break
		}
		segment := data[offset+2 : offset+length]
		offset += length
		switch {
		case marker == 0xc0 || marker == 0xc1 || marker == 0xc2:
			if found || len(segment) < 6 {
				return frame, false
			}
			count := int(segment[5])
			if len(segment) < 6+3*count {
				return frame, false
			}
			for i := range count {
				id, sampling := int64(segment[6+3*i]), segment[7+3*i]
				h, v := int64(sampling>>4), int64(sampling&0x0f)
				if h < 1 || h > 4 || v < 1 || v > 4 {
					return frame, false
				}
				if count == 1 {
					// image/jpeg ignores the sampling factors of a single
					// component.
					h, v = 1, 1
				}
				frame.components = append(frame.components, jpegComponent{id: id, h: h, v: v})
			}
			frame.progressive = marker == 0xc2
			found = true
		case marker == 0xee && len(segment) >= 12 && string(segment[:5]) == "Adobe":
			if segment[11] == 0 {
				frame.adobeRGB = true
			}
		case marker == 0xda:
			// Skip entropy-coded data up to the next real marker.
			for offset+1 < len(data) && (data[offset] != 0xff || data[offset+1] == 0 || data[offset+1] >= 0xd0 && data[offset+1] <= 0xd7) {
				offset++
			}
		}
	}
	return frame, found
}

// webpDecodeMemory follows x/image/webp and decodeAnimatedWebPFirstFrame.
// Lossy VP8 decodes into a macroblock-padded 4:2:0 YCbCr image and reads its
// partitions into memory; VP8L (including VP8L-compressed alpha, decoded as a
// full NRGBA) allocates its pixels and a transform output; an animation adds
// the still copy of its first frame and the full NRGBA canvas.
func webpDecodeMemory(data []byte, config image.Config, pixels int64) (int64, bool) {
	chunks, err := parseWebPChunks(data)
	if err != nil {
		return 0, false
	}
	macroblocks := saturatingMul((int64(config.Width)+15)/16, (int64(config.Height)+15)/16)
	lossy := saturatingMul(macroblocks, 16*16+2*8*8+16)
	lossless := saturatingMul(pixels, 12)
	input := saturatingMul(int64(len(data)), 2)
	tags := make(map[string]bool, len(chunks))
	for _, chunk := range chunks {
		tags[chunk.tag] = true
	}
	if tags["ANIM"] || tags["ANMF"] {
		canvas := saturatingMul(pixels, 4)
		return saturatingAdd(input, int64(len(data)), lossy, lossless, canvas), true
	}
	total := input
	if tags["VP8 "] {
		total = saturatingAdd(total, lossy)
	}
	if tags["VP8L"] || tags["ALPH"] {
		total = saturatingAdd(total, lossless)
	}
	if !tags["VP8 "] && !tags["VP8L"] {
		return 0, false
	}
	return total, true
}

func saturatingMul(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

func saturatingAdd(values ...int64) int64 {
	var total int64
	for _, value := range values {
		if value > 0 && total > math.MaxInt64-value {
			return math.MaxInt64
		}
		total += value
	}
	return total
}
