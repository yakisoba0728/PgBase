package backup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLocalRepositoryPublishPreservesExistingBackup(t *testing.T) {
	directory := t.TempDir()
	repository := NewLocalRepository(directory)
	entry := Entry{ID: uuid.NewString(), CreatedAt: time.Now().UTC(), Size: 8}
	if err := repository.Publish(context.Background(), entry, strings.NewReader("original")); err != nil {
		t.Fatal(err)
	}
	replacement := entry
	replacement.Size = 11
	if err := repository.Publish(context.Background(), replacement, strings.NewReader("replacement")); err == nil {
		t.Fatal("immutable backup was replaced")
	}
	reader, err := repository.Open(context.Background(), entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err != nil || closeErr != nil || string(contents) != "original" {
		t.Fatalf("archive=%q read=%v close=%v", contents, err, closeErr)
	}
	if got, err := repository.Get(context.Background(), entry.ID); err != nil || got.Size != entry.Size {
		t.Fatalf("receipt=%#v err=%v", got, err)
	}
	if err := os.Remove(repository.metadata(entry.ID)); err != nil {
		t.Fatal(err)
	}
	if err := repository.Publish(context.Background(), replacement, strings.NewReader("replacement")); err == nil {
		t.Fatal("orphaned archive was replaced")
	}
	if raw, err := os.ReadFile(repository.archive(entry.ID)); err != nil || string(raw) != "original" {
		t.Fatalf("orphaned archive=%q err=%v", raw, err)
	}
}

func TestLocalRepositoryPublishPreservesOrphanedReceipt(t *testing.T) {
	directory := t.TempDir()
	repository := NewLocalRepository(directory)
	id := uuid.NewString()
	path := filepath.Join(directory, id+".json")
	if err := os.WriteFile(path, []byte("existing receipt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := repository.Publish(context.Background(), Entry{ID: id}, strings.NewReader("archive")); err == nil {
		t.Fatal("orphaned receipt was replaced")
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "existing receipt" {
		t.Fatalf("receipt=%q err=%v", raw, err)
	}
	if _, err := os.Stat(repository.archive(id)); !os.IsNotExist(err) {
		t.Fatalf("archive published despite existing receipt: %v", err)
	}
}

type cancelingArchiveReader struct{ cancel context.CancelFunc }

func (r cancelingArchiveReader) Read(buffer []byte) (int, error) {
	r.cancel()
	return copy(buffer, "partial"), io.EOF
}

func TestLocalRepositoryPublishStopsOnCancellation(t *testing.T) {
	repository := NewLocalRepository(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := uuid.NewString()
	if err := repository.Publish(ctx, Entry{ID: id}, cancelingArchiveReader{cancel}); !errors.Is(err, context.Canceled) {
		t.Fatalf("publish after cancellation=%v", err)
	}
	if _, err := repository.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canceled archive was published: %v", err)
	}
}

func TestLocalRepositoryRejectsInvalidPublicationAndDiscardIDs(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "repository")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	repository := NewLocalRepository(directory)
	path := filepath.Join(root, "outside.tar.gz")
	if err := os.WriteFile(path, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := repository.Publish(context.Background(), Entry{ID: "../outside"}, strings.NewReader("replacement")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid publication=%v", err)
	}
	if err := repository.Discard(context.Background(), "../outside"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid discard=%v", err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "untouched" {
		t.Fatalf("outside archive=%q err=%v", raw, err)
	}
}
