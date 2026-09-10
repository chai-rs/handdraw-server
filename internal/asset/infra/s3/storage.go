// Package s3 implements private content-addressed assets against Garage's S3 API.
package s3

import (
	"bytes"
	"context"
	"crypto/md5" // S3 transport checksum, not an identity or security digest.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/chai-rs/handdraw-server/internal/asset/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// Config requires explicit endpoint, region, private bucket and limited credentials.
type Config struct {
	Endpoint  string `split_words:"true"`
	Region    string `split_words:"true" default:"garage"`
	Bucket    string `split_words:"true"`
	AccessKey string `split_words:"true" json:"-"`
	SecretKey string `split_words:"true" json:"-"`
}

// Storage uses exact-byte identity because Garage does not enforce conditional PUT immutability.
type Storage struct {
	client *awss3.Client
	bucket string
}

var _ model.Storage = (*Storage)(nil)

// New avoids ambient cloud credentials, metadata services and external endpoint discovery.
func New(config Config) (*Storage, error) {
	u, err := url.Parse(config.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || (u.Path != "" && u.Path != "/") || config.Bucket == "" || strings.ContainsAny(config.Bucket, "/\\") || config.Region == "" || config.AccessKey == "" || config.SecretKey == "" {
		return nil, model.ErrInvalid
	}

	client := awss3.New(awss3.Options{Region: config.Region, BaseEndpoint: aws.String(strings.TrimRight(config.Endpoint, "/")), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider(config.AccessKey, config.SecretKey, ""), HTTPClient: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired, RetryMaxAttempts: 2})

	return &Storage{client: client, bucket: config.Bucket}, nil
}

// Check verifies that the configured credentials can reach the private bucket.
func (s *Storage) Check(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	return err
}

func key(a model.Asset, suffix string) (string, error) {
	digest, err := hex.DecodeString(a.SHA256)
	if resourceid.Validate(a.ID, model.IDPrefix) != nil || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != a.SHA256 || a.Size < 1 || a.Size > model.MaxImportBytes {
		return "", model.ErrInvalid
	}

	return a.ID + "/" + a.SHA256 + suffix, nil
}

func missing(err error) bool {
	var api smithy.APIError
	return errors.As(err, &api) && (api.ErrorCode() == "NoSuchKey" || api.ErrorCode() == "NotFound")
}

func verify(a model.Asset, data []byte) bool {
	sum := sha256.Sum256(data)
	return int64(len(data)) == a.Size && hex.EncodeToString(sum[:]) == a.SHA256
}

func (s *Storage) read(ctx context.Context, a model.Asset, suffix string) ([]byte, error) {
	k, err := key(a, suffix)
	if err != nil {
		return nil, err
	}

	response, err := s.client.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(k)})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if aws.ToInt64(response.ContentLength) != a.Size {
		return nil, model.ErrConflict
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, a.Size+1))
	if err != nil {
		return nil, err
	}

	if !verify(a, data) {
		return nil, model.ErrConflict
	}

	return data, nil
}

func (s *Storage) put(ctx context.Context, a model.Asset, suffix string, data []byte) error {
	k, err := key(a, suffix)
	if err != nil {
		return err
	}

	if !verify(a, data) {
		return model.ErrConflict
	}

	if old, err := s.read(ctx, a, suffix); err == nil {
		if !bytes.Equal(old, data) {
			return model.ErrConflict
		}

		return nil
	} else if !missing(err) {
		return err
	}
	// Only bytes matching the key's SHA-256 can reach this PUT. The application holds the workspace lock.
	transportChecksum := md5.Sum(data)

	_, err = s.client.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(k), Body: bytes.NewReader(data), ContentLength: aws.Int64(a.Size), ContentType: aws.String(a.MIME), ContentMD5: aws.String(base64.StdEncoding.EncodeToString(transportChecksum[:]))})
	if err != nil {
		return err
	}

	_, err = s.read(ctx, a, suffix)

	return err
}

// Stage stores verified bytes under a staging key; no browser receives S3 credentials.
func (s *Storage) Stage(ctx context.Context, a model.Asset, data []byte) error {
	return s.put(ctx, a, ".stage", data)
}

// Finalize verifies a separate final object before SQL can mark the asset available.
func (s *Storage) Finalize(ctx context.Context, a model.Asset) error {
	if _, err := s.read(ctx, a, ".final"); err == nil {
		return s.removeKey(ctx, a, ".stage")
	} else if !missing(err) {
		return err
	}

	data, err := s.read(ctx, a, ".stage")
	if err != nil {
		return err
	}

	if err = s.put(ctx, a, ".final", data); err != nil {
		return err
	}

	return s.removeKey(ctx, a, ".stage")
}

// Read returns bounded verified content; caller authorization is rechecked separately on every request.
func (s *Storage) Read(ctx context.Context, a model.Asset) ([]byte, error) {
	return s.read(ctx, a, ".final")
}

func (s *Storage) removeKey(ctx context.Context, a model.Asset, suffix string) error {
	k, err := key(a, suffix)
	if err != nil {
		return err
	}

	_, err = s.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(k)})
	if err != nil {
		return err
	}

	_, err = s.client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(k)})
	if !missing(err) {
		if err != nil {
			return err
		}

		return model.ErrConflict
	}

	return nil
}

// Remove deletes only the validated asset namespace and confirms its absence before quota release.
func (s *Storage) Remove(ctx context.Context, id string) error {
	if resourceid.Validate(id, model.IDPrefix) != nil {
		return model.ErrInvalid
	}

	prefix := id + "/"
	for range 3 {
		result, err := s.client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(prefix), MaxKeys: aws.Int32(100)})
		if err != nil {
			return err
		}

		if len(result.Contents) == 0 {
			return nil
		}

		for _, entry := range result.Contents {
			if !strings.HasPrefix(aws.ToString(entry.Key), prefix) {
				return model.ErrConflict
			}

			_, err = s.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: entry.Key})
			if err != nil {
				return err
			}
		}
	}

	return model.ErrConflict
}
