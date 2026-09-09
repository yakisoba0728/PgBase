//go:build integration

package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

func TestS3RepositoryPublishListStreamAndDelete(t *testing.T) {
	endpoint := os.Getenv("PGBASE_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("PGBASE_TEST_S3_ENDPOINT is not set")
	}
	repository, err := NewS3Repository(context.Background(), S3RepositoryOptions{
		Bucket: "pgbase-test", Prefix: "backup-tests/" + uuid.NewString(), Region: "us-east-1", Endpoint: endpoint,
		AccessKey: "pgbase-test-access", SecretKey: "pgbase-test-secret-0123456789", UsePathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	archive := []byte("portable backup archive")
	digest := sha256.Sum256(archive)
	entry := Entry{ID: id, CreatedAt: time.Now().UTC(), Size: int64(len(archive)), PostgreSQLMajor: 18, SHA256: fmt.Sprintf("%x", digest[:])}
	if err := repository.Publish(context.Background(), entry, bytes.NewReader(archive)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Delete(context.Background(), id) })
	foreignID := uuid.NewString()
	foreignArchive := repository.key(foreignID, ".tar.gz")
	foreignMetadata := repository.key(foreignID, ".json")
	if err := repository.put(context.Background(), foreignArchive, bytes.NewReader([]byte("foreign"))); err != nil {
		t.Fatal(err)
	}
	if err := repository.put(context.Background(), foreignMetadata, bytes.NewReader([]byte(fmt.Sprintf(`{"id":%q}`, foreignID)))); err != nil {
		t.Fatal(err)
	}
	defer repository.remove(context.Background(), foreignArchive)
	defer repository.remove(context.Background(), foreignMetadata)
	unrelated := repository.prefix + "/nested/unrelated.json"
	if err := repository.put(context.Background(), unrelated, bytes.NewReader([]byte("{}"))); err != nil {
		t.Fatal(err)
	}
	defer repository.remove(context.Background(), unrelated)
	listed, err := repository.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0].ID != id {
		t.Fatalf("List()=%+v,%v", listed, err)
	}
	foreign, err := repository.client.GetObject(context.Background(), &s3.GetObjectInput{Bucket: aws.String(repository.bucket), Key: aws.String(foreignArchive)})
	if err != nil {
		t.Fatalf("foreign object removed: %v", err)
	}
	_ = foreign.Body.Close()
	foreign, err = repository.client.GetObject(context.Background(), &s3.GetObjectInput{Bucket: aws.String(repository.bucket), Key: aws.String(unrelated)})
	if err != nil {
		t.Fatalf("unrelated nested object removed: %v", err)
	}
	_ = foreign.Body.Close()
	got, err := repository.Get(context.Background(), id)
	if err != nil || got.ID != id {
		t.Fatalf("Get()=%+v,%v", got, err)
	}
	reader, err := repository.Open(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(raw, archive) {
		t.Fatalf("Open()=%q,%v,%v", raw, readErr, closeErr)
	}
	if err := repository.Delete(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(deleted)=%v", err)
	}
}

func TestManagerCreatesAndRestoresThroughS3(t *testing.T) {
	endpoint := os.Getenv("PGBASE_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("PGBASE_TEST_S3_ENDPOINT is not set")
	}
	repository, err := NewS3Repository(context.Background(), S3RepositoryOptions{Bucket: "pgbase-test", Prefix: "backup-manager-tests/" + uuid.NewString(), Region: "us-east-1", Endpoint: endpoint, AccessKey: "pgbase-test-access", SecretKey: "pgbase-test-secret-0123456789", UsePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	restored := make(chan struct{}, 1)
	manager, err := NewManager(ManagerOptions{Directory: t.TempDir(), Repository: repository, Create: func(_ context.Context, path string) error { makeArchiveFixture(t, path); return nil }, Restore: func(_ context.Context, path string) error {
		if _, err := Verify(path); err != nil {
			return err
		}
		restored <- struct{}{}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(context.Background())
	create, err := manager.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, manager, create.ID)
	entries, err := manager.List(context.Background())
	if err != nil || len(entries) != 1 {
		t.Fatalf("List()=%+v,%v", entries, err)
	}
	restore, err := manager.Restore(context.Background(), entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, manager, restore.ID)
	select {
	case <-restored:
	case <-time.After(time.Second):
		t.Fatal("restore callback not reached")
	}
	if err := manager.Delete(context.Background(), entries[0].ID); err != nil {
		t.Fatal(err)
	}
}

func waitBackupJob(t *testing.T, manager *Manager, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, err := manager.Job(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == "completed" {
			// Receipt publication precedes the worker releasing its reservation.
			manager.wg.Wait()
			return
		}
		if job.Status == "failed" {
			t.Fatalf("job failed: %+v", job)
		}
		if time.Now().After(deadline) {
			t.Fatal("job timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
