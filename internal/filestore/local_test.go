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

func TestLocalStoreStreamsResolvedByteRange(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "objects/" + uuid.NewString()
	if err := store.Put(context.Background(), key, bytes.NewBufferString("0123456789")); err != nil {
		t.Fatal(err)
	}
	for _, requested := range []ByteRange{{Start: 2, End: 5}, {Start: 7, End: -1}, {Start: -3, End: -1}} {
		result, err := store.OpenRange(context.Background(), key, requested)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(result.Body)
		_ = result.Body.Close()
		if readErr != nil || string(body) != "2345" && string(body) != "789" || result.Size != 10 {
			t.Fatalf("range=%+v body=%q size=%d err=%v", requested, body, result.Size, readErr)
		}
	}
	result, err := store.OpenRange(context.Background(), key, ByteRange{Start: 20, End: -1})
	if !errors.Is(err, ErrRangeNotSatisfiable) || result.Size != 10 {
		t.Fatalf("unsatisfiable result=%+v err=%v", result, err)
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
