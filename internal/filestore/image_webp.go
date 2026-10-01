package filestore

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/webp"
)

type webPChunk struct {
	tag  string
	data []byte
}

const maximumWebPChunks = 1024

func decodeAnimatedWebPFirstFrame(data []byte, maximumImagePixels int64) (image.Image, error) {
	if maximumImagePixels < 1 {
		return nil, ErrInvalid
	}
	chunks, err := parseWebPChunks(data)
	if err != nil {
		return nil, ErrInvalid
	}
	canvasWidth, canvasHeight := 0, 0
	background := color.NRGBA{}
	animated, sawVP8X, sawAnimation := false, false, false
	for _, chunk := range chunks {
		switch chunk.tag {
		case "VP8X":
			if sawVP8X || len(chunk.data) != 10 {
				return nil, ErrInvalid
			}
			sawVP8X = true
			animated = chunk.data[0]&0x02 != 0
			canvasWidth = 1 + int(uint24(chunk.data[4:7]))
			canvasHeight = 1 + int(uint24(chunk.data[7:10]))
		case "ANIM":
			if !animated || sawAnimation || len(chunk.data) != 6 {
				return nil, ErrInvalid
			}
			background = color.NRGBA{R: chunk.data[2], G: chunk.data[1], B: chunk.data[0], A: chunk.data[3]}
			sawAnimation = true
		case "ANMF":
			if !animated || !sawAnimation || canvasWidth < 1 || canvasHeight < 1 || len(chunk.data) < 16 {
				return nil, ErrInvalid
			}
			if int64(canvasWidth) > maximumImagePixels/int64(canvasHeight) {
				return nil, ErrInvalid
			}
			return decodeWebPAnimationFrame(chunk.data, canvasWidth, canvasHeight, background, maximumImagePixels)
		}
	}
	return nil, ErrInvalid
}

func parseWebPChunks(data []byte) ([]webPChunk, error) {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" || uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != uint64(len(data)) {
		return nil, ErrInvalid
	}
	chunks := make([]webPChunk, 0, 4)
	for offset := 12; offset < len(data); {
		if offset+8 > len(data) {
			return nil, ErrInvalid
		}
		size := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := uint64(offset + 8)
		end := start + size
		paddedEnd := end + size%2
		if end < start || paddedEnd > uint64(len(data)) {
			return nil, ErrInvalid
		}
		chunks = append(chunks, webPChunk{tag: string(data[offset : offset+4]), data: data[start:end]})
		if len(chunks) > maximumWebPChunks {
			return nil, ErrInvalid
		}
		offset = int(paddedEnd)
	}
	return chunks, nil
}

func decodeWebPAnimationFrame(frame []byte, canvasWidth, canvasHeight int, background color.NRGBA, maximumImagePixels int64) (image.Image, error) {
	x := 2 * int(uint24(frame[0:3]))
	y := 2 * int(uint24(frame[3:6]))
	width := 1 + int(uint24(frame[6:9]))
	height := 1 + int(uint24(frame[9:12]))
	if width < 1 || height < 1 || x > canvasWidth-width || y > canvasHeight-height {
		return nil, ErrInvalid
	}
	frameChunks, err := parseWebPFrameChunks(frame[16:])
	if err != nil {
		return nil, err
	}
	still, err := buildStillWebP(frameChunks, width, height)
	if err != nil {
		return nil, err
	}
	config, err := webp.DecodeConfig(bytes.NewReader(still))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width != width || config.Height != height || int64(config.Width) > maximumImagePixels/int64(config.Height) {
		return nil, ErrInvalid
	}
	decoded, err := webp.Decode(bytes.NewReader(still))
	if err != nil || decoded.Bounds().Dx() != width || decoded.Bounds().Dy() != height {
		return nil, ErrInvalid
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, canvasWidth, canvasHeight))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: background}, image.Point{}, draw.Src)
	op := draw.Over
	if frame[15]&0x02 != 0 {
		op = draw.Src
	}
	draw.Draw(canvas, image.Rect(x, y, x+width, y+height), decoded, decoded.Bounds().Min, op)
	return canvas, nil
}

func parseWebPFrameChunks(data []byte) ([]webPChunk, error) {
	chunks := make([]webPChunk, 0, 2)
	chunkCount := 0
	for offset := 0; offset < len(data); {
		chunkCount++
		if chunkCount > maximumWebPChunks {
			return nil, ErrInvalid
		}
		if offset+8 > len(data) {
			return nil, ErrInvalid
		}
		size := uint64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := uint64(offset + 8)
		end := start + size
		paddedEnd := end + size%2
		if end < start || paddedEnd > uint64(len(data)) {
			return nil, ErrInvalid
		}
		tag := string(data[offset : offset+4])
		if tag == "ALPH" || tag == "VP8 " || tag == "VP8L" {
			chunks = append(chunks, webPChunk{tag: tag, data: data[start:end]})
		}
		offset = int(paddedEnd)
	}
	return chunks, nil
}

func buildStillWebP(chunks []webPChunk, width, height int) ([]byte, error) {
	var alpha, bitstream *webPChunk
	for i := range chunks {
		switch chunks[i].tag {
		case "ALPH":
			if alpha != nil || bitstream != nil {
				return nil, ErrInvalid
			}
			alpha = &chunks[i]
		case "VP8 ", "VP8L":
			if bitstream != nil || chunks[i].tag == "VP8L" && alpha != nil {
				return nil, ErrInvalid
			}
			bitstream = &chunks[i]
		}
	}
	if bitstream == nil {
		return nil, ErrInvalid
	}
	payload := make([]byte, 4, 64)
	copy(payload, "WEBP")
	if alpha != nil {
		vp8x := make([]byte, 10)
		vp8x[0] = 0x10
		putUint24(vp8x[4:7], uint32(width-1))
		putUint24(vp8x[7:10], uint32(height-1))
		payload = appendWebPChunk(payload, "VP8X", vp8x)
		payload = appendWebPChunk(payload, alpha.tag, alpha.data)
	}
	payload = appendWebPChunk(payload, bitstream.tag, bitstream.data)
	result := make([]byte, 8, len(payload)+8)
	copy(result, "RIFF")
	binary.LittleEndian.PutUint32(result[4:8], uint32(len(payload)))
	return append(result, payload...), nil
}

func appendWebPChunk(target []byte, tag string, data []byte) []byte {
	target = append(target, tag...)
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(len(data)))
	target = append(target, size[:]...)
	target = append(target, data...)
	if len(data)%2 != 0 {
		target = append(target, 0)
	}
	return target
}

func uint24(data []byte) uint32 {
	return uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16
}

func putUint24(target []byte, value uint32) {
	target[0], target[1], target[2] = byte(value), byte(value>>8), byte(value>>16)
}
