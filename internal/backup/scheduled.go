package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

type scheduledArchiveRepository interface {
	openUnpublishedArchive(context.Context, string) (io.ReadCloser, error)
	publishArchiveEntry(context.Context, Entry) error
}

func (m *Manager) resumeScheduledPublication(ctx context.Context, id, directory string) (bool, error) {
	if controller, ok := m.options.Repository.(*StorageController); ok {
		return controller.resumeScheduledPublication(ctx, id, directory)
	}
	return resumeScheduledPublication(ctx, m.options.Repository, id, directory)
}

func (c *StorageController) resumeScheduledPublication(ctx context.Context, id, directory string) (bool, error) {
	ctx, repository, conn, release, err := c.lease(ctx, id)
	if err != nil {
		return false, err
	}
	defer release()
	published, err := resumeScheduledPublication(ctx, repository, id, directory)
	if err != nil || !published {
		return false, err
	}
	_, err = conn.Exec(ctx, `update pgbase_meta.backup_storage_operations set status='committed',updated_at=now()
 where backup_id=$1 and status='pending' and settings_revision=(select revision from pgbase_meta.backup_storage_settings where singleton)`, id)
	return err == nil, err
}

func resumeScheduledPublication(ctx context.Context, repository Repository, id, directory string) (bool, error) {
	_, err := repository.Get(ctx, id)
	if err == nil {
		archive, err := repository.Open(ctx, id)
		if err != nil {
			return false, err
		}
		err = archive.Close()
		return err == nil, err
	}
	if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	recoverable, ok := repository.(scheduledArchiveRepository)
	if !ok {
		return false, nil
	}
	archive, err := recoverable.openUnpublishedArchive(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Inspect the completed immutable archive before writing its missing
	// receipt. An interrupted or failed metadata write cannot erase it.
	digest := sha256.New()
	limited := &io.LimitedReader{R: archive, N: MaximumArchiveBytes + 1}
	err = errors.Join(Unpack(ctx, io.TeeReader(limited, digest), directory), archive.Close())
	if err != nil {
		return false, err
	}
	manifest, _, _, err := readBackupLayout(ctx, directory)
	if err != nil {
		return false, err
	}
	entry := Entry{ID: id, CreatedAt: manifest.CreatedAt, Size: MaximumArchiveBytes + 1 - limited.N,
		PostgreSQLMajor: manifest.PostgreSQLMajor, SHA256: hex.EncodeToString(digest.Sum(nil)), StorageFormat: s3StorageFormat}
	err = recoverable.publishArchiveEntry(ctx, entry)
	return err == nil, err
}
