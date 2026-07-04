package l3r2

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"aero-cache/internal/store"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const defaultTimeout = 750 * time.Millisecond

type Config struct {
	Enabled         bool
	Endpoint        string
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	Prefix          string
	Timeout         time.Duration
	MinBytes        int
}

type Store struct {
	client   *s3.Client
	bucket   string
	prefix   string
	timeout  time.Duration
	minBytes int
}

type DisabledStore struct{}

func NewDisabled() *DisabledStore {
	return &DisabledStore{}
}

func (s *DisabledStore) Name() string {
	return "l3-r2-disabled"
}

func (s *DisabledStore) Get(ctx context.Context, key store.Key) (*store.Entry, bool, error) {
	return nil, false, nil
}

func (s *DisabledStore) Put(ctx context.Context, key store.Key, entry *store.Entry) error {
	return nil
}

func (s *DisabledStore) Delete(ctx context.Context, key store.Key) error {
	return nil
}

func New(ctx context.Context, cfg Config) (*Store, error) {
	if !cfg.Enabled {
		return nil, errors.New("r2 store disabled")
	}

	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, errors.New("r2 endpoint is required")
	}

	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, errors.New("r2 bucket is required")
	}

	if strings.TrimSpace(cfg.AccessKeyID) == "" {
		return nil, errors.New("r2 access key id is required")
	}

	if strings.TrimSpace(cfg.SecretAccessKey) == "" {
		return nil, errors.New("r2 secret access key is required")
	}

	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = "auto"
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				cfg.AccessKeyID,
				cfg.SecretAccessKey,
				"",
			),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("load r2 aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(strings.TrimRight(cfg.Endpoint, "/"))
		o.UsePathStyle = true
	})

	return &Store{
		client:   client,
		bucket:   cfg.Bucket,
		prefix:   strings.Trim(strings.TrimSpace(cfg.Prefix), "/"),
		timeout:  timeout,
		minBytes: cfg.MinBytes,
	}, nil
}

func (s *Store) Name() string {
	return "l3-r2"
}

func (s *Store) Get(ctx context.Context, key store.Key) (*store.Entry, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, false, nil
		}

		return nil, false, fmt.Errorf("r2 get object: %w", err)
	}
	defer out.Body.Close()

	payload, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, false, fmt.Errorf("r2 read object: %w", err)
	}

	var entry store.Entry
	if err := gob.NewDecoder(bytes.NewReader(payload)).Decode(&entry); err != nil {
		return nil, false, fmt.Errorf("r2 decode entry: %w", err)
	}

	return &entry, true, nil
}

func (s *Store) Put(ctx context.Context, key store.Key, entry *store.Entry) error {
	if entry == nil {
		return nil
	}

	if s.minBytes > 0 && len(entry.Response) < s.minBytes {
		return nil
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(entry); err != nil {
		return fmt.Errorf("r2 encode entry: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(s.objectKey(key)),
		Body:        bytes.NewReader(buf.Bytes()),
		ContentType: aws.String("application/vnd.aerocache.entry+gob"),
	})
	if err != nil {
		return fmt.Errorf("r2 put object: %w", err)
	}

	return nil
}

func (s *Store) Delete(ctx context.Context, key store.Key) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.objectKey(key)),
	})
	if err != nil {
		return fmt.Errorf("r2 delete object: %w", err)
	}

	return nil
}

func (s *Store) objectKey(key store.Key) string {
	raw := strings.TrimLeft(string(key), "/")

	if s.prefix == "" {
		return raw
	}

	return s.prefix + "/" + raw
}

func isNotFound(err error) bool {
	msg := strings.ToLower(err.Error())

	return strings.Contains(msg, "notfound") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "no such key") ||
		strings.Contains(msg, "nosuchkey") ||
		strings.Contains(msg, "status code: 404")
}
