package filestore

import (
	"image"
	"image/color"
)

// thumbnailSampleGrid returns the nearest-neighbour source columns and rows
// buildThumbnail reads for a width x height source scaled to
// newWidth x newHeight. Coordinates are relative to the source bounds.
func thumbnailSampleGrid(width, height, newWidth, newHeight int) (xs, ys []int) {
	xs = make([]int, newWidth)
	for x := range xs {
		xs[x] = x * width / newWidth
	}
	ys = make([]int, newHeight)
	for y := range ys {
		ys[y] = y * height / newHeight
	}
	return xs, ys
}

// variantSampleGrid returns the nearest-neighbour source columns and rows a
// variant of mode reads for an sw x sh source rendered at outW x outH. A fit
// ("f") variant scales the whole source; any other mode crops the excess of
// the longer axis, centred except for "t" (top) and "b" (bottom) on a
// vertical crop. Coordinates are relative to the source bounds.
func variantSampleGrid(sw, sh, outW, outH int, mode string) (xs, ys []int) {
	xs = make([]int, outW)
	for x := range xs {
		xs[x] = x * sw / outW
	}
	ys = make([]int, outH)
	for y := range ys {
		ys[y] = y * sh / outH
	}
	if mode == "f" || sw*outH == sh*outW {
		return xs, ys
	}
	if sw*outH > sh*outW {
		cropW := sh * outW / outH
		offset := (sw - cropW) / 2
		for x := range xs {
			xs[x] = offset + x*cropW/outW
		}
		return xs, ys
	}
	cropH := sw * outH / outW
	offset := (sh - cropH) / 2
	if mode == "t" {
		offset = 0
	} else if mode == "b" {
		offset = sh - cropH
	}
	for y := range ys {
		ys[y] = offset + y*cropH/outH
	}
	return xs, ys
}

// sampleNearest builds a len(xs) x len(ys) NRGBA image whose pixel (x, y) is
// exactly what out.Set(x, y, source.At(min.X+xs[x], min.Y+ys[y])) stores, i.e.
// color.NRGBAModel applied to the source colour.
//
// The decoders' concrete types are read through Pix directly so a 4096x4096
// variant does not cost tens of millions of interface calls and colour
// allocations; each fast path reproduces the colour conversion of the generic
// path bit for bit. Any other image type, and any grid reaching outside the
// source bounds (a degenerate crop such as a 1x1 source widened with "b"
// reads one row past the end, where At answers the zero colour), takes the
// generic path.
func sampleNearest(source image.Image, xs, ys []int) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, len(xs), len(ys)))
	if len(xs) == 0 || len(ys) == 0 {
		return out
	}
	bounds := source.Bounds()
	if !offsetsWithin(xs, bounds.Dx()) || !offsetsWithin(ys, bounds.Dy()) {
		sampleNearestGeneric(out, source, xs, ys)
		return out
	}
	switch src := source.(type) {
	case *image.NRGBA:
		columns := scaledOffsets(xs, 4)
		for y, sy := range ys {
			row := src.Pix[sy*src.Stride:]
			dst := out.Pix[y*out.Stride:]
			for x, column := range columns {
				copy(dst[x*4:x*4+4], row[column:column+4])
			}
		}
	case *image.RGBA:
		columns := scaledOffsets(xs, 4)
		for y, sy := range ys {
			row := src.Pix[sy*src.Stride:]
			dst := out.Pix[y*out.Stride:]
			for x, column := range columns {
				s := row[column : column+4]
				r, g, b, a := uint32(s[0]), uint32(s[1]), uint32(s[2]), uint32(s[3])
				// color.RGBA.RGBA widens each channel by v | v<<8.
				storeNRGBA(dst[x*4:x*4+4], r|r<<8, g|g<<8, b|b<<8, a|a<<8)
			}
		}
	case *image.Gray:
		for y, sy := range ys {
			row := src.Pix[sy*src.Stride:]
			dst := out.Pix[y*out.Stride:]
			for x, sx := range xs {
				v := row[sx]
				d := dst[x*4 : x*4+4]
				d[0], d[1], d[2], d[3] = v, v, v, 0xff
			}
		}
	case *image.YCbCr:
		yColumns, cColumns := make([]int, len(xs)), make([]int, len(xs))
		for x, sx := range xs {
			yColumns[x] = src.YOffset(bounds.Min.X+sx, bounds.Min.Y)
			cColumns[x] = src.COffset(bounds.Min.X+sx, bounds.Min.Y)
		}
		for y, sy := range ys {
			// Every subsample ratio's offsets are a row term plus a column
			// term, both zero at the bounds' minimum point.
			yRow := src.YOffset(bounds.Min.X, bounds.Min.Y+sy)
			cRow := src.COffset(bounds.Min.X, bounds.Min.Y+sy)
			dst := out.Pix[y*out.Stride:]
			for x := range xs {
				ci := cRow + cColumns[x]
				r, g, b, _ := color.YCbCr{Y: src.Y[yRow+yColumns[x]], Cb: src.Cb[ci], Cr: src.Cr[ci]}.RGBA()
				d := dst[x*4 : x*4+4]
				d[0], d[1], d[2], d[3] = uint8(r>>8), uint8(g>>8), uint8(b>>8), 0xff
			}
		}
	case *image.NYCbCrA:
		yColumns, cColumns, aColumns := make([]int, len(xs)), make([]int, len(xs)), make([]int, len(xs))
		for x, sx := range xs {
			yColumns[x] = src.YOffset(bounds.Min.X+sx, bounds.Min.Y)
			cColumns[x] = src.COffset(bounds.Min.X+sx, bounds.Min.Y)
			aColumns[x] = src.AOffset(bounds.Min.X+sx, bounds.Min.Y)
		}
		for y, sy := range ys {
			yRow := src.YOffset(bounds.Min.X, bounds.Min.Y+sy)
			cRow := src.COffset(bounds.Min.X, bounds.Min.Y+sy)
			aRow := src.AOffset(bounds.Min.X, bounds.Min.Y+sy)
			dst := out.Pix[y*out.Stride:]
			for x := range xs {
				ci := cRow + cColumns[x]
				r, g, b, a := color.NYCbCrA{
					YCbCr: color.YCbCr{Y: src.Y[yRow+yColumns[x]], Cb: src.Cb[ci], Cr: src.Cr[ci]},
					A:     src.A[aRow+aColumns[x]],
				}.RGBA()
				storeNRGBA(dst[x*4:x*4+4], r, g, b, a)
			}
		}
	case *image.Paletted:
		palette, ok := nrgbaPalette(src.Palette)
		if !ok {
			sampleNearestGeneric(out, source, xs, ys)
			return out
		}
		for y, sy := range ys {
			row := src.Pix[sy*src.Stride:]
			dst := out.Pix[y*out.Stride:]
			for x, sx := range xs {
				index := int(row[sx])
				if index >= len(palette) {
					// Leave the generic path's behaviour for an index the
					// palette does not cover exactly as it was.
					out.Set(x, y, source.At(src.Rect.Min.X+sx, src.Rect.Min.Y+sy))
					continue
				}
				c := palette[index]
				d := dst[x*4 : x*4+4]
				d[0], d[1], d[2], d[3] = c.R, c.G, c.B, c.A
			}
		}
	default:
		sampleNearestGeneric(out, source, xs, ys)
	}
	return out
}

// sampleNearestGeneric is the interface-based path every image type
// supports; the fast paths in sampleNearest must match it exactly.
func sampleNearestGeneric(out *image.NRGBA, source image.Image, xs, ys []int) {
	bounds := source.Bounds()
	for y, sy := range ys {
		for x, sx := range xs {
			out.Set(x, y, source.At(bounds.Min.X+sx, bounds.Min.Y+sy))
		}
	}
}

func offsetsWithin(offsets []int, limit int) bool {
	for _, offset := range offsets {
		if offset < 0 || offset >= limit {
			return false
		}
	}
	return true
}

func scaledOffsets(xs []int, scale int) []int {
	offsets := make([]int, len(xs))
	for i, x := range xs {
		offsets[i] = x * scale
	}
	return offsets
}

// storeNRGBA writes the alpha-premultiplied 16-bit colour r, g, b, a as
// color.NRGBAModel converts it.
func storeNRGBA(d []byte, r, g, b, a uint32) {
	switch a {
	case 0xffff:
		d[0], d[1], d[2], d[3] = uint8(r>>8), uint8(g>>8), uint8(b>>8), 0xff
	case 0:
		d[0], d[1], d[2], d[3] = 0, 0, 0, 0
	default:
		d[0] = uint8(((r * 0xffff) / a) >> 8)
		d[1] = uint8(((g * 0xffff) / a) >> 8)
		d[2] = uint8(((b * 0xffff) / a) >> 8)
		d[3] = uint8(a >> 8)
	}
}

// nrgbaPalette converts a palette once. A nil entry has no conversion, so a
// palette holding one is left to the generic path.
func nrgbaPalette(palette color.Palette) ([]color.NRGBA, bool) {
	if len(palette) == 0 {
		return nil, false
	}
	converted := make([]color.NRGBA, len(palette))
	for i, c := range palette {
		if c == nil {
			return nil, false
		}
		converted[i] = color.NRGBAModel.Convert(c).(color.NRGBA)
	}
	return converted, true
}
