package filestore

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
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
	// Reserve before acquiring a pooled publishing transaction: concurrent
	// variants must not each hold a pool connection while waiting for another
	// connection to persist their intent. The session lease protects the key
	// until publication takes the object-reference lock used by the GC worker.
	reservedKey := "thumbnails/" + uuid.NewString()
	ownership, err := s.reserveObject(ctx, reservedKey, true)
	if err != nil {
		return "", err
	}
	defer ownership.release()
	var key string
	writeResolved := true // No Put has started yet.
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var ready uuid.UUID
		if err := tx.QueryRow(ctx, `select id from pgbase_meta.record_files where id=$1 and status='ready' for update`, file.ID).Scan(&ready); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if err := tx.QueryRow(ctx, `select object_key from pgbase_meta.record_file_variants where file_id=$1 and variant=$2`, file.ID, variant).Scan(&key); err == nil {
			return retireUnusedVariantIntent(ctx, tx, reservedKey)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, file.ID.String()+"/"+variant); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `select object_key from pgbase_meta.record_file_variants where file_id=$1 and variant=$2`, file.ID, variant).Scan(&key); err == nil {
			return retireUnusedVariantIntent(ctx, tx, reservedKey)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := lockObjectReference(ctx, tx, reservedKey); err != nil {
			return err
		}
		var owned bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from pg_locks where locktype='advisory' and pid=$1 and database=(select oid from pg_database where datname=current_database()) and classid=((hashtextextended($2,0) >> 32) & 4294967295)::oid and objid=(hashtextextended($2,0) & 4294967295)::oid and objsubid=1 and mode='ExclusiveLock' and granted)`, int32(ownership.conn.PgConn().PID()), stagedObjectLockName(reservedKey)).Scan(&owned); err != nil {
			return err
		}
		if !owned {
			return ErrStorageConflict
		}
		data, err := s.renderVariant(ctx, file, variant)
		if err != nil {
			return err
		}
		key = reservedKey
		writeResolved = false
		if err := s.objects.Put(ctx, key, bytes.NewReader(data)); err != nil {
			return err
		}
		writeResolved = true // The backend acknowledged completion of this Put.
		if err := s.guardStoragePublication(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into pgbase_meta.record_file_variants(file_id,variant,object_key,created_at) values($1,$2,$3,$4)`, file.ID, variant, key, s.options.Now().UTC()); err != nil {
			return err
		}
		// Commit publishes the reference and retires its intent atomically. A
		// lost COMMIT reply needs no synchronous reference check: the still-live
		// transaction's reference lock fences GC until its actual outcome.
		_, err = tx.Exec(ctx, `delete from pgbase_meta.file_object_deletions where object_key=$1`, key)
		return err
	})
	if err != nil && writeResolved {
		// No write is outstanding. This optimization may time out behind an
		// unknown commit or an exhausted pool; the retained intent is already
		// durable, so returning never loses the only record of object bytes.
		cleanup, cancel := database.BoundedCleanupContext(ctx)
		_, cleanupErr := s.pool.Exec(cleanup, `update pgbase_meta.file_object_deletions set retain_on_success=false,next_attempt_at=CURRENT_TIMESTAMP where object_key=$1`, reservedKey)
		cancel()
		if cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}
	return key, err
}

func retireUnusedVariantIntent(ctx context.Context, tx pgx.Tx, key string) error {
	// The cached-variant path never starts Put for the freshly reserved key.
	if _, err := tx.Exec(ctx, `update pgbase_meta.file_object_deletions set retain_on_success=false where object_key=$1`, key); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `delete from pgbase_meta.file_object_deletions where object_key=$1`, key)
	return err
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
	return s.renderThumbnailData(ctx, data, file.ContentType, variant)
}

// RenderThumbnailData borrows caller-owned bytes read-only for the bounded
// thumbnail pipeline. It does not copy the encoded input into another buffer.
// Without a caller context it waits for decode memory only for the bounded
// lock-holder interval and then reports ErrImageBusy.
func (s *Service) RenderThumbnailData(data []byte, contentType, size string) ([]byte, error) {
	return s.renderThumbnailData(context.Background(), data, contentType, size)
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
	return s.renderThumbnailData(context.Background(), data, contentType, size)
}

// renderThumbnailData waits for decode memory only for the bounded interval:
// variantObject calls it under a record_files row lock, an advisory lock and
// two pooled connections, and extension callers may run inside transactions.
func (s *Service) renderThumbnailData(ctx context.Context, data []byte, contentType, variant string) ([]byte, error) {
	if s == nil || int64(len(data)) > s.options.MaximumBytes || !validVariant(Variant(variant)) || !thumbnailImageType(contentType) {
		return nil, ErrInvalid
	}
	m := variantPattern.FindStringSubmatch(variant)
	if m == nil {
		return nil, ErrInvalid
	}
	requestedW, _ := strconv.Atoi(m[1])
	requestedH, _ := strconv.Atoi(m[2])
	mode := m[3]
	// The reservation covers the output this spec yields for the source size,
	// not the largest output any spec could request.
	var estimated int64
	outputPixels := func(sw, sh int) int64 {
		outW, outH, ok := s.variantOutputSize(sw, sh, requestedW, requestedH, mode)
		if ok {
			estimated = int64(outW) * int64(outH)
		}
		return estimated
	}
	return s.renderDecodedImage(ctx, data, contentType, outputPixels, true, func(source image.Image, format string) ([]byte, error) {
		sw, sh := source.Bounds().Dx(), source.Bounds().Dy()
		outW, outH, ok := s.variantOutputSize(sw, sh, requestedW, requestedH, mode)
		// A decoded size that would render more than was reserved is refused.
		if !ok || int64(outW)*int64(outH) > estimated {
			return nil, ErrInvalid
		}
		xs, ys := variantSampleGrid(sw, sh, outW, outH, mode)
		out := sampleNearest(source, xs, ys)
		var buffer bytes.Buffer
		err := encodeThumbnail(&buffer, out, strings.ToLower(format))
		if err != nil || int64(buffer.Len()) > s.options.MaximumBytes {
			return nil, ErrInvalid
		}
		return buffer.Bytes(), nil
	})
}

// variantOutputSize is the size a requested variant renders for an sw x sh
// source. A zero requested dimension follows the source aspect ratio; mode
// "f" fits inside the requested box. ok is false when the source or the
// derived box is outside the variant and pixel limits.
func (s *Service) variantOutputSize(sw, sh, requestedW, requestedH int, mode string) (outW, outH int, ok bool) {
	if sw < 1 || sh < 1 || int64(sw) > s.options.MaximumImagePixels/int64(sh) {
		return 0, 0, false
	}
	w, h := requestedW, requestedH
	if w == 0 {
		w = max(1, sw*h/sh)
	}
	if h == 0 {
		h = max(1, sh*w/sw)
	}
	if w < 1 || h < 1 || w > 4096 || h > 4096 || int64(w) > s.options.MaximumImagePixels/int64(h) {
		return 0, 0, false
	}
	outW, outH = w, h
	if mode == "f" {
		if sw*h > sh*w {
			outH = max(1, sh*w/sw)
		} else {
			outW = max(1, sw*h/sh)
		}
	}
	return outW, outH, true
}

// RenderThumbnail applies the bounded variant pipeline to an object without
// publishing it as a record-file variant. The caller owns the destination.
func (s *Service) RenderThumbnail(ctx context.Context, objectKey, contentType, size string) ([]byte, error) {
	if s == nil || objectKey == "" || !validVariant(Variant(size)) {
		return nil, ErrInvalid
	}
	return s.renderVariant(ctx, File{ObjectKey: objectKey, ContentType: contentType}, size)
}
