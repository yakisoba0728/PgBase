package filestore

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"

	"golang.org/x/image/webp"
)

func (s *Service) buildThumbnail(ctx context.Context, file File) ([]byte, error) {
	if !thumbnailImageType(file.ContentType) {
		return nil, nil
	}
	reader, err := s.objects.Open(ctx, file.ObjectKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, s.options.MaximumBytes+1))
	if err != nil || int64(len(data)) > s.options.MaximumBytes {
		return nil, ErrInvalid
	}
	// The upload holds a storage slot but no transaction or row lock here, so
	// it may queue for decode memory until its own context ends.
	dimension := s.options.ThumbnailDimension
	outputPixels := func(width, height int) int64 {
		newWidth, newHeight := thumbnailOutputSize(width, height, dimension)
		return int64(newWidth) * int64(newHeight)
	}
	return s.renderDecodedImage(ctx, data, file.ContentType, outputPixels, false, func(source image.Image, format string) ([]byte, error) {
		width, height := source.Bounds().Dx(), source.Bounds().Dy()
		newWidth, newHeight := thumbnailOutputSize(width, height, dimension)
		xs, ys := thumbnailSampleGrid(width, height, newWidth, newHeight)
		thumbnail := sampleNearest(source, xs, ys)
		var output bytes.Buffer
		err := encodeThumbnail(&output, thumbnail, format)
		if err != nil || int64(output.Len()) > s.options.MaximumBytes {
			return nil, ErrInvalid
		}
		return output.Bytes(), nil
	})
}

// thumbnailOutputSize is the upload thumbnail size for a width x height
// source: the longer side scaled to maximum, never enlarged.
func thumbnailOutputSize(width, height, maximum int) (int, int) {
	if width <= maximum && height <= maximum {
		maximum = max(width, height)
	}
	if height > width {
		return max(1, width*maximum/height), maximum
	}
	return maximum, max(1, height*maximum/width)
}

// renderDecodedImage is the only path that decodes stored image bytes. It
// reserves the estimated decode memory (see estimateImageMemory) before
// decoding and holds it until render has returned its encoded bytes, after
// which no reference to the decoded, oriented or resampled pixels remains.
// outputPixels returns, for the source size render will see (after EXIF
// orientation), how many pixels render allocates for its result. An image
// whose estimate exceeds the whole budget is not refused: it reserves the
// whole budget and renders alone (the pixel limit still bounds its memory).
// bounded callers may hold database locks and wait at most
// lockedImageBudgetWait.
func (s *Service) renderDecodedImage(ctx context.Context, data []byte, contentType string, outputPixels func(width, height int) int64, bounded bool, render func(source image.Image, format string) ([]byte, error)) ([]byte, error) {
	config, format, err := decodeImageConfig(bytes.NewReader(data), contentType)
	if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width) > s.options.MaximumImagePixels/int64(config.Height) || !matchesImageFormat(contentType, format) {
		return nil, ErrInvalid
	}
	width, height := config.Width, config.Height
	if format == "jpeg" {
		if orientation := jpegEXIFOrientation(data); orientation >= 5 && orientation <= 8 {
			width, height = height, width
		}
	}
	release, err := s.acquireImageMemory(ctx, estimateImageMemory(data, format, config, outputPixels(width, height)), bounded)
	if err != nil {
		return nil, err
	}
	defer release()
	source, format, err := decodeImage(data, contentType, s.options.MaximumImagePixels)
	if err != nil {
		return nil, ErrInvalid
	}
	if format == "jpeg" {
		source = orientImage(source, jpegEXIFOrientation(data))
	}
	return render(source, format)
}

func thumbnailImageType(contentType string) bool {
	return contentType == "image/jpeg" || contentType == "image/png" || contentType == "image/gif" || contentType == "image/webp"
}

func decodeImageConfig(r io.Reader, contentType string) (image.Config, string, error) {
	switch contentType {
	case "image/jpeg", "image/png":
		return image.DecodeConfig(r)
	case "image/gif":
		config, err := gif.DecodeConfig(r)
		return config, "gif", err
	case "image/webp":
		config, err := webp.DecodeConfig(r)
		return config, "webp", err
	default:
		return image.Config{}, "", ErrInvalid
	}
}

func encodeThumbnail(output io.Writer, thumbnail image.Image, sourceFormat string) error {
	switch sourceFormat {
	case "png", "gif", "webp":
		return png.Encode(output, thumbnail)
	case "jpeg":
		return jpeg.Encode(output, thumbnail, &jpeg.Options{Quality: 82})
	default:
		return ErrInvalid
	}
}

// decodeImage owns the format and pixel-budget checks for every thumbnail
// entry point. Callers bound encoded byte length before entering this function.
// The input is borrowed read-only: WebP fallback can inspect the original RIFF
// bytes without allocating another full-size io.ReadAll buffer.
func decodeImage(data []byte, contentType string, maximumImagePixels int64) (image.Image, string, error) {
	config, format, err := decodeImageConfig(bytes.NewReader(data), contentType)
	if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width) > maximumImagePixels/int64(config.Height) || !matchesImageFormat(contentType, format) {
		return nil, format, ErrInvalid
	}
	reader := bytes.NewReader(data)
	var decoded image.Image
	switch contentType {
	case "image/jpeg", "image/png":
		decoded, format, err = image.Decode(reader)
	case "image/gif":
		decoded, err = gif.Decode(reader)
	case "image/webp":
		decoded, err = webp.Decode(reader)
		if err != nil {
			decoded, err = decodeAnimatedWebPFirstFrame(data, maximumImagePixels)
		}
	default:
		return nil, "", ErrInvalid
	}
	if err != nil || decoded == nil || !matchesImageFormat(contentType, format) {
		return nil, format, ErrInvalid
	}
	bounds := decoded.Bounds()
	if bounds.Dx() < 1 || bounds.Dy() < 1 || int64(bounds.Dx()) > maximumImagePixels/int64(bounds.Dy()) {
		return nil, format, ErrInvalid
	}
	return decoded, format, nil
}

func matchesImageFormat(contentType, format string) bool {
	return (contentType == "image/jpeg" && format == "jpeg") ||
		(contentType == "image/png" && format == "png") ||
		(contentType == "image/gif" && format == "gif") ||
		(contentType == "image/webp" && format == "webp")
}

func (s *Service) validateStoredType(ctx context.Context, file File) error {
	reader, err := s.objects.Open(ctx, file.ObjectKey)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	buffer := make([]byte, 512)
	n, err := io.ReadFull(reader, buffer)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return err
	}
	if strings.TrimSpace(strings.Split(http.DetectContentType(buffer[:n]), ";")[0]) != file.ContentType {
		return ErrInvalid
	}
	return nil
}
