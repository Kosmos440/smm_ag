// Package media stages public source originals through private S3.
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"smmagent/internal/ingest"
	"smmagent/internal/model"
)

type Store struct {
	client  *s3.Client
	public  *http.Client
	bucket  string
	TempDir string
}

func New(endpoint, bucket, accessKey, secretKey, tempDir string) *Store {
	client := s3.New(s3.Options{
		Region: "us-east-1", BaseEndpoint: aws.String(endpoint), UsePathStyle: true,
		Credentials:                credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	public := ingest.NewPublicClient()
	public.Timeout = 90 * time.Second
	return &Store{client: client, public: public, bucket: bucket, TempDir: tempDir}
}

// Open returns bytes read from S3, including on the first source download.
// The source URL hash gives repeated deliveries a stable object identity.
func (s *Store) Open(ctx context.Context, item model.Media) (io.ReadCloser, error) {
	if item.Kind != "photo" && item.Kind != "video" {
		return nil, errors.New("unsupported media kind")
	}
	if _, err := ingest.ParseURL(item.URL); err != nil {
		return nil, errors.New("invalid media URL")
	}
	hash := sha256.Sum256([]byte(item.Kind + "\n" + item.URL))
	key := "news/" + hex.EncodeToString(hash[:])
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err == nil {
		return output.Body, nil
	}
	var apiError smithy.APIError
	if !errors.As(err, &apiError) || (apiError.ErrorCode() != "NoSuchKey" && apiError.ErrorCode() != "NotFound") {
		return nil, errors.New("S3 read failed")
	}
	if err := s.upload(ctx, item, key); err != nil {
		return nil, err
	}
	output, err = s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key})
	if err != nil {
		return nil, errors.New("S3 read after upload failed")
	}
	return output.Body, nil
}

func (s *Store) upload(ctx context.Context, item model.Media, key string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
	if err != nil {
		return errors.New("invalid media request")
	}
	response, err := s.public.Do(request)
	if err != nil {
		return errors.New("source media download failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("source media returned an unsuccessful status")
	}
	limit := int64(10 << 20)
	if item.Kind == "video" {
		limit = 50 << 20
	}
	file, err := os.CreateTemp(s.TempDir, "source-media-*")
	if err != nil {
		return errors.New("cannot create media staging file")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	size, err := io.Copy(file, io.LimitReader(response.Body, limit+1))
	if err != nil || size == 0 || size > limit {
		return errors.New("source media exceeds size limit or download is incomplete")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	header := make([]byte, 512)
	n, _ := file.Read(header)
	contentType := http.DetectContentType(header[:n])
	if (item.Kind == "photo" && contentType != "image/jpeg" && contentType != "image/png") ||
		(item.Kind == "video" && !strings.HasPrefix(contentType, "video/mp4")) {
		return errors.New("media requires JPEG, PNG, or MP4")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &s.bucket, Key: &key, Body: file,
		ContentType: &contentType, ContentLength: &size,
	})
	if err != nil {
		return errors.New("S3 upload failed")
	}
	return nil
}
