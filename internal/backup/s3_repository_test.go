package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type failingS3Client struct {
	puts, deletes []string
	failMetadata  bool
	failArchive   bool
}

type receiptS3Client struct {
	objects         map[string]string
	metadata        map[string]map[string]string
	deletes         []string
	archiveCloseErr error
	closeFailures   int
}

type closeErrorReader struct {
	io.Reader
	err error
}

func (r closeErrorReader) Close() error { return r.err }

func (*receiptS3Client) PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	return nil, errors.New("unused")
}
func (c *receiptS3Client) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := aws.ToString(input.Key)
	value, ok := c.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	body := io.ReadCloser(io.NopCloser(strings.NewReader(value)))
	if strings.HasSuffix(key, ".tar.gz") && c.closeFailures > 0 {
		c.closeFailures--
		body = closeErrorReader{Reader: strings.NewReader(value), err: c.archiveCloseErr}
	}
	return &s3.GetObjectOutput{Body: body, Metadata: c.metadata[key]}, nil
}
func (c *receiptS3Client) DeleteObject(_ context.Context, input *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	c.deletes = append(c.deletes, aws.ToString(input.Key))
	return &s3.DeleteObjectOutput{}, nil
}

func TestS3DiscardRequiresArchiveOwnershipMarker(t *testing.T) {
	id := "019551d0-e9c0-7000-8000-000000000001"
	archiveKey := id + ".tar.gz"
	client := &receiptS3Client{objects: map[string]string{archiveKey: "foreign"}, metadata: map[string]map[string]string{}}
	repository := &S3Repository{bucket: "bucket", client: client}
	if err := repository.Discard(context.Background(), id); !errors.Is(err, ErrStorageConflict) {
		t.Fatalf("foreign archive discard=%v", err)
	}
	if len(client.deletes) != 0 {
		t.Fatalf("foreign archive deleted: %v", client.deletes)
	}
	client.metadata[archiveKey] = map[string]string{"pgbase-format": s3StorageFormat, "pgbase-id": id}
	client.objects[id+".json"] = `{}`
	if err := repository.Discard(context.Background(), id); err != nil {
		t.Fatalf("owned archive discard=%v", err)
	}
	if len(client.deletes) != 1 || client.deletes[0] != archiveKey {
		t.Fatalf("owned archive with foreign metadata deletes=%v", client.deletes)
	}
	client.objects[id+".json"] = `{"id":"` + id + `","storageFormat":"pgbase-backup-v1","size":1,"sha256":"0000000000000000000000000000000000000000000000000000000000000000"}`
	if err := repository.Discard(context.Background(), id); err != nil {
		t.Fatalf("owned pair discard=%v", err)
	}
	if len(client.deletes) != 3 {
		t.Fatalf("owned pair deletes=%v", client.deletes)
	}
}
func (c *receiptS3Client) ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	contents := make([]types.Object, 0, len(c.objects))
	for key := range c.objects {
		contents = append(contents, types.Object{Key: aws.String(key)})
	}
	return &s3.ListObjectsV2Output{Contents: contents}, nil
}

func (c *failingS3Client) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	key := *input.Key
	c.puts = append(c.puts, key)
	if c.failArchive && strings.HasSuffix(key, ".tar.gz") {
		return nil, errors.New("precondition failed")
	}
	_, _ = io.Copy(io.Discard, input.Body)
	if c.failMetadata && key[len(key)-5:] == ".json" {
		return nil, errors.New("metadata failed")
	}
	return &s3.PutObjectOutput{}, nil
}
func (c *failingS3Client) GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return nil, errors.New("unused")
}
func (c *failingS3Client) DeleteObject(_ context.Context, input *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	c.deletes = append(c.deletes, *input.Key)
	return &s3.DeleteObjectOutput{}, nil
}
func (c *failingS3Client) ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	return nil, errors.New("unused")
}

func TestS3PublishWritesMetadataLastAndCleansFailure(t *testing.T) {
	client := &failingS3Client{failMetadata: true}
	repository := &S3Repository{bucket: "bucket", prefix: "backups", client: client}
	entry := Entry{ID: "019551d0-e9c0-7000-8000-000000000001"}
	if err := repository.Publish(context.Background(), entry, bytes.NewReader([]byte("archive"))); err == nil {
		t.Fatal("metadata failure accepted")
	}
	if len(client.puts) != 2 || client.puts[0] != "backups/"+entry.ID+".tar.gz" || client.puts[1] != "backups/"+entry.ID+".json" {
		t.Fatalf("put order=%v", client.puts)
	}
	if len(client.deletes) != 1 || !strings.HasSuffix(client.deletes[0], ".tar.gz") {
		t.Fatalf("cleanup deletes=%v", client.deletes)
	}
}

func TestS3PublishDoesNotDeleteExistingPairAfterArchiveConflict(t *testing.T) {
	client := &failingS3Client{failArchive: true}
	repository := &S3Repository{bucket: "bucket", prefix: "backups", client: client}
	entry := Entry{ID: "019551d0-e9c0-7000-8000-000000000001"}
	if err := repository.Publish(context.Background(), entry, bytes.NewReader(nil)); err == nil {
		t.Fatal("archive conflict accepted")
	}
	if len(client.deletes) != 0 {
		t.Fatalf("existing objects deleted: %v", client.deletes)
	}
}

func TestVerifiedReaderRejectsArchiveDigestMismatch(t *testing.T) {
	reader := newVerifiedReadCloser(io.NopCloser(bytes.NewReader([]byte("tampered"))), Entry{Size: 8, SHA256: "0000000000000000000000000000000000000000000000000000000000000000"})
	if _, err := io.ReadAll(reader); err == nil {
		t.Fatal("tampered archive accepted")
	}
}

func TestS3ReceiptOwnershipAndCorruptionAreDistinguished(t *testing.T) {
	id := "019551d0-e9c0-7000-8000-000000000001"
	key := id + ".json"
	repository := &S3Repository{bucket: "bucket", client: &receiptS3Client{objects: map[string]string{key: `{}`}}}
	if _, err := repository.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign JSON receipt=%v", err)
	}
	repository.client = &receiptS3Client{objects: map[string]string{key: `{"storageFormat":"pgbase-backup-v1"} trailing`}}
	if _, err := repository.Get(context.Background(), id); !errors.Is(err, ErrInvalidStorage) {
		t.Fatalf("trailing owned receipt=%v", err)
	}
	repository.client = &receiptS3Client{objects: map[string]string{key: `{"id":"` + id + `","storageFormat":"pgbase-backup-v1","size":1,"sha256":"short"}`}}
	if _, err := repository.List(context.Background()); !errors.Is(err, ErrInvalidStorage) {
		t.Fatalf("corrupt owned receipt list=%v", err)
	}
}

func TestS3ListDoesNotPromoteArchiveProbeCloseFailure(t *testing.T) {
	id := "019551d0-e9c0-7000-8000-000000000001"
	client := &receiptS3Client{
		objects: map[string]string{
			id + ".json":   `{"id":"` + id + `","storageFormat":"pgbase-backup-v1","size":1,"sha256":"0000000000000000000000000000000000000000000000000000000000000000"}`,
			id + ".tar.gz": "archive",
		},
		archiveCloseErr: errors.New("transient response close"),
		closeFailures:   1,
	}
	repository := &S3Repository{bucket: "bucket", client: client}
	for attempt := 1; attempt <= 2; attempt++ {
		entries, err := repository.List(context.Background())
		if err != nil || len(entries) != 1 || entries[0].ID != id {
			t.Fatalf("List() attempt %d=%+v,%v", attempt, entries, err)
		}
	}
}
