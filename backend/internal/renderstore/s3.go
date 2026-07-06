// backend/internal/renderstore/s3.go
package renderstore

import (
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3RenderStore stores encrypted blobs in any S3-compatible service (AWS S3,
// Yandex Object Storage, MinIO, etc.). Object key layout:
// <keyPrefix><repoID>/<notePath>.enc
type S3RenderStore struct {
	client    *minio.Client
	bucket    string
	keyPrefix string
}

func NewS3(endpoint, bucket, accessKey, secretKey, region string, useSSL bool, keyPrefix string) (*S3RenderStore, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, err
	}
	return &S3RenderStore{client: client, bucket: bucket, keyPrefix: keyPrefix}, nil
}

// ObjectKey returns the S3 key for (repoID, notePath) — exported so the
// disk-usage listing code (Task 8) can recognize which prefix a listed
// object belongs to.
func (s *S3RenderStore) ObjectKey(repoID, notePath string) string {
	return s.keyPrefix + repoID + "/" + notePath + ".enc"
}

func (s *S3RenderStore) Write(repoID, notePath string, data []byte) error {
	_, err := s.client.PutObject(
		context.Background(), s.bucket, s.ObjectKey(repoID, notePath),
		bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"},
	)
	return err
}

func (s *S3RenderStore) Read(repoID, notePath string) ([]byte, error) {
	obj, err := s.client.GetObject(context.Background(), s.bucket, s.ObjectKey(repoID, notePath), minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

func (s *S3RenderStore) Delete(repoID, notePath string) error {
	err := s.client.RemoveObject(context.Background(), s.bucket, s.ObjectKey(repoID, notePath), minio.RemoveObjectOptions{})
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return nil
	}
	return err
}

// ListAllObjects returns every object currently in the bucket (across all
// prefixes) — the caller filters/sums by prefix as needed. Uses minio's
// recursive listing so it pages through the full bucket regardless of size.
func (s *S3RenderStore) ListAllObjects() ([]minio.ObjectInfo, error) {
	var all []minio.ObjectInfo
	for obj := range s.client.ListObjects(context.Background(), s.bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		all = append(all, obj)
	}
	return all, nil
}

// SumObjectSizesWithPrefix sums the sizes of objects whose key starts with
// prefix — a small pure function so the filtering logic is unit-testable
// without a live S3 endpoint.
func SumObjectSizesWithPrefix(objects []minio.ObjectInfo, prefix string) int64 {
	var total int64
	for _, obj := range objects {
		if strings.HasPrefix(obj.Key, prefix) {
			total += obj.Size
		}
	}
	return total
}
