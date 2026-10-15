package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
	"github.com/theimaginaryfoundation/what-iff/internal/telemetry"
	"github.com/theimaginaryfoundation/what-iff/internal/telemetry/telemetrytest"
	"go.uber.org/zap"
)

// fakeS3Stream records the calls a streamed upload makes and assembles completed objects.
type fakeS3Stream struct {
	calls     []string
	parts     map[int32][]byte
	objects   map[string][]byte
	partErrAt int32 // UploadPart fails for this part number when non-zero
	aborted   bool
	abortCtx  error // the abort context's Err() at call time
}

func newFakeS3Stream() *fakeS3Stream {
	return &fakeS3Stream{parts: map[int32][]byte{}, objects: map[string][]byte{}}
}

func (f *fakeS3Stream) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.calls = append(f.calls, "put")
	b, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.objects[aws.ToString(in.Key)] = b
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3Stream) CreateMultipartUpload(_ context.Context, in *s3.CreateMultipartUploadInput, _ ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error) {
	f.calls = append(f.calls, "create")
	return &s3.CreateMultipartUploadOutput{UploadId: aws.String("upload-1")}, nil
}

func (f *fakeS3Stream) UploadPart(_ context.Context, in *s3.UploadPartInput, _ ...func(*s3.Options)) (*s3.UploadPartOutput, error) {
	n := aws.ToInt32(in.PartNumber)
	f.calls = append(f.calls, fmt.Sprintf("part%d", n))
	if n == f.partErrAt {
		return nil, errors.New("part rejected")
	}
	b, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.parts[n] = b
	return &s3.UploadPartOutput{ETag: aws.String(fmt.Sprintf("etag-%d", n)), ChecksumCRC32: aws.String(fmt.Sprintf("crc-%d", n))}, nil
}

func (f *fakeS3Stream) CompleteMultipartUpload(_ context.Context, in *s3.CompleteMultipartUploadInput, _ ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error) {
	f.calls = append(f.calls, "complete")
	var obj []byte
	for i, p := range in.MultipartUpload.Parts {
		if aws.ToInt32(p.PartNumber) != int32(i+1) || aws.ToString(p.ETag) != fmt.Sprintf("etag-%d", i+1) ||
			aws.ToString(p.ChecksumCRC32) != fmt.Sprintf("crc-%d", i+1) {
			return nil, fmt.Errorf("part %d listed wrong", i+1)
		}
		obj = append(obj, f.parts[int32(i+1)]...)
	}
	f.objects[aws.ToString(in.Key)] = obj
	return &s3.CompleteMultipartUploadOutput{}, nil
}

func (f *fakeS3Stream) AbortMultipartUpload(ctx context.Context, _ *s3.AbortMultipartUploadInput, _ ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error) {
	f.calls = append(f.calls, "abort")
	f.aborted = true
	f.abortCtx = ctx.Err()
	return &s3.AbortMultipartUploadOutput{}, nil
}

func TestStreamToS3(t *testing.T) {
	const partSize = 8
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz") // 36 bytes → 4 full parts + 4

	tests := []struct {
		name  string
		data  []byte
		calls []string
	}{
		{"empty object is a plain put", nil, []string{"put"}},
		{"small object is a plain put", payload[:5], []string{"put"}},
		{"exactly one part", payload[:partSize], []string{"create", "part1", "complete"}},
		{"several parts with a short tail", payload, []string{"create", "part1", "part2", "part3", "part4", "part5", "complete"}},
		{"parts that end on a boundary", payload[:2*partSize], []string{"create", "part1", "part2", "complete"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newFakeS3Stream()
			// iotest-style one-byte reads prove the uploader fills whole parts from a trickling stream.
			r := &oneByteReader{r: bytes.NewReader(tt.data)}
			require.NoError(t, streamToS3(context.Background(), api, "bucket", "k", r, "application/zip", partSize))
			require.Equal(t, tt.calls, api.calls)
			require.Equal(t, string(tt.data), string(api.objects["k"]))
			for n, p := range api.parts {
				if int(n) < len(api.parts) {
					require.Len(t, p, partSize, "every part but the last is full")
				}
			}
		})
	}
}

// A reader that fails mid-stream aborts the multipart upload, and a part the store rejects does too,
// even when the caller's context is already done.
func TestStreamToS3AbortsOnFailure(t *testing.T) {
	const partSize = 8
	readErr := errors.New("zip build failed")

	t.Run("reader error", func(t *testing.T) {
		api := newFakeS3Stream()
		r := io.MultiReader(bytes.NewReader(make([]byte, 2*partSize+3)), &errReader{err: readErr})
		err := streamToS3(context.Background(), api, "bucket", "k", r, "", partSize)
		require.ErrorIs(t, err, readErr)
		require.True(t, api.aborted)
		require.Equal(t, []string{"create", "part1", "part2", "abort"}, api.calls)
		require.NotContains(t, api.objects, "k")
	})

	t.Run("reader error before one part fills", func(t *testing.T) {
		api := newFakeS3Stream()
		r := io.MultiReader(bytes.NewReader([]byte("abc")), &errReader{err: readErr})
		require.ErrorIs(t, streamToS3(context.Background(), api, "bucket", "k", r, "", partSize), readErr)
		require.Empty(t, api.calls, "nothing reaches the store")
	})

	t.Run("part rejected after the context ends", func(t *testing.T) {
		api := newFakeS3Stream()
		api.partErrAt = 2
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := streamToS3(ctx, api, "bucket", "k", bytes.NewReader(make([]byte, 3*partSize)), "", partSize)
		require.ErrorContains(t, err, "upload part 2")
		require.True(t, api.aborted)
		require.NoError(t, api.abortCtx, "the abort runs on its own context")
		require.NotContains(t, api.objects, "k")
	})
}

func TestLocalFileStoreUploadStream(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	fs := &localFileStore{dataDir: dir, logger: zap.NewNop()}

	require.NoError(t, fs.UploadStream(ctx, "exports/u/a.zip", &oneByteReader{r: bytes.NewReader([]byte("streamed"))}, "application/zip"))
	data, err := fs.DownloadFile(ctx, "exports/u/a.zip")
	require.NoError(t, err)
	require.Equal(t, "streamed", string(data))
	info, err := os.Stat(filepath.Join(dir, "exports", "u", "a.zip"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// A failed stream leaves nothing behind: no object and no temp file.
	err = fs.UploadStream(ctx, "exports/u/b.zip", io.MultiReader(bytes.NewReader([]byte("part")), &errReader{err: io.ErrClosedPipe}), "")
	require.ErrorIs(t, err, io.ErrClosedPipe)
	entries, err := os.ReadDir(filepath.Join(dir, "exports", "u"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "a.zip", entries[0].Name())
}

// Stores without StreamUploader (test doubles) get the stream read fully and passed to UploadFile;
// the instrumented wrapper streams through to a store that can, timed as put_object.
func TestUploadStreamFallbackAndInstrumented(t *testing.T) {
	tm := telemetrytest.UseGlobal(t)
	ctx := context.Background()

	rec := &recordingPlainStore{}
	require.NoError(t, UploadStream(ctx, rec, "k", bytes.NewReader([]byte("whole")), "text/plain"))
	require.Equal(t, "whole", string(rec.content))

	wrapped := Instrument(rec)
	_, ok := wrapped.(StreamUploader)
	require.True(t, ok)
	require.NoError(t, UploadStream(ctx, wrapped, "k", bytes.NewReader([]byte("again")), "text/plain"))
	require.Equal(t, "again", string(rec.content))

	local := Instrument(&localFileStore{dataDir: t.TempDir(), logger: zap.NewNop()})
	require.NoError(t, UploadStream(ctx, local, "users/u/s.txt", bytes.NewReader([]byte("s")), "text/plain"))
	require.Equal(t, uint64(1), tm.HistogramCount(t, telemetry.DependencyDuration.Name,
		telemetry.AttrDependency.String(telemetry.DependencyLocalFS), telemetry.AttrOperation.String("put_object")))
}

type recordingPlainStore struct{ content []byte }

func (s *recordingPlainStore) UploadFile(_ context.Context, _ string, content []byte, _ string) error {
	s.content = content
	return nil
}
func (s *recordingPlainStore) DownloadFile(context.Context, string) ([]byte, error) { return nil, nil }
func (s *recordingPlainStore) DeleteFile(context.Context, string) error             { return nil }

type oneByteReader struct{ r io.Reader }

func (o *oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

type errReader struct{ err error }

func (e *errReader) Read([]byte) (int, error) { return 0, e.err }
