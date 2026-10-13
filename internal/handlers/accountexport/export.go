package accountexport

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/theimaginaryfoundation/what-iff/internal/email"
	"github.com/theimaginaryfoundation/what-iff/internal/exporter"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"github.com/theimaginaryfoundation/what-iff/internal/storage"
	"github.com/theimaginaryfoundation/what-iff/internal/telemetry"
)

// runAccountExport streams the export ZIP into storage as it is built, presigns a link, and emails
// it. All failures are recorded on the job and swallowed so a panic never takes down the request
// path. The download link is emailed and never written to the job (email-only delivery is the
// security control).
// The run is tracked as an account_export job, with build/email stage timings, the archive size
// and per-section counts on the file metrics.
func (h *Handler) runAccountExport(ctx context.Context, userID, jobID uuid.UUID) {
	metrics := telemetry.Global()
	finishJob := metrics.TrackJob(ctx, models.JobTypeAccountExport)
	// Every early return below is a failure; fail() distinguishes cancelled/timeout from failed.
	// finishJob is deferred first so it runs after the recover below and sees a panic outcome.
	outcome := telemetry.JobOutcomeFailed
	defer func() { finishJob(outcome) }()
	fail := func(err error) { outcome = telemetry.JobOutcomeFromError(err) }
	defer func() {
		if v := recover(); v != nil {
			outcome = telemetry.JobOutcomePanic
			h.logger.Error("account export: panic in build",
				zap.String("job_id", jobID.String()), zap.Any("panic", v), zap.ByteString("stack", debug.Stack()))
			h.failExport(ctx, userID, jobID, "Export failed unexpectedly")
		}
	}()

	if _, err := h.ds.UpdateJobStatus(ctx, userID, jobID, models.JobStatusProcessing, ""); err != nil {
		h.logger.Warn("account export: mark processing failed", zap.String("job_id", jobID.String()), zap.Error(err))
	}
	h.setProgress(ctx, userID, jobID, models.AccountExportProgress{Phase: models.AccountExportPhaseBuilding})

	userEmail, username, err := h.ds.ExportAccountIdentity(ctx, userID)
	if err != nil {
		h.logger.Error("account export: identity load failed", zap.String("job_id", jobID.String()), zap.Error(err))
		fail(err)
		h.failExport(ctx, userID, jobID, "Failed to build export")
		return
	}

	// The archive streams into the upload as it is built, so build_zip covers the upload too (the
	// two interleave). Size and item counts are recorded, and the uploading phase set, once the
	// archive is complete and only its final part is still in flight.
	key := path.Join(bundlePrefix, userID.String(), fmt.Sprintf("account-export-%s.zip", time.Now().UTC().Format("20060102-150405")))
	doneBuild := metrics.TimeFileStage(ctx, telemetry.FileOpAccountExport, telemetry.FileStageBuildZip)
	counts, buildErr, uploadErr := h.streamAccountZip(ctx, h.ds, key, userID, userEmail, username, func(size int64, counts map[string]int) {
		metrics.RecordFileSize(ctx, telemetry.FileOpAccountExport, telemetry.FileKind("application/zip"), size)
		recordAccountExportItems(ctx, metrics, counts)
		h.setProgress(ctx, userID, jobID, models.AccountExportProgress{Phase: models.AccountExportPhaseUploading, Counts: counts})
	})
	doneBuild(buildErr)
	if buildErr != nil {
		fail(buildErr)
		h.logger.Error("account export: build failed", zap.String("job_id", jobID.String()), zap.Error(buildErr))
		h.failExport(ctx, userID, jobID, "Failed to build export")
		return
	}
	if uploadErr != nil {
		h.logger.Error("account export: upload failed", zap.String("job_id", jobID.String()), zap.Error(uploadErr))
		fail(uploadErr)
		h.failExport(ctx, userID, jobID, "Failed to store export")
		return
	}

	url, expiresAt, err := h.presignBundle(ctx, key)
	if err != nil {
		h.logger.Error("account export: presign failed", zap.String("job_id", jobID.String()), zap.Error(err))
		fail(err)
		h.failExport(ctx, userID, jobID, "Failed to prepare download link")
		return
	}

	// Email is the sole delivery channel; if it fails the user can't get the link, so fail the job.
	doneEmail := metrics.TimeFileStage(ctx, telemetry.FileOpAccountExport, telemetry.FileStageEmail)
	err = h.sender.SendExportReady(ctx, userEmail, email.ExportReadyData{
		Username:   username,
		BundleURL:  url,
		ExpiresAt:  expiresAt,
		FilesCount: counts["files"],
	})
	doneEmail(err)
	if err != nil {
		h.logger.Error("account export: email send failed", zap.String("job_id", jobID.String()), zap.Error(err))
		fail(err)
		// The link is undeliverable, so the uploaded archive is unreachable — clean it up rather than
		// leaving an orphan (best effort; an S3 lifecycle rule is the backstop).
		if delErr := h.fileStore.DeleteFile(ctx, key); delErr != nil {
			h.logger.Warn("account export: failed to remove archive after email failure", zap.String("key", key), zap.Error(delErr))
		}
		h.failExport(ctx, userID, jobID, "Export built but the notification email could not be sent")
		return
	}

	outcome = telemetry.JobOutcomeSuccess
	h.setProgress(ctx, userID, jobID, models.AccountExportProgress{
		Phase:   models.AccountExportPhaseComplete,
		Counts:  counts,
		Message: "Export ready — a download link has been emailed to you.",
	})
	if _, err := h.ds.UpdateJobStatus(ctx, userID, jobID, models.JobStatusComplete, ""); err != nil {
		h.logger.Warn("account export: mark complete failed", zap.String("job_id", jobID.String()), zap.Error(err))
	}
	h.ds.AuditAccountExport(ctx, userID, "completed", "account export delivered by email", map[string]any{
		"job_id": jobID.String(),
		"counts": counts,
	})
	h.logger.Info("account export complete", zap.String("job_id", jobID.String()), zap.String("key", key))
}

// accountExportItemKinds maps the export's section counts to FileOperationItems kinds.
var accountExportItemKinds = map[string]string{
	"conversations": telemetry.FileItemConversation,
	"personalities": telemetry.FileItemPersonality,
	"files":         telemetry.FileItemFile,
}

// recordAccountExportItems records how many items of each section one export wrote.
func recordAccountExportItems(ctx context.Context, metrics *telemetry.Metrics, counts map[string]int) {
	for section, kind := range accountExportItemKinds {
		metrics.RecordFileItems(ctx, telemetry.FileOpAccountExport, kind, telemetry.FileItemExported, counts[section])
	}
}

// accountExportSource is the account data the export reads. *datastore.Datastore satisfies it;
// tests substitute a fake.
type accountExportSource interface {
	ForEachExportConversation(ctx context.Context, userID uuid.UUID, fn func(exporter.ConversationInput) error) error
	ExportPersonalityInputs(ctx context.Context, userID uuid.UUID) ([]exporter.PersonalityInput, error)
	ExportMemories(ctx context.Context, userID uuid.UUID, w io.Writer) error
}

// errExportUploadStopped is what the archive writer sees when the upload stops reading early (it
// failed); the upload's own error is the one reported.
var errExportUploadStopped = errors.New("account export upload stopped")

// streamAccountZip builds the export archive straight into a streaming upload of key: the zip
// writer feeds one end of a pipe and storage.UploadStream reads the other, so memory stays at about
// one upload part plus compressor state whatever the account's size. built runs once the archive is
// complete, with its size and section counts, while the upload's final part is still in flight.
// A failed build or upload leaves nothing at key (the store aborts the partial upload).
func (h *Handler) streamAccountZip(ctx context.Context, src accountExportSource, key string, userID uuid.UUID, userEmail, username string, built func(size int64, counts map[string]int)) (counts map[string]int, buildErr, uploadErr error) {
	pr, pw := io.Pipe()
	uploaded := make(chan error, 1)
	go func() {
		var err error
		defer func() {
			if v := recover(); v != nil {
				err = fmt.Errorf("upload panicked: %v", v)
			}
			// Unblocks the archive writer if the upload stopped before EOF; a no-op after it.
			pr.CloseWithError(errExportUploadStopped)
			uploaded <- err
		}()
		err = storage.UploadStream(ctx, h.fileStore, key, pr, "application/zip")
	}()
	// If the build panics, end the stream so the upload aborts instead of waiting forever. After
	// a normal close below this is a no-op.
	defer pw.CloseWithError(errors.New("account export build aborted"))

	cw := &countingWriter{w: pw}
	counts, buildErr = h.writeAccountZip(ctx, src, cw, userID, userEmail, username)
	// A nil buildErr ends the stream at EOF, so the upload completes; otherwise it aborts.
	pw.CloseWithError(buildErr)
	if buildErr == nil {
		built(cw.n, counts)
	}
	uploadErr = <-uploaded
	switch {
	case buildErr == nil:
		return counts, nil, uploadErr
	case uploadErr != nil && !errors.Is(uploadErr, buildErr):
		// The upload failed first and the build only saw the closed pipe; a build that fails first
		// reaches the upload through the pipe, so its error is wrapped in uploadErr.
		return nil, nil, uploadErr
	default:
		return nil, buildErr, nil
	}
}

// writeAccountZip writes the export archive to w and returns its section counts. Conversations
// stream one at a time and memories stream into their nested archive, so neither is held whole. The
// account identity is passed in (already loaded by the caller) to stamp the manifest.
func (h *Handler) writeAccountZip(ctx context.Context, src accountExportSource, w io.Writer, userID uuid.UUID, userEmail, username string) (map[string]int, error) {
	zw := zip.NewWriter(w)
	tree := exporter.NewZipTree(zw)

	// The datastore yields chats in (created_at, id) order, the order conversations.json is sorted in.
	convWriter, err := tree.Create(exporter.ConversationsFile)
	if err != nil {
		return nil, err
	}
	conversations := exporter.NewConversationsWriter(convWriter)
	convCount := 0
	if err := src.ForEachExportConversation(ctx, userID, func(c exporter.ConversationInput) error {
		convCount++
		return conversations.Add(c)
	}); err != nil {
		return nil, fmt.Errorf("conversations: %w", err)
	}
	if err := conversations.Close(); err != nil {
		return nil, err
	}

	personas, err := src.ExportPersonalityInputs(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("personalities: %w", err)
	}
	if err := exporter.WritePersonalities(tree, personas); err != nil {
		return nil, err
	}
	if err := exporter.WriteSillyTavernCards(tree, personas, username); err != nil {
		return nil, err
	}

	// Memories ride as a nested memories.zip in the existing memory-export format, so they re-import
	// through the same ds.ImportMemories path (embedding regeneration included). ExportMemories
	// streams its archive straight into the entry.
	memWriter, err := tree.Create("memories.zip")
	if err != nil {
		return nil, err
	}
	if err := src.ExportMemories(ctx, userID, memWriter); err != nil {
		return nil, fmt.Errorf("memories: %w", err)
	}

	filesPrefix := path.Join("users", userID.String()) + "/"
	fileObjs, err := h.listUserFiles(ctx, filesPrefix)
	if err != nil {
		return nil, fmt.Errorf("files: %w", err)
	}
	if err := exporter.WriteFilesManifest(tree, filesPrefix, fileObjs); err != nil {
		return nil, err
	}

	counts := map[string]int{
		"conversations": convCount,
		"personalities": len(personas),
		"files":         len(fileObjs),
	}
	if err := exporter.WriteManifest(tree, exporter.Manifest{
		ExportedAt: time.Now().UTC(),
		Account:    exporter.ManifestAccount{UserID: userID.String(), Email: userEmail, Username: username},
		Counts:     counts,
	}); err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalize zip: %w", err)
	}
	return counts, nil
}

// countingWriter counts the bytes written through it (the streamed archive's size).
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// listUserFiles enumerates the user's raw file objects when the store supports listing.
func (h *Handler) listUserFiles(ctx context.Context, prefix string) ([]exporter.FileObject, error) {
	lister, ok := h.fileStore.(storage.ObjectLister)
	if !ok {
		return nil, nil
	}
	objs, err := lister.ListObjects(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]exporter.FileObject, 0, len(objs))
	for _, o := range objs {
		out = append(out, exporter.FileObject{Key: o.Key, Size: o.Size, ContentType: o.ContentType, ETag: o.ETag})
	}
	return out, nil
}

// presignBundle returns a presigned download URL and its expiry.
func (h *Handler) presignBundle(ctx context.Context, key string) (string, time.Time, error) {
	signer, ok := h.fileStore.(storage.Presigner)
	if !ok {
		return "", time.Time{}, fmt.Errorf("file store does not support presigned URLs")
	}
	url, err := signer.PresignGetObject(ctx, key, presignTTL)
	if err != nil {
		return "", time.Time{}, err
	}
	return url, time.Now().Add(presignTTL), nil
}

func (h *Handler) setProgress(ctx context.Context, userID, jobID uuid.UUID, p models.AccountExportProgress) {
	b, err := json.Marshal(p)
	if err != nil {
		return
	}
	if err := h.ds.UpdateJobProgress(ctx, userID, jobID, string(b)); err != nil {
		h.logger.Warn("account export: progress update failed", zap.String("job_id", jobID.String()), zap.Error(err))
	}
}

func (h *Handler) failExport(ctx context.Context, userID, jobID uuid.UUID, msg string) {
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
	}
	h.setProgress(ctx, userID, jobID, models.AccountExportProgress{Phase: models.AccountExportPhaseFailed, Message: msg})
	if _, err := h.ds.UpdateJobStatus(ctx, userID, jobID, models.JobStatusFailed, msg); err != nil {
		h.logger.Warn("account export: mark failed failed", zap.String("job_id", jobID.String()), zap.Error(err))
	}
	h.ds.AuditAccountExport(ctx, userID, "failed", "account export failed", map[string]any{
		"job_id": jobID.String(),
		"reason": msg,
	})
}
