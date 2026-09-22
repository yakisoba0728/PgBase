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
	config, format, err := decodeImageConfig(bytes.NewReader(data), file.ContentType)
	if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width) > s.options.MaximumImagePixels/int64(config.Height) || !matchesImageFormat(file.ContentType, format) {
		return nil, ErrInvalid
	}
	source, format, err := decodeImage(bytes.NewReader(data), file.ContentType, s.options.MaximumImagePixels)
	if err != nil {
		return nil, ErrInvalid
	}
	if format == "jpeg" {
		source = orientImage(source, jpegEXIFOrientation(data))
	}
	width, height, maximum := source.Bounds().Dx(), source.Bounds().Dy(), s.options.ThumbnailDimension
	if width <= maximum && height <= maximum {
		maximum = max(width, height)
	}
	newWidth, newHeight := maximum, max(1, height*maximum/width)
	if height > width {
		newWidth, newHeight = max(1, width*maximum/height), maximum
	}
	thumbnail := image.NewNRGBA(image.Rect(0, 0, newWidth, newHeight))
	for y := range newHeight {
		for x := range newWidth {
			thumbnail.Set(x, y, source.At(source.Bounds().Min.X+x*width/newWidth, source.Bounds().Min.Y+y*height/newHeight))
		}
	}
	var output bytes.Buffer
	err = encodeThumbnail(&output, thumbnail, format)
	if err != nil || int64(output.Len()) > s.options.MaximumBytes {
		return nil, ErrInvalid
	}
	return output.Bytes(), nil
}

func thumbnailImageType(contentType string) bool {
	return contentType == "image/jpeg" || contentType == "image/png" || contentType == "image/gif" || contentType == "image/webp"
}

// decodeImageConfig and decodeImage select decoders by the already validated
// declared type. image.Decode alone does not register WebP, and accepting a
// mismatched decoder would let a claimed image type choose an unintended output
// format.
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

func decodeImage(r io.Reader, contentType string, maximumImagePixels int64) (image.Image, string, error) {
	switch contentType {
	case "image/jpeg", "image/png":
		return image.Decode(r)
	case "image/gif":
		decoded, err := gif.Decode(r)
		return decoded, "gif", err
	case "image/webp":
		data, err := io.ReadAll(r)
		if err != nil {
			return nil, "webp", err
		}
		decoded, err := webp.Decode(bytes.NewReader(data))
		if err == nil {
			return decoded, "webp", nil
		}
		decoded, err = decodeAnimatedWebPFirstFrame(data, maximumImagePixels)
		return decoded, "webp", err
	default:
		return nil, "", ErrInvalid
	}
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
