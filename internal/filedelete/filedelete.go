// Package filedelete deletes a user's file attachments with the cleanup each kind needs, so the
// single-file routes (DELETE /image-gallery/{id}, DELETE /file-attachment/{id}) and the gallery's
// bulk delete (POST /image-gallery/delete) share one implementation.
package filedelete

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/theimaginaryfoundation/what-iff/internal/datastore"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"github.com/theimaginaryfoundation/what-iff/internal/storage"
	"go.uber.org/zap"
)

var (
	// ErrNotFound means the id is not one of the caller's file attachments (or does not exist).
	ErrNotFound = errors.New("file not found")
	// ErrNotImage means DeleteImage was asked to delete something that is not an image.
	ErrNotImage = errors.New("not an image attachment")
)

// Stage names the step of a delete that failed, so a route can keep its own status and message.
type Stage string

const (
	// StageLookup is reading the row (any failure other than ErrNotFound).
	StageLookup Stage = "lookup"
	// StageStorage is removing an image's full-resolution object, which happens before the row goes.
	StageStorage Stage = "storage"
	// StageRecord is deleting the row itself.
	StageRecord Stage = "record"
)

// StepError is a delete that failed partway, at Stage.
type StepError struct {
	Stage Stage
	Err   error
}

func (e *StepError) Error() string { return string(e.Stage) + ": " + e.Err.Error() }
func (e *StepError) Unwrap() error { return e.Err }

// StageOf returns the stage a delete error happened at, or "" when it is not a StepError.
func StageOf(err error) Stage {
	var step *StepError
	if errors.As(err, &step) {
		return step.Stage
	}
	return ""
}

// Store is the datastore surface deletes need; *datastore.Datastore satisfies it.
type Store interface {
	GetFileAttachment(ctx context.Context, userID, id uuid.UUID) (*models.FileAttachment, error)
	DeleteFileAttachment(ctx context.Context, userID, id uuid.UUID) error
	// Answers which object keys remaining rows still reference (storage.AttachmentKeyRefs).
	storage.AttachmentKeyRefs
}

// Service deletes file attachments. A nil file store skips object cleanup.
type Service struct {
	ds     Store
	files  storage.FileStore
	logger *zap.Logger
}

// New returns a Service. files may be nil (see Service).
func New(ds Store, files storage.FileStore, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{ds: ds, files: files, logger: logger}
}

// DeleteImage deletes an image the way DELETE /image-gallery/{id} always has: the full-resolution
// object first (a failure stops the delete, so the row and storage do not drift), the thumbnail
// best-effort, then the row. Anything that is not an image is ErrNotImage.
func (s *Service) DeleteImage(ctx context.Context, userID, id uuid.UUID) error {
	att, err := s.lookup(ctx, userID, id)
	if err != nil {
		return err
	}
	if !isImage(att) {
		return ErrNotImage
	}
	return s.deleteImage(ctx, userID, att)
}

// DeleteFile deletes any attachment the way DELETE /file-attachment/{id} always has: the row, then
// (best-effort, even if the caller hangs up) the stored objects no remaining row references.
func (s *Service) DeleteFile(ctx context.Context, userID, id uuid.UUID) error {
	att, err := s.lookup(ctx, userID, id)
	if err != nil {
		return err
	}
	return s.deleteFile(ctx, userID, att)
}

// Delete deletes one gallery file the way the web app's single delete does for its kind: images as
// DeleteImage, everything else as DeleteFile.
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	att, err := s.lookup(ctx, userID, id)
	if err != nil {
		return err
	}
	if isImage(att) {
		return s.deleteImage(ctx, userID, att)
	}
	return s.deleteFile(ctx, userID, att)
}

func (s *Service) lookup(ctx context.Context, userID, id uuid.UUID) (*models.FileAttachment, error) {
	att, err := s.ds.GetFileAttachment(ctx, userID, id)
	switch {
	case err == nil:
		return att, nil
	case errors.Is(err, datastore.ErrFileAttachmentNotFound):
		return nil, ErrNotFound
	default:
		return nil, &StepError{Stage: StageLookup, Err: err}
	}
}

func (s *Service) deleteImage(ctx context.Context, userID uuid.UUID, att *models.FileAttachment) error {
	if s.files != nil {
		// Use the stored key when there is one, else the key images were stored under before it.
		fullKey := att.S3Key
		if fullKey == "" {
			fullKey = storage.FileKeyForImage(userID, att.ID, att.Name)
		}
		if err := s.files.DeleteFile(ctx, fullKey); err != nil {
			s.logger.Error("file delete: failed to delete full image object", zap.String("key", fullKey), zap.Error(err))
			return &StepError{Stage: StageStorage, Err: err}
		}
		thumbKey := storage.FileKeyForImageThumbnail(userID, att.ID)
		if err := s.files.DeleteFile(ctx, thumbKey); err != nil {
			s.logger.Warn("file delete: failed to delete image thumbnail", zap.String("key", thumbKey), zap.Error(err))
		}
	}
	if err := s.ds.DeleteFileAttachment(ctx, userID, att.ID); err != nil {
		s.logger.Error("file delete: failed to delete image row", zap.String("id", att.ID.String()), zap.Error(err))
		return &StepError{Stage: StageRecord, Err: err}
	}
	return nil
}

func (s *Service) deleteFile(ctx context.Context, userID uuid.UUID, att *models.FileAttachment) error {
	if err := s.ds.DeleteFileAttachment(ctx, userID, att.ID); err != nil {
		return &StepError{Stage: StageRecord, Err: err}
	}
	// The row is gone, so its objects go too unless a reference copy still points at them.
	// WithoutCancel so a client hanging up does not abandon the cleanup halfway.
	storage.ReleaseAttachmentObjects(context.WithoutCancel(ctx), s.logger, s.files, s.ds, userID, []models.FileAttachment{*att})
	return nil
}

func isImage(att *models.FileAttachment) bool {
	return strings.HasPrefix(att.FileType, models.ImageMIMEPrefix)
}
