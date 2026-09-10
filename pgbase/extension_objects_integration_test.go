//go:build integration

package pgbase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/filestore"
)

func TestExtensionObjectsStreamListCopyAndRejectTraversal(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 32}
	})
	objects := fixture.app.extensions.objects
	ctx := context.Background()
	if _, err := objects.WriteStream(ctx, "module.js", "docs/a", bytes.NewBufferString("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := objects.Write(ctx, "module.js", "docs/b", []byte("bravo")); err != nil {
		t.Fatal(err)
	}
	page, err := objects.List(ctx, "module.js", "docs/", "", 1)
	if err != nil || len(page.Objects) != 1 || page.Objects[0].Key != "docs/a" || page.NextCursor != "docs/a" {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	page, err = objects.List(ctx, "module.js", "docs/", page.NextCursor, 1)
	if err != nil || len(page.Objects) != 1 || page.Objects[0].Key != "docs/b" || page.NextCursor != "" {
		t.Fatalf("next page=%#v err=%v", page, err)
	}
	if size, err := objects.Copy(ctx, "module.js", "docs/a", "docs/c"); err != nil || size != 5 {
		t.Fatalf("copy size=%d err=%v", size, err)
	}
	stream, err := objects.Open(ctx, "module.js", "docs/c")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(stream)
	_ = stream.Close()
	if err != nil || string(data) != "alpha" {
		t.Fatalf("stream=%q err=%v", data, err)
	}
	if _, err := objects.WriteStream(ctx, "module.js", "../escape", bytes.NewBufferString("x")); err == nil {
		t.Fatal("traversal accepted")
	}
	if _, err := objects.WriteStream(ctx, "module.js", "too-large", bytes.NewReader(bytes.Repeat([]byte("x"), 33))); !errors.Is(err, filestore.ErrTooLarge) {
		t.Fatalf("large stream err=%v", err)
	}
	if _, err := objects.Read(ctx, "module.js", "too-large"); !errors.Is(err, filestore.ErrNotFound) {
		t.Fatalf("failed stream left object: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := objects.WriteStream(cancelled, "module.js", "cancelled", bytes.NewBufferString("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stream err=%v", err)
	}
	if _, err := objects.Read(ctx, "module.js", "cancelled"); !errors.Is(err, filestore.ErrNotFound) {
		t.Fatalf("cancelled stream left object: %v", err)
	}
}

func TestExtensionObjectsStreamListCopyOnMinIO(t *testing.T) {
	endpoint := testS3Endpoint(t)
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "s3", S3Bucket: "pgbase-test", S3Region: "us-east-1", S3Endpoint: endpoint, S3AccessKey: "pgbase-test-access", S3SecretKey: "pgbase-test-secret-0123456789", S3UsePathStyle: true, S3AllowInsecureLoopback: true, MaximumBytes: 1024}
	})
	objects := fixture.app.extensions.objects
	if _, err := objects.WriteStream(t.Context(), "s3.js", "prefix/one", bytes.NewBufferString("one")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = objects.Delete(context.Background(), "s3.js", "prefix/one")
		_ = objects.Delete(context.Background(), "s3.js", "prefix/two")
	})
	if _, err := objects.Copy(t.Context(), "s3.js", "prefix/one", "prefix/two"); err != nil {
		t.Fatal(err)
	}
	page, err := objects.List(t.Context(), "s3.js", "prefix/", "", 10)
	if err != nil || len(page.Objects) != 2 {
		t.Fatalf("s3 page=%#v err=%v", page, err)
	}
}

func testS3Endpoint(t *testing.T) string {
	t.Helper()
	endpoint := os.Getenv("PGBASE_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("PGBASE_TEST_S3_ENDPOINT is not set")
	}
	return endpoint
}
