package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type S3RepositoryOptions struct {
	Bucket, Prefix, Region, Endpoint, AccessKey, SecretKey string
	UsePathStyle                                           bool
}

type backupS3Client interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

type S3Repository struct {
	bucket, prefix, region, endpoint string
	client                           backupS3Client
}

const s3StorageFormat = "pgbase-backup-v1"

func (r *S3Repository) RepositoryIdentity() string {
	digest := sha256.Sum256([]byte(r.endpoint + "\x00" + r.region + "\x00" + r.bucket + "\x00" + r.prefix))
	return "s3:" + hex.EncodeToString(digest[:])
}

func NewS3Repository(ctx context.Context, input S3RepositoryOptions) (*S3Repository, error) {
	input.Prefix = strings.Trim(strings.TrimSpace(input.Prefix), "/")
	if input.Bucket == "" || input.Region == "" || (input.AccessKey == "") != (input.SecretKey == "") || strings.Contains(input.Prefix, "..") {
		return nil, errors.New("invalid backup S3 configuration")
	}
	load := []func(*config.LoadOptions) error{config.WithRegion(input.Region), config.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired)}
	if input.AccessKey != "" {
		load = append(load, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(input.AccessKey, input.SecretKey, "")))
	}
	cfg, err := config.LoadDefaultConfig(ctx, load...)
	if err != nil {
		return nil, fmt.Errorf("load backup S3 configuration: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.UsePathStyle = input.UsePathStyle
		if input.Endpoint != "" {
			options.BaseEndpoint = aws.String(input.Endpoint)
		}
	})
	return &S3Repository{bucket: input.Bucket, prefix: input.Prefix, region: input.Region, endpoint: input.Endpoint, client: client}, nil
}

func (r *S3Repository) key(id, suffix string) string {
	if r.prefix == "" {
		return id + suffix
	}
	return r.prefix + "/" + id + suffix
}
func (r *S3Repository) put(ctx context.Context, key string, body io.Reader, createOnly ...bool) error {
	input := &s3.PutObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(key), Body: body}
	if len(createOnly) > 0 && createOnly[0] {
		input.IfNoneMatch = aws.String("*")
	}
	_, err := r.client.PutObject(ctx, input, func(o *s3.Options) {
		o.APIOptions = append(o.APIOptions, awsv4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware)
	})
	return err
}

func (r *S3Repository) putArchive(ctx context.Context, id string, body io.Reader) error {
	input := &s3.PutObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(r.key(id, ".tar.gz")), Body: body,
		IfNoneMatch: aws.String("*"), Metadata: map[string]string{"pgbase-format": s3StorageFormat, "pgbase-id": id}}
	_, err := r.client.PutObject(ctx, input, func(o *s3.Options) {
		o.APIOptions = append(o.APIOptions, awsv4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware)
	})
	return err
}
func (r *S3Repository) remove(ctx context.Context, key string) error {
	_, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(key)})
	return err
}
func (r *S3Repository) Publish(ctx context.Context, entry Entry, archive io.Reader) error {
	if entry.StorageFormat != "" && entry.StorageFormat != s3StorageFormat {
		return ErrInvalidStorage
	}
	entry.StorageFormat = s3StorageFormat
	if err := r.putArchive(ctx, entry.ID, archive); err != nil {
		return fmt.Errorf("upload backup archive: %w", err)
	}
	metadata, err := json.Marshal(entry)
	if err == nil {
		err = r.put(ctx, r.key(entry.ID, ".json"), bytes.NewReader(metadata), true)
	}
	if err != nil {
		r.cleanupArchive(ctx, entry.ID)
		return fmt.Errorf("publish backup metadata: %w", err)
	}
	return nil
}

func (r *S3Repository) cleanupArchive(parent context.Context, id string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer cancel()
	_ = r.remove(cleanup, r.key(id, ".tar.gz"))
}
func (r *S3Repository) Open(ctx context.Context, id string) (io.ReadCloser, error) {
	if !validID(id) {
		return nil, ErrNotFound
	}
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(r.key(id, ".tar.gz"))})
	if s3Missing(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}
func (r *S3Repository) Get(ctx context.Context, id string) (Entry, error) {
	if !validID(id) {
		return Entry{}, ErrNotFound
	}
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(r.key(id, ".json"))})
	if s3Missing(err) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(out.Body, (64<<10)+1))
	readErr = errors.Join(readErr, out.Body.Close())
	if readErr != nil || len(raw) > 64<<10 {
		return Entry{}, ErrInvalidStorage
	}
	var entry Entry
	if json.Unmarshal(raw, &entry) != nil {
		return Entry{}, ErrInvalidStorage
	}
	// A valid JSON object without our marker belongs to another application and
	// remains invisible. Once marked as ours, malformed ownership or integrity
	// metadata is repository corruption and must stop List/retention.
	if entry.StorageFormat != s3StorageFormat {
		return Entry{}, ErrNotFound
	}
	digest, digestErr := hex.DecodeString(entry.SHA256)
	if entry.ID != id || entry.Size < 0 || entry.Size > MaximumArchiveBytes || digestErr != nil || len(digest) != sha256.Size {
		return Entry{}, ErrInvalidStorage
	}
	return entry, nil
}
func (r *S3Repository) List(ctx context.Context) ([]Entry, error) {
	prefix := r.prefix
	if prefix != "" {
		prefix += "/"
	}
	var token *string
	entries := []Entry{}
	for {
		out, err := r.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(r.bucket), Prefix: aws.String(prefix), ContinuationToken: token})
		if err != nil {
			return nil, err
		}
		for _, object := range out.Contents {
			key := aws.ToString(object.Key)
			if !strings.HasSuffix(key, ".json") {
				continue
			}
			id := strings.TrimSuffix(strings.TrimPrefix(key, prefix), ".json")
			entry, getErr := r.Get(ctx, id)
			if getErr != nil {
				if errors.Is(getErr, ErrNotFound) {
					continue
				}
				return nil, getErr
			}
			archive, openErr := r.Open(ctx, id)
			if openErr != nil {
				if errors.Is(openErr, ErrNotFound) {
					continue
				}
				return nil, openErr
			}
			if closeErr := archive.Close(); closeErr != nil {
				return nil, closeErr
			}
			entries = append(entries, entry)
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		token = out.NextContinuationToken
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].CreatedAt.After(entries[j].CreatedAt) })
	return entries, nil
}

func (r *S3Repository) Delete(ctx context.Context, id string) error {
	if _, err := r.Get(ctx, id); err != nil {
		return err
	}
	if err := r.remove(ctx, r.key(id, ".tar.gz")); err != nil {
		return err
	}
	return r.remove(ctx, r.key(id, ".json"))
}
func (r *S3Repository) Discard(ctx context.Context, id string) error {
	if !validID(id) {
		return ErrNotFound
	}
	archive, err := r.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(r.key(id, ".tar.gz"))})
	if s3Missing(err) {
		// With no archive there is no ownership proof for a same-name metadata
		// object. Leave it untouched and only let the caller clear its DB fence.
		return nil
	}
	if err != nil {
		return err
	}
	_ = archive.Body.Close()
	if archive.Metadata["pgbase-format"] != s3StorageFormat || archive.Metadata["pgbase-id"] != id {
		return ErrStorageConflict
	}
	_, receiptErr := r.Get(ctx, id)
	ownedReceipt := receiptErr == nil
	if receiptErr != nil && !errors.Is(receiptErr, ErrNotFound) && !errors.Is(receiptErr, ErrInvalidStorage) {
		return receiptErr
	}
	archiveErr := r.remove(ctx, r.key(id, ".tar.gz"))
	if archiveErr != nil || !ownedReceipt {
		return archiveErr
	}
	return r.remove(ctx, r.key(id, ".json"))
}
func s3Missing(err error) bool {
	var api smithy.APIError
	return errors.As(err, &api) && (api.ErrorCode() == "NoSuchKey" || api.ErrorCode() == "NotFound")
}
