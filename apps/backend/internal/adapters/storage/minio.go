// Package storage wraps a MinIO/S3-compatible object store. Pattern mirrors
// gochick-be/internal/storage/minio.go: a single StorageService struct with
// a constructor that auto-creates the bucket, an Upload helper that returns
// a stable direct URL, and a presign helper that turns that URL into a
// time-limited GET. Argentum's generate_document tool uses fixed object
// keys (UploadKey) instead of the random-uuid Upload helper.
package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/sirupsen/logrus"
)

// MinIOConfig captures everything NewStorageService needs. Mirrors
// gochick-be's MinIOConfig field-for-field so operators familiar with that
// project can move between them without re-learning the env vars.
type MinIOConfig struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	UseSSL          bool
}

type StorageService struct {
	client   *minio.Client
	bucket   string
	endpoint string
	useSSL   bool
}

func NewStorageService(cfg *MinIOConfig) (*StorageService, error) {
	if cfg == nil || cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, fmt.Errorf("storage: endpoint and bucket are required")
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: create minio client: %w", err)
	}

	ctx := context.Background()
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("storage: bucket exists check: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("storage: create bucket %q: %w", cfg.Bucket, err)
		}
		logrus.WithField("bucket", cfg.Bucket).Info("storage: created bucket")
	}

	return &StorageService{
		client:   client,
		bucket:   cfg.Bucket,
		endpoint: cfg.Endpoint,
		useSSL:   cfg.UseSSL,
	}, nil
}

// Upload writes the body under "{folder}/{uuid}{ext}" and returns the
// stored direct URL. Mirrors gochick-be Upload semantics for cases where
// the caller doesn't care about the exact key.
func (s *StorageService) Upload(ctx context.Context, reader io.Reader, filename, contentType, folder string) (string, error) {
	ext := filepath.Ext(filename)
	key := fmt.Sprintf("%s/%s%s", strings.TrimSuffix(folder, "/"), uuid.New().String(), ext)
	return s.UploadKey(ctx, key, reader, contentType)
}

// UploadKey writes the body under an exact key. Used by callers that need
// the key to be deterministic (e.g. the generate_document tool, which
// uses documents/{company}/{thread}/{document_id}.{ext}).
func (s *StorageService) UploadKey(ctx context.Context, key string, reader io.Reader, contentType string) (string, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("storage: read body: %w", err)
	}
	_, err = s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return "", fmt.Errorf("storage: put %q: %w", key, err)
	}
	return s.directURL(key), nil
}

// GeneratePresignedURL returns a time-limited GET URL for an object that
// was previously uploaded via Upload/UploadKey (i.e. its stored URL was
// produced by directURL).
func (s *StorageService) GeneratePresignedURL(ctx context.Context, storedURL string, expiry time.Duration) (string, error) {
	if storedURL == "" {
		return "", nil
	}
	key := s.extractObjectName(storedURL)
	if key == "" {
		return "", fmt.Errorf("storage: cannot extract key from %q", storedURL)
	}
	signed, err := s.client.PresignedGetObject(ctx, s.bucket, key, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("storage: presign %q: %w", key, err)
	}
	return signed.String(), nil
}

// DownloadKey reads an object back into memory.
//
// Every caller so far is the report renderer fetching a tenant logo, which is
// capped at half a megabyte on the way in — so this returns bytes rather than
// a ReadCloser. A caller that needs to stream something large should add a
// method rather than growing this one, because a []byte return is a promise
// about size that a stream is not.
func (s *StorageService) DownloadKey(ctx context.Context, key string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage: get %q: %w", key, err)
	}
	defer func() { _ = obj.Close() }()
	data, err := io.ReadAll(obj)
	if err != nil {
		// minio-go defers the request until first read, so a missing object
		// surfaces here rather than from GetObject.
		return nil, fmt.Errorf("storage: read %q: %w", key, err)
	}
	return data, nil
}

// StreamKey opens an object for reading and reports its size.
//
// The method DownloadKey's comment asked for (T-A2). `GET
// /v1/documents/:id/content` writes a generated document straight to an HTTP
// response, and a deck with a dozen chart images is megabytes — buffering
// every concurrent download in the API's heap is a memory footprint chosen by
// whoever is downloading. The caller closes the reader.
//
// The size comes from a HEAD rather than from the caller's row, because the
// Content-Length header has to describe the stream that is actually about to
// be written.
func (s *StorageService) StreamKey(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("storage: stat %q: %w", key, err)
	}
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("storage: get %q: %w", key, err)
	}
	return obj, info.Size, nil
}

// PresignKey is a convenience for callers that already hold the key.
func (s *StorageService) PresignKey(ctx context.Context, key string, expiry time.Duration) (string, error) {
	signed, err := s.client.PresignedGetObject(ctx, s.bucket, key, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("storage: presign %q: %w", key, err)
	}
	return signed.String(), nil
}

// RemoveKey deletes an object.
//
// The first caller is the uploaded-document path (T-P1), which needs it twice:
// once when the row it was going to point at could not be written, and once
// when a tenant deletes the document. A tenant who deletes a PDF has not
// consented to its bytes surviving in a bucket, and a row that was never
// created leaves an object nothing will ever reference again.
//
// A missing object is not an error. Both callers are cleaning up, and "it is
// already gone" is the outcome they wanted.
func (s *StorageService) RemoveKey(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil && minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("storage: remove %q: %w", key, err)
	}
	return nil
}

func (s *StorageService) Bucket() string { return s.bucket }

func (s *StorageService) directURL(key string) string {
	scheme := "http"
	if s.useSSL {
		scheme = "https"
	}
	// url.PathEscape each segment to keep slashes intact but escape spaces etc.
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return fmt.Sprintf("%s://%s/%s/%s", scheme, s.endpoint, s.bucket, strings.Join(parts, "/"))
}

func (s *StorageService) extractObjectName(storedURL string) string {
	marker := "/" + s.bucket + "/"
	idx := strings.Index(storedURL, marker)
	if idx < 0 {
		return ""
	}
	encoded := storedURL[idx+len(marker):]
	// Reverse the per-segment escaping done in directURL.
	parts := strings.Split(encoded, "/")
	for i, p := range parts {
		if dec, err := url.PathUnescape(p); err == nil {
			parts[i] = dec
		}
	}
	return strings.Join(parts, "/")
}
