package filestore

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"
)

func (s *Service) buildThumbnail(ctx context.Context, file File) ([]byte, error) {
	if file.ContentType != "image/jpeg" && file.ContentType != "image/png" {
		return nil, nil
	}
	reader, err := s.objects.Open(ctx, file.ObjectKey)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, s.options.MaximumBytes+1))
	if err != nil || int64(len(data)) > s.options.MaximumBytes {
		return nil, ErrInvalid
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width) > s.options.MaximumImagePixels/int64(config.Height) || (file.ContentType == "image/jpeg" && format != "jpeg") || (file.ContentType == "image/png" && format != "png") {
		return nil, ErrInvalid
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalid
	}
	width, height, maximum := source.Bounds().Dx(), source.Bounds().Dy(), s.options.ThumbnailDimension
	if width <= maximum && height <= maximum {
		maximum = max(width, height)
	}
	newWidth, newHeight := maximum, max(1, height*maximum/width)
	if height > width {
		newWidth, newHeight = max(1, width*maximum/height), maximum
	}
	thumbnail := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	for y := range newHeight {
		for x := range newWidth {
			thumbnail.Set(x, y, source.At(source.Bounds().Min.X+x*width/newWidth, source.Bounds().Min.Y+y*height/newHeight))
		}
	}
	var output bytes.Buffer
	if file.ContentType == "image/png" {
		err = png.Encode(&output, thumbnail)
	} else {
		err = jpeg.Encode(&output, thumbnail, &jpeg.Options{Quality: 82})
	}
	if err != nil || int64(output.Len()) > s.options.MaximumBytes {
		return nil, ErrInvalid
	}
	return output.Bytes(), nil
}

func (s *Service) validateStoredType(ctx context.Context, file File) error {
	reader, err := s.objects.Open(ctx, file.ObjectKey)
	if err != nil {
		return err
	}
	defer reader.Close()
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
