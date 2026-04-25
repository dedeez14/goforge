package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// S3Config configures an S3-compatible client. Endpoint is empty for
// AWS S3; set it to the MinIO/R2/B2 base URL to point elsewhere.
type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string

	// UsePathStyle is required by MinIO and most non-AWS S3
	// implementations.
	UsePathStyle bool
}

// S3 is an S3-compatible Storage implementation built on aws-sdk-go-v2.
type S3 struct {
	cfg     S3Config
	client  *s3.Client
	presign *s3.PresignClient
}

// NewS3 constructs a client. The constructor does not validate the
// bucket exists; call Ping (Put + Delete a probe key) yourself when
// you need eager validation.
func NewS3(ctx context.Context, cfg S3Config) (*S3, error) {
	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(orDefault(cfg.Region, "us-east-1")),
	}
	if cfg.AccessKeyID != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken),
		))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.UsePathStyle
	})
	return &S3{
		cfg:     cfg,
		client:  client,
		presign: s3.NewPresignClient(client),
	}, nil
}

// Put implements Storage.
func (s *S3) Put(ctx context.Context, key string, body io.Reader, size int64, ctype string) error {
	in := &s3.PutObjectInput{
		Bucket:      aws.String(s.cfg.Bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(ctype),
	}
	if size >= 0 {
		in.ContentLength = aws.Int64(size)
	}
	_, err := s.client.PutObject(ctx, in)
	return err
}

// Get implements Storage.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return out.Body, nil
}

// Delete implements Storage.
func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil && isNotFound(err) {
		return nil
	}
	return err
}

// PresignPut implements Storage.
func (s *S3) PresignPut(ctx context.Context, key string, ttl time.Duration, ctype string) (string, error) {
	r, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.cfg.Bucket),
		Key:         aws.String(key),
		ContentType: aws.String(ctype),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return r.URL, nil
}

// PresignGet implements Storage.
func (s *S3) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	r, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return r.URL, nil
}

// List implements Storage.
func (s *S3) List(ctx context.Context, prefix string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 1000
	}
	out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(s.cfg.Bucket),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int32(int32(limit)),
	})
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(out.Contents))
	for _, o := range out.Contents {
		if o.Key != nil {
			keys = append(keys, *o.Key)
		}
	}
	return keys, nil
}

func isNotFound(err error) bool {
	var nsk *types.NoSuchKey
	if errors.As(err, &nsk) {
		return true
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "NoSuchKey", "NotFound", "404":
			return true
		}
	}
	return false
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
