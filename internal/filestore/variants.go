package filestore

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var variantPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,3})x(0|[1-9][0-9]{0,3})([tbf]?)$`)

func validVariant(value Variant) bool {
	return value == Original || value == Thumbnail || value != "0x0" && variantPattern.MatchString(string(value))
}
func containsVariant(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func (s *Service) variantObject(ctx context.Context, file File, variant string) (string, error) {
	ctx, release, err := s.beginStorageOperation(ctx)
	if err != nil {
		return "", err
	}
	defer release()
	var key string
	var uploadedKey string
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `select object_key from pgbase_meta.record_file_variants where file_id=$1 and variant=$2`, file.ID, variant).Scan(&key); err == nil {
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, file.ID.String()+"/"+variant); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `select object_key from pgbase_meta.record_file_variants where file_id=$1 and variant=$2`, file.ID, variant).Scan(&key); err == nil {
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		data, err := s.renderVariant(ctx, file, variant)
		if err != nil {
			return err
		}
		key = "thumbnails/" + uuid.NewString()
		if err := s.objects.Put(ctx, key, bytes.NewReader(data)); err != nil {
			return err
		}
		uploadedKey = key
		if err := s.guardStoragePublication(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into pgbase_meta.record_file_variants(file_id,variant,object_key,created_at) values($1,$2,$3,$4)`, file.ID, variant, key, s.options.Now().UTC()); err != nil {
			return err
		}
		return nil
	})
	if err != nil && uploadedKey != "" {
		// The transaction may be aborted or its commit result may be unknown.
		// Queue cleanup in a fresh transaction and guard every metadata table so
		// a variant that did commit is never physically deleted.
		err = errors.Join(err, s.queueUnreferencedVariant(context.WithoutCancel(ctx), file.ID, variant, uploadedKey))
	}
	return key, err
}

func (s *Service) queueUnreferencedVariant(ctx context.Context, fileID uuid.UUID, variant, key string) error {
	return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// Serialize with the publishing transaction. In a commit-unknown case a
		// fresh snapshot could otherwise miss a commit that is still in flight
		// and queue a live object for deletion.
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, fileID.String()+"/"+variant); err != nil {
			return err
		}
		var referenced bool
		if err := tx.QueryRow(ctx, `select exists(
			select 1 from pgbase_meta.record_files where object_key=$1 or thumbnail_object_key=$1
			union all
			select 1 from pgbase_meta.record_file_variants where object_key=$1
		)`, key).Scan(&referenced); err != nil {
			return err
		}
		if referenced {
			return nil
		}
		return enqueueObjects(ctx, tx, key)
	})
}

func (s *Service) renderVariant(ctx context.Context, file File, variant string) (result []byte, resultErr error) {
	if !thumbnailImageType(file.ContentType) {
		return nil, ErrInvalid
	}
	reader, err := s.objects.Open(ctx, file.ObjectKey)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, reader.Close()) }()
	data, err := io.ReadAll(io.LimitReader(reader, s.options.MaximumBytes+1))
	if err != nil || int64(len(data)) > s.options.MaximumBytes {
		return nil, ErrInvalid
	}
	return s.renderThumbnailData(data, file.ContentType, variant)
}

// RenderThumbnailData applies the bounded thumbnail pipeline to caller-owned
// bytes. It is used by rooted extension files, whose paths are deliberately
// not valid object-store keys.
func (s *Service) RenderThumbnailData(data []byte, contentType, size string) ([]byte, error) {
	return s.RenderThumbnailReader(bytes.NewReader(data), contentType, size)
}

// RenderThumbnailReader applies the bounded thumbnail pipeline to a reader
// without requiring it to be backed by the managed object store.
func (s *Service) RenderThumbnailReader(reader io.Reader, contentType, size string) ([]byte, error) {
	if s == nil || reader == nil || !validVariant(Variant(size)) {
		return nil, ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(reader, s.options.MaximumBytes+1))
	if err != nil || int64(len(data)) > s.options.MaximumBytes {
		return nil, ErrInvalid
	}
	return s.renderThumbnailData(data, contentType, size)
}

func (s *Service) renderThumbnailData(data []byte, contentType, variant string) ([]byte, error) {
	if !thumbnailImageType(contentType) {
		return nil, ErrInvalid
	}
	config, format, err := decodeImageConfig(bytes.NewReader(data), contentType)
	if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width) > s.options.MaximumImagePixels/int64(config.Height) || !matchesImageFormat(contentType, format) {
		return nil, ErrInvalid
	}
	source, format, err := decodeImage(bytes.NewReader(data), contentType)
	if err != nil {
		return nil, ErrInvalid
	}
	m := variantPattern.FindStringSubmatch(variant)
	if m == nil {
		return nil, ErrInvalid
	}
	w, _ := strconv.Atoi(m[1])
	h, _ := strconv.Atoi(m[2])
	mode := m[3]
	sw, sh := source.Bounds().Dx(), source.Bounds().Dy()
	if sw < 1 || sh < 1 || int64(sw) > s.options.MaximumImagePixels/int64(sh) {
		return nil, ErrInvalid
	}
	if w == 0 {
		w = max(1, sw*h/sh)
	}
	if h == 0 {
		h = max(1, sh*w/sw)
	}
	if w < 1 || h < 1 || w > 4096 || h > 4096 || int64(w) > s.options.MaximumImagePixels/int64(h) {
		return nil, ErrInvalid
	}
	outW, outH := w, h
	if mode == "f" {
		if sw*h > sh*w {
			outH = max(1, sh*w/sw)
		} else {
			outW = max(1, sw*h/sh)
		}
	}
	out := image.NewRGBA(image.Rect(0, 0, outW, outH))
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
	var buffer bytes.Buffer
	if strings.EqualFold(format, "png") {
		err = png.Encode(&buffer, out)
	} else {
		err = jpeg.Encode(&buffer, out, &jpeg.Options{Quality: 82})
	}
	if err != nil || int64(buffer.Len()) > s.options.MaximumBytes {
		return nil, ErrInvalid
	}
	return buffer.Bytes(), nil
}

// RenderThumbnail applies the bounded variant pipeline to an object without
// publishing it as a record-file variant. The caller owns the destination.
func (s *Service) RenderThumbnail(ctx context.Context, objectKey, contentType, size string) ([]byte, error) {
	if s == nil || objectKey == "" || !validVariant(Variant(size)) {
		return nil, ErrInvalid
	}
	return s.renderVariant(ctx, File{ObjectKey: objectKey, ContentType: contentType}, size)
}
