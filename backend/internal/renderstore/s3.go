// backend/internal/renderstore/s3.go
package renderstore

import (
	"bytes"
	"context"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3RenderStore stores encrypted render blobs in any S3-compatible service
// (AWS S3, Yandex Object Storage, MinIO, etc.).
// Object key layout: <repoID>/<notePath>.enc
type S3RenderStore struct {
	client *minio.Client
	bucket string
}

func NewS3(endpoint, bucket, accessKey, secretKey, region string, useSSL bool) (*S3RenderStore, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: region,
	})
	if err != nil {
		return nil, err
	}
	return &S3RenderStore{client: client, bucket: bucket}, nil
}

func (s *S3RenderStore) Write(repoID, notePath string, data []byte) error {
	key := repoID + "/" + notePath + ".enc"
	_, err := s.client.PutObject(
		context.Background(), s.bucket, key,
		bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"},
	)
	return err
}

func (s *S3RenderStore) Read(repoID, notePath string) ([]byte, error) {
	key := repoID + "/" + notePath + ".enc"
	obj, err := s.client.GetObject(context.Background(), s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, nil
		}
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
	key := repoID + "/" + notePath + ".enc"
	err := s.client.RemoveObject(context.Background(), s.bucket, key, minio.RemoveObjectOptions{})
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return nil
	}
	return err
}
