package filestore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

func TestLocalStoreRejectsTraversalAndPublishesAtomically(t *testing.T) {
	root := t.TempDir()
	store, err := NewLocalStore(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"../secret", "objects/../../secret", "/objects/" + uuid.NewString(), "objects/not-a-uuid"} {
		if err := store.Put(context.Background(), key, bytes.NewBufferString("secret")); !errors.Is(err, ErrInvalid) {
			t.Errorf("Put(%q) = %v, want ErrInvalid", key, err)
		}
	}

	key := "objects/" + uuid.NewString()
	if err := store.Put(context.Background(), key, bytes.NewBufferString("complete object")); err != nil {
		t.Fatal(err)
	}
	reader, err := store.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(data) != "complete object" {
		t.Fatalf("stored object = %q, %v", data, err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	failedKey := "objects/" + uuid.NewString()
	if err := store.Put(canceled, failedKey, bytes.NewBufferString("partial")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Put = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(failedKey))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled upload left published object: %v", err)
	}
	entries, err := filepath.Glob(filepath.Join(root, "objects", ".upload-*"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary uploads = %v, %v", entries, err)
	}

	if err := store.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), key); err != nil {
		t.Fatalf("idempotent Delete = %v", err)
	}
}

func TestCountingReaderStopsAtMaximumWithoutBuffering(t *testing.T) {
	reader := &countingReader{reader: bytes.NewReader(bytes.Repeat([]byte("x"), 1024)), maximum: 31}
	read, err := io.ReadAll(reader)
	if !errors.Is(err, ErrTooLarge) || len(read) != 32 || reader.count != 32 {
		t.Fatalf("bounded read bytes=%d count=%d err=%v", len(read), reader.count, err)
	}
}

func TestDownloadTokenIsBoundToCurrentUserFileVariantAndExpiry(t *testing.T) {
	now := int64(1_800_000_000)
	service := &Service{pool: nil, authorize: nil, options: Options{
		TokenSecret: bytes.Repeat([]byte{0x42}, 32),
		TokenTTL:    2 * time.Second,
		Now:         func() time.Time { return time.Unix(now, 0) },
	}}
	user := identity.Subject{Kind: identity.User, ID: uuid.New()}
	fileID := uuid.New()
	// Token encoding is tested below independently from the database-backed
	// authorization gate used by MintDownloadToken.
	token := service.signDownloadToken(user, fileID, Original)
	if !service.verifyToken(token, user, fileID, Original, time.Unix(now+2, 0)) {
		t.Fatal("token rejected at its expiry boundary")
	}
	for _, changed := range []struct {
		user    identity.Subject
		file    uuid.UUID
		variant Variant
		at      time.Time
	}{
		{identity.Subject{Kind: identity.User, ID: uuid.New()}, fileID, Original, time.Unix(now, 0)},
		{user, uuid.New(), Original, time.Unix(now, 0)},
		{user, fileID, Thumbnail, time.Unix(now, 0)},
		{user, fileID, Original, time.Unix(now+3, 0)},
	} {
		if service.verifyToken(token, changed.user, changed.file, changed.variant, changed.at) {
			t.Fatal("token accepted outside its user/file/variant/expiry binding")
		}
	}
}
