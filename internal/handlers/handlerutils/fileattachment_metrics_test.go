package handlerutils

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"github.com/theimaginaryfoundation/what-iff/internal/telemetry"
	"github.com/theimaginaryfoundation/what-iff/internal/telemetry/telemetrytest"
	"go.uber.org/zap"
)

type failingFileStore struct{}

func (failingFileStore) UploadFile(context.Context, string, []byte, string) error {
	return errors.New("s3 down")
}
func (failingFileStore) DownloadFile(context.Context, string) ([]byte, error) { return nil, nil }
func (failingFileStore) DeleteFile(context.Context, string) error             { return nil }

func uploadCount(tm *telemetrytest.Recorder, t *testing.T, kind, outcome string) int64 {
	return tm.CounterValue(t, telemetry.FileUploads.Name,
		telemetry.AttrKind.String(kind), telemetry.AttrOutcome.String(outcome))
}

// A successful upload records its size and kind, and is counted once as a success: by
// TriggerAsyncFileChunking, the step every upload path runs after storing the attachment.
func TestUploadFileAttachmentMetricsCountSuccessOnce(t *testing.T) {
	tm := telemetrytest.UseGlobal(t)
	payload := []byte("hello, world")

	attachment, tempPath, err := UploadFileAttachment(httptest.NewRecorder(),
		multipartUploadRequest(t, "notes.txt", payload), zap.NewNop(), uuid.New())
	require.NoError(t, err)
	defer os.Remove(tempPath)

	upload := telemetry.AttrOperation.String(telemetry.FileOpUpload)
	require.Equal(t, uint64(1), tm.HistogramCount(t, telemetry.FileSize.Name, upload, telemetry.AttrKind.String("text")))
	require.Equal(t, float64(len(payload)), tm.HistogramSum(t, telemetry.FileSize.Name, upload))
	require.Zero(t, tm.CounterValue(t, telemetry.FileUploads.Name), "success is counted later, not by UploadFileAttachment")

	TriggerAsyncFileChunking(zap.NewNop(), uuid.New(), nil, uuid.New(), "", attachment.Name)
	require.Equal(t, int64(1), uploadCount(tm, t, "text", telemetry.FileUploadSuccess))
	require.Equal(t, int64(1), tm.CounterValue(t, telemetry.FileUploads.Name))
}

func TestUploadFileAttachmentMetricsCountFailures(t *testing.T) {
	tm := telemetrytest.UseGlobal(t)

	// An image that cannot be decoded fails during normalization, after its type is known.
	_, _, err := UploadFileAttachment(httptest.NewRecorder(),
		multipartUploadRequest(t, "pic.png", []byte("this is not a png")), zap.NewNop(), uuid.New())
	require.Error(t, err)
	require.Equal(t, int64(1), uploadCount(tm, t, "image", telemetry.FileUploadFailure))

	// An unrecognised extension fails before the type is known, so it is counted as "other".
	_, _, err = UploadFileAttachment(httptest.NewRecorder(),
		multipartUploadRequest(t, "binary.exe", []byte("MZ")), zap.NewNop(), uuid.New())
	require.Error(t, err)
	require.Equal(t, int64(1), uploadCount(tm, t, "other", telemetry.FileUploadFailure))

	// An object store failure after a good upload is also a failed upload.
	tmp, err := os.CreateTemp(t.TempDir(), "upload-*")
	require.NoError(t, err)
	require.NoError(t, tmp.Close())
	require.Error(t, UploadToS3(context.Background(), failingFileStore{}, "key", tmp.Name(), "text/plain"))
	require.Equal(t, int64(1), uploadCount(tm, t, "text", telemetry.FileUploadFailure))
	require.Zero(t, tm.CounterValue(t, telemetry.FileUploads.Name, telemetry.AttrOutcome.String(telemetry.FileUploadSuccess)))
}

func TestUploadFileAttachmentRecordsImageNormalizeStage(t *testing.T) {
	tm := telemetrytest.UseGlobal(t)
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 64, 32))))

	_, tempPath, err := UploadFileAttachment(httptest.NewRecorder(),
		multipartUploadRequest(t, "pic.png", buf.Bytes()), zap.NewNop(), uuid.New())
	require.NoError(t, err)
	defer os.Remove(tempPath)

	require.Equal(t, uint64(1), tm.HistogramCount(t, telemetry.FileOperationDuration.Name,
		telemetry.AttrOperation.String(telemetry.FileOpUpload), telemetry.AttrStage.String(telemetry.FileStageNormalize)))
	require.Equal(t, uint64(1), tm.HistogramCount(t, telemetry.FileSize.Name, telemetry.AttrKind.String("image")))
}

// When the attachment record can't be saved after a good upload, the temp file is cleaned up and
// the upload is counted once, as a failure.
func TestAbandonFileAttachmentUpload(t *testing.T) {
	tm := telemetrytest.UseGlobal(t)

	attachment, tempPath, err := UploadFileAttachment(httptest.NewRecorder(),
		multipartUploadRequest(t, "notes.txt", []byte("hi")), zap.NewNop(), uuid.New())
	require.NoError(t, err)

	// The request context is often what ended (client gone); cleanup must still run.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	AbandonFileAttachmentUpload(ctx, attachment, tempPath)

	_, statErr := os.Stat(tempPath)
	require.True(t, os.IsNotExist(statErr), "temp file is removed")
	require.Equal(t, int64(1), uploadCount(tm, t, "text", telemetry.FileUploadFailure))
	require.Equal(t, int64(1), tm.CounterValue(t, telemetry.FileUploads.Name))
}

// Abandoning an upload that never produced a temp file (the path is empty) just counts it.
func TestAbandonFileAttachmentUpload_WithoutATempFile(t *testing.T) {
	tm := telemetrytest.UseGlobal(t)

	require.NotPanics(t, func() {
		AbandonFileAttachmentUpload(context.Background(), models.FileAttachment{FileType: "text/plain"}, "")
	})
	require.Equal(t, int64(1), uploadCount(tm, t, "text", telemetry.FileUploadFailure))
}
