package filestore

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type disappearingRangeS3Client struct{ headCode string }

func (disappearingRangeS3Client) PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	return nil, errors.New("unused")
}
func (disappearingRangeS3Client) GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return nil, &smithy.GenericAPIError{Code: "InvalidRange", Message: "range invalid"}
}
func (client disappearingRangeS3Client) HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return nil, &smithy.GenericAPIError{Code: client.headCode, Message: "gone"}
}
func (disappearingRangeS3Client) DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	return nil, errors.New("unused")
}

func TestParseS3RangeMetadataRejectsInconsistentResponses(t *testing.T) {
	requested := ByteRange{Start: 2, End: 5}
	for _, test := range []struct {
		value  string
		length int64
	}{
		{"items 2-5/10", 4},
		{"bytes -1-5/10", 7},
		{"bytes 5-2/10", 4},
		{"bytes 2-10/10", 9},
		{"bytes 1-4/10", 4},
		{"bytes 2-5/10", 3},
	} {
		if _, _, _, err := parseS3RangeMetadata(test.value, test.length, requested); err == nil {
			t.Fatalf("metadata %q length %d accepted", test.value, test.length)
		}
	}
	start, end, size, err := parseS3RangeMetadata("bytes 2-5/10", 4, requested)
	if err != nil || start != 2 || end != 5 || size != 10 {
		t.Fatalf("valid metadata=%d-%d/%d err=%v", start, end, size, err)
	}
}

func TestS3RangeMapsDisappearanceDuringSizeResolutionToNotFound(t *testing.T) {
	store := &S3Store{bucket: "bucket", client: disappearingRangeS3Client{headCode: "NoSuchKey"}}
	_, err := store.OpenRange(context.Background(), "objects/00000000-0000-0000-0000-000000000001", ByteRange{Start: 20, End: -1})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("range race error=%v, want ErrNotFound", err)
	}
	store.client = disappearingRangeS3Client{headCode: "NotFound"}
	_, err = store.OpenRange(context.Background(), "objects/00000000-0000-0000-0000-000000000001", ByteRange{Start: 0, End: -2})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed range size error=%v, want ErrNotFound", err)
	}
}
