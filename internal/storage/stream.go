package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"go.uber.org/zap"
)

const (
	// streamPartSize is the S3 multipart part size for UploadStream, and so the most a streamed
	// upload holds in memory at once. S3 requires every part but the last to be at least 5 MiB, and
	// allows 10,000 parts (~80 GiB at this size).
	streamPartSize = 8 << 20
	// streamAbortTimeout bounds the abort of a failed multipart upload. The abort runs even when the
	// caller's context is already done (a job timeout), so it gets its own deadline.
	streamAbortTimeout = 30 * time.Second
)

// StreamUploader stores an object read from r without holding the whole object in memory. A failed
// upload (including r returning an error) leaves no partial object at key. Implemented by the S3 and
// local stores; consumers go through UploadStream so the core FileStore interface (and its mocks)
// stay unchanged.
type StreamUploader interface {
	UploadStream(ctx context.Context, key string, r io.Reader, contentType string) error
}

var (
	_ StreamUploader = (*s3FileStore)(nil)
	_ StreamUploader = (*localFileStore)(nil)
	_ s3StreamAPI    = (*s3.Client)(nil)
)

// UploadStream stores r at key, streaming when fs implements StreamUploader. Other stores (test
// doubles) get r read fully and passed to UploadFile.
func UploadStream(ctx context.Context, fs FileStore, key string, r io.Reader, contentType string) error {
	if su, ok := fs.(StreamUploader); ok {
		return su.UploadStream(ctx, key, r, contentType)
	}
	content, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	return fs.UploadFile(ctx, key, content, contentType)
}

// ── S3 implementation ──────────────────────────────────────────────────────────

// s3StreamAPI is the subset of *s3.Client a streamed upload uses (narrowed for tests).
type s3StreamAPI interface {
	PutObject(ctx context.Context, in *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	CreateMultipartUpload(ctx context.Context, in *s3.CreateMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error)
	UploadPart(ctx context.Context, in *s3.UploadPartInput, optFns ...func(*s3.Options)) (*s3.UploadPartOutput, error)
	CompleteMultipartUpload(ctx context.Context, in *s3.CompleteMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error)
	AbortMultipartUpload(ctx context.Context, in *s3.AbortMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error)
}

// UploadStream uploads r in streamPartSize parts. An object that fits in one part is a plain
// PutObject; a failed multipart upload is aborted so no parts are left billed in the bucket.
func (s *s3FileStore) UploadStream(ctx context.Context, key string, r io.Reader, contentType string) error {
	if err := streamToS3(ctx, s.client, s.bucketName, key, r, contentType, streamPartSize); err != nil {
		return fmt.Errorf("s3 stream upload s3://%s/%s: %w", s.bucketName, key, err)
	}
	s.logger.Debug("streamed file to S3", zap.String("key", key))
	return nil
}

func streamToS3(ctx context.Context, api s3StreamAPI, bucket, key string, r io.Reader, contentType string, partSize int) (err error) {
	buf := make([]byte, partSize)
	n, readErr := io.ReadFull(r, buf)
	if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
		_, err := api.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(bucket),
			Key:         aws.String(key),
			Body:        bytes.NewReader(buf[:n]),
			ContentType: aws.String(contentType),
		})
		return err
	}
	if readErr != nil {
		return readErr
	}

	// CRC32 matches the SDK's default part checksum; once named here, Complete must list each part's.
	created, err := api.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:            aws.String(bucket),
		Key:               aws.String(key),
		ContentType:       aws.String(contentType),
		ChecksumAlgorithm: s3types.ChecksumAlgorithmCrc32,
	})
	if err != nil {
		return fmt.Errorf("create multipart upload: %w", err)
	}
	uploadID := created.UploadId
	defer func() {
		if err == nil {
			return
		}
		abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), streamAbortTimeout)
		defer cancel()
		if _, abortErr := api.AbortMultipartUpload(abortCtx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: uploadID,
		}); abortErr != nil {
			err = errors.Join(err, fmt.Errorf("abort multipart upload: %w", abortErr))
		}
	}()

	var parts []s3types.CompletedPart
	for partNumber := int32(1); ; partNumber++ {
		out, err := api.UploadPart(ctx, &s3.UploadPartInput{
			Bucket:            aws.String(bucket),
			Key:               aws.String(key),
			UploadId:          uploadID,
			PartNumber:        aws.Int32(partNumber),
			Body:              bytes.NewReader(buf[:n]),
			ChecksumAlgorithm: s3types.ChecksumAlgorithmCrc32,
		})
		if err != nil {
			return fmt.Errorf("upload part %d: %w", partNumber, err)
		}
		parts = append(parts, s3types.CompletedPart{
			ETag:          out.ETag,
			ChecksumCRC32: out.ChecksumCRC32,
			PartNumber:    aws.Int32(partNumber),
		})
		if readErr != nil { // that was the short final part
			break
		}
		n, readErr = io.ReadFull(r, buf)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return readErr
		}
	}

	if _, err := api.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(bucket),
		Key:             aws.String(key),
		UploadId:        uploadID,
		MultipartUpload: &s3types.CompletedMultipartUpload{Parts: parts},
	}); err != nil {
		return fmt.Errorf("complete multipart upload: %w", err)
	}
	return nil
}

// ── Local implementation (dev/test) ─────────────────────────────────────────────

// UploadStream copies r to a temp file beside the destination and renames it into place, so a
// failed stream leaves nothing at key.
func (l *localFileStore) UploadStream(_ context.Context, key string, r io.Reader, _ string) (err error) {
	fullPath := filepath.Join(l.dataDir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		return fmt.Errorf("local file store mkdir for %s: %w", key, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(fullPath), ".upload-*") // created 0600
	if err != nil {
		return fmt.Errorf("local file store temp for %s: %w", key, err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := io.Copy(tmp, r); err != nil {
		return fmt.Errorf("local file store write %s: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("local file store close %s: %w", key, err)
	}
	if err := os.Rename(tmp.Name(), fullPath); err != nil {
		return fmt.Errorf("local file store rename %s: %w", key, err)
	}
	l.logger.Debug("streamed file to local store", zap.String("key", key), zap.String("path", fullPath))
	return nil
}
