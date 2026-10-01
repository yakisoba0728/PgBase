package filestore

import (
	"encoding/binary"
	"image"
)

const maximumEXIFEntries = 1024

// jpegEXIFOrientation returns the TIFF orientation from the first valid EXIF
// APP1 segment. It never follows offsets outside that segment and bounds IFD
// traversal independently of the uploaded file-size limit.
func jpegEXIFOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}
	for offset := 2; offset+4 <= len(data); {
		if data[offset] != 0xff {
			return 1
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			return 1
		}
		marker := data[offset]
		offset++
		if marker == 0xda || marker == 0xd9 {
			return 1
		}
		if marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if offset+2 > len(data) {
			return 1
		}
		length := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if length < 2 || length > len(data)-offset {
			return 1
		}
		segment := data[offset+2 : offset+length]
		if marker == 0xe1 && len(segment) >= 6 && string(segment[:6]) == "Exif\x00\x00" {
			if orientation := tiffOrientation(segment[6:]); orientation != 1 {
				return orientation
			}
		}
		offset += length
	}
	return 1
}

func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	ifdOffset := uint64(order.Uint32(tiff[4:8]))
	if ifdOffset+2 > uint64(len(tiff)) {
		return 1
	}
	entries := uint64(order.Uint16(tiff[ifdOffset : ifdOffset+2]))
	if entries > maximumEXIFEntries || ifdOffset+2+entries*12 > uint64(len(tiff)) {
		return 1
	}
	for i := uint64(0); i < entries; i++ {
		entry := tiff[ifdOffset+2+i*12 : ifdOffset+2+(i+1)*12]
		if order.Uint16(entry[:2]) != 0x0112 || order.Uint16(entry[2:4]) != 3 || order.Uint32(entry[4:8]) != 1 {
			continue
		}
		value := int(order.Uint16(entry[8:10]))
		if value >= 2 && value <= 8 {
			return value
		}
		return 1
	}
	return 1
}

func orientImage(source image.Image, orientation int) image.Image {
	if orientation < 2 || orientation > 8 {
		return source
	}
	bounds := source.Bounds()
	sw, sh := bounds.Dx(), bounds.Dy()
	dw, dh := sw, sh
	if orientation >= 5 {
		dw, dh = sh, sw
	}
	out := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			var sx, sy int
			switch orientation {
			case 2:
				sx, sy = sw-1-x, y
			case 3:
				sx, sy = sw-1-x, sh-1-y
			case 4:
				sx, sy = x, sh-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, sh-1-x
			case 7:
				sx, sy = sw-1-y, sh-1-x
			case 8:
				sx, sy = sw-1-y, x
			}
			out.Set(x, y, source.At(bounds.Min.X+sx, bounds.Min.Y+sy))
		}
	}
	return out
}
