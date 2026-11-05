// Package s3 stores files in any S3-compatible service.
package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/provider"
)

const (
	// objectPrefix is the part of a bucket dextro owns. The rest belongs to
	// whoever owns the bucket and is never read, listed or deleted.
	objectPrefix = "dextro/"
	// partSize sets the memory an upload uses, since one part is held at a time.
	// With S3's limit of 10,000 parts, it also caps a file at about 156 GiB.
	partSize        = 16 << 20
	defaultPageSize = 1000
	abortTimeout    = 30 * time.Second
)

// Settings are the non-secret details of a connection.
type Settings struct {
	Endpoint string `json:"endpoint"`
	Region   string `json:"region"`
	Bucket   string `json:"bucket"`
}

// Credentials are the secret details of a connection.
type Credentials struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

// Provider is one bucket.
type Provider struct {
	client   *awss3.Client
	bucket   string
	pageSize int32
}

var _ provider.Provider = (*Provider)(nil)

// Open returns a Provider that makes its requests through httpClient, which must
// be the guarded client because the endpoint is user-supplied.
func Open(s Settings, c Credentials, httpClient *http.Client) (*Provider, error) {
	if s.Endpoint == "" || s.Bucket == "" {
		return nil, errors.New("s3 endpoint and bucket are required")
	}
	client := awss3.New(awss3.Options{
		Region:       s.Region,
		BaseEndpoint: aws.String(s.Endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, ""),
		HTTPClient:   httpClient,
		// Keeps the bucket out of the hostname, so requests go to exactly the
		// host that was checked, and works on every S3-compatible service.
		UsePathStyle: true,
		// Many S3-compatible services reject the newer default checksum headers.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &Provider{client: client, bucket: s.Bucket, pageSize: defaultPageSize}, nil
}

// Check implements provider.Provider.
func (p *Provider) Check(ctx context.Context) error {
	if _, err := p.client.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: &p.bucket}); err != nil {
		return fmt.Errorf("reach bucket: %w", err)
	}
	return nil
}

// Put implements provider.Provider. A file that fits in one part goes up in a
// single request; anything larger goes up one part at a time, reusing one buffer.
func (p *Provider) Put(ctx context.Context, upload provider.Upload) (provider.Object, error) {
	if upload.Name == "" {
		return provider.Object{}, errors.New("object name is required")
	}
	key := objectPrefix + uuid.NewString() + "/" + escapeName(upload.Name)
	buffer := make([]byte, partSize)

	n, err := io.ReadFull(upload.Body, buffer)
	var size int64
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		_, err := p.client.PutObject(ctx, &awss3.PutObjectInput{
			Bucket:      &p.bucket,
			Key:         &key,
			ContentType: contentType(upload),
			Body:        bytes.NewReader(buffer[:n]),
		})
		if err != nil {
			return provider.Object{}, fmt.Errorf("put object: %w", err)
		}
		size = int64(n)
	case err != nil:
		return provider.Object{}, fmt.Errorf("read upload: %w", err)
	default:
		if size, err = p.putParts(ctx, key, upload, buffer); err != nil {
			return provider.Object{}, err
		}
	}
	return provider.Object{ID: key, Name: upload.Name, Size: size, ModifiedAt: time.Now()}, nil
}

// putParts sends a file whose first part is already in buffer.
func (p *Provider) putParts(ctx context.Context, key string, upload provider.Upload, buffer []byte) (size int64, err error) {
	created, err := p.client.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
		Bucket:      &p.bucket,
		Key:         &key,
		ContentType: contentType(upload),
	})
	if err != nil {
		return 0, fmt.Errorf("start multipart upload: %w", err)
	}
	defer func() {
		if err != nil {
			p.abort(ctx, key, created.UploadId)
		}
	}()

	var parts []types.CompletedPart
	filled := len(buffer)
	for number := int32(1); filled > 0; number++ {
		part, err := p.client.UploadPart(ctx, &awss3.UploadPartInput{
			Bucket:     &p.bucket,
			Key:        &key,
			UploadId:   created.UploadId,
			PartNumber: aws.Int32(number),
			Body:       bytes.NewReader(buffer[:filled]),
		})
		if err != nil {
			return 0, fmt.Errorf("upload part %d: %w", number, err)
		}
		parts = append(parts, types.CompletedPart{ETag: part.ETag, PartNumber: aws.Int32(number)})
		size += int64(filled)

		filled, err = io.ReadFull(upload.Body, buffer)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, fmt.Errorf("read upload: %w", err)
		}
	}

	_, err = p.client.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket:          &p.bucket,
		Key:             &key,
		UploadId:        created.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	})
	if err != nil {
		return 0, fmt.Errorf("finish multipart upload: %w", err)
	}
	return size, nil
}

// abort discards a failed upload's parts, which otherwise keep costing the
// user money. The request's context may already be cancelled, so it isn't used.
func (p *Provider) abort(ctx context.Context, key string, uploadID *string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortTimeout)
	defer cancel()
	_, _ = p.client.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{
		Bucket:   &p.bucket,
		Key:      &key,
		UploadId: uploadID,
	})
}

// Get implements provider.Provider.
func (p *Provider) Get(ctx context.Context, id string, rng *provider.ByteRange) (io.ReadCloser, error) {
	if !ownsKey(id) {
		return nil, provider.ErrObjectNotFound
	}
	input := &awss3.GetObjectInput{Bucket: &p.bucket, Key: &id}
	if rng != nil {
		input.Range = aws.String(fmt.Sprintf("bytes=%d-%d", rng.Start, rng.End))
	}
	out, err := p.client.GetObject(ctx, input)
	if isNotFound(err) {
		return nil, provider.ErrObjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	return out.Body, nil
}

// Delete implements provider.Provider. S3 reports success for a key that doesn't
// exist, so it looks first in order to keep to the contract.
func (p *Provider) Delete(ctx context.Context, id string) error {
	if !ownsKey(id) {
		return provider.ErrObjectNotFound
	}
	_, err := p.client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: &p.bucket, Key: &id})
	if isNotFound(err) {
		return provider.ErrObjectNotFound
	}
	if err != nil {
		return fmt.Errorf("find object: %w", err)
	}
	if _, err := p.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: &p.bucket, Key: &id}); err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// List implements provider.Provider.
func (p *Provider) List(ctx context.Context, pageToken string) (provider.ObjectPage, error) {
	input := &awss3.ListObjectsV2Input{
		Bucket:  &p.bucket,
		Prefix:  aws.String(objectPrefix),
		MaxKeys: aws.Int32(p.pageSize),
	}
	if pageToken != "" {
		input.ContinuationToken = &pageToken
	}
	out, err := p.client.ListObjectsV2(ctx, input)
	if err != nil {
		return provider.ObjectPage{}, fmt.Errorf("list objects: %w", err)
	}
	page := provider.ObjectPage{Objects: make([]provider.Object, 0, len(out.Contents))}
	for _, item := range out.Contents {
		key := aws.ToString(item.Key)
		if !ownsKey(key) {
			continue
		}
		page.Objects = append(page.Objects, provider.Object{
			ID:         key,
			Name:       nameFromKey(key),
			Size:       aws.ToInt64(item.Size),
			ModifiedAt: aws.ToTime(item.LastModified),
		})
	}
	if aws.ToBool(out.IsTruncated) {
		page.NextPageToken = aws.ToString(out.NextContinuationToken)
	}
	return page, nil
}

func contentType(upload provider.Upload) *string {
	if upload.ContentType == "" {
		return nil
	}
	return &upload.ContentType
}

// escapeName makes a name safe as one key segment. A name of only dots is
// escaped too, because some servers treat it as a path step.
func escapeName(name string) string {
	escaped := url.PathEscape(name)
	if strings.Trim(escaped, ".") == "" {
		return strings.ReplaceAll(escaped, ".", "%2E")
	}
	return escaped
}

func nameFromKey(key string) string {
	segment := key[strings.LastIndexByte(key, '/')+1:]
	name, err := url.PathUnescape(segment)
	if err != nil {
		return segment
	}
	return name
}

// ownsKey reports whether key has the exact shape Put creates, which keeps every
// operation inside dextro's area of the bucket.
func ownsKey(key string) bool {
	segments := strings.Split(key, "/")
	if len(segments) != 3 || segments[0]+"/" != objectPrefix {
		return false
	}
	_, err := uuid.Parse(segments[1])
	return err == nil && strings.Trim(segments[2], ".") != ""
}

func isNotFound(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	code := apiErr.ErrorCode()
	return code == "NoSuchKey" || code == "NotFound"
}
