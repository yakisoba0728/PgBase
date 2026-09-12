package filestore

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type S3Config struct {
	Bucket, Region, Endpoint, AccessKey, SecretKey string
	UsePathStyle                                   bool
}

type s3Client interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

type S3Store struct {
	bucket string
	client s3Client
}

func NewS3Store(ctx context.Context, input S3Config) (*S3Store, error) {
	if input.Bucket == "" || input.Region == "" || (input.AccessKey == "") != (input.SecretKey == "") {
		return nil, ErrInvalid
	}
	options := []func(*config.LoadOptions) error{
		config.WithRegion(input.Region),
		config.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
	}
	if input.AccessKey != "" {
		options = append(options, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(input.AccessKey, input.SecretKey, "")))
	}
	cfg, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("load S3 configuration: %w", err)
	}
	client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.UsePathStyle = input.UsePathStyle
		if input.Endpoint != "" {
			options.BaseEndpoint = aws.String(input.Endpoint)
		}
	})
	return &S3Store{bucket: input.Bucket, client: client}, nil
}

func (s *S3Store) validate(key string) error {
	if s == nil || s.client == nil || s.bucket == "" || !objectKeyPattern.MatchString(key) {
		return ErrInvalid
	}
	return nil
}

func (s *S3Store) Put(ctx context.Context, key string, body io.Reader) error {
	return s.put(ctx, key, body, nil)
}

func (s *S3Store) PutSized(ctx context.Context, key string, body io.Reader, size int64) error {
	if size < 0 {
		return ErrInvalid
	}
	return s.put(ctx, key, body, &size)
}

func (s *S3Store) put(ctx context.Context, key string, body io.Reader, size *int64) error {
	if err := s.validate(key); err != nil {
		return err
	}
	input := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), Body: body}
	if size != nil {
		input.ContentLength = aws.Int64(*size)
	}
	_, err := s.client.PutObject(ctx, input, func(options *s3.Options) {
		options.APIOptions = append(options.APIOptions, awsv4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware)
	})
	if err != nil {
		return fmt.Errorf("put S3 object: %w", err)
	}
	return nil
}

func (s *S3Store) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := s.validate(key); err != nil {
		return nil, err
	}
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		var api smithy.APIError
		if errors.As(err, &api) && (api.ErrorCode() == "NoSuchKey" || api.ErrorCode() == "NotFound") {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get S3 object: %w", err)
	}
	return result.Body, nil
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	if err := s.validate(key); err != nil {
		return err
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("delete S3 object: %w", err)
	}
	return nil
}
