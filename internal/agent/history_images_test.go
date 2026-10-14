package agent

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/theimaginaryfoundation/what-iff/internal/agent/provider"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"github.com/theimaginaryfoundation/what-iff/internal/storage"
)

type historyImageStore struct{ content []byte }

func (s historyImageStore) UploadFile(context.Context, string, []byte, string) error { return nil }
func (s historyImageStore) DownloadFile(context.Context, string) ([]byte, error) {
	return s.content, nil
}
func (s historyImageStore) DeleteFile(context.Context, string) error { return nil }

func historyImageMessage() *models.ChatMessage {
	return &models.ChatMessage{
		Attachments: []*models.FileAttachment{
			{ID: uuid.New(), Name: "a.png", FileType: "image/png"},
		},
	}
}

func TestHistoryImagesForMessage_LoadsBytesFromTheStore(t *testing.T) {
	t.Parallel()

	var store storage.FileStore = historyImageStore{content: []byte("hello")}
	imgs := historyImagesForMessage(t.Context(), nil, store, uuid.New(), uuid.New(), historyImageMessage(), true)
	require.Len(t, imgs, 1)
	require.Equal(t, []byte("hello"), imgs[0].RawBytes)
}

// Nothing but the bytes can be sent, so with loading off a history image is left out (and the
// store is not touched).
func TestHistoryImagesForMessage_NoImagesWhenBytesNotLoaded(t *testing.T) {
	t.Parallel()

	var store storage.FileStore = historyImageStore{content: []byte("hello")}
	require.Empty(t, historyImagesForMessage(t.Context(), nil, store, uuid.New(), uuid.New(), historyImageMessage(), false))
}

// An image whose object is missing from the store is skipped rather than sent empty.
func TestHistoryImagesForMessage_SkipsImagesMissingFromTheStore(t *testing.T) {
	t.Parallel()

	var store storage.FileStore = historyImageStore{}
	require.Empty(t, historyImagesForMessage(t.Context(), nil, store, uuid.New(), uuid.New(), historyImageMessage(), true))
}

func TestAppendHistoryTurn_WithImages(t *testing.T) {
	t.Parallel()

	var ctx provider.ModelContext
	ctx.AppendHistoryTurn(provider.RoleUser, "look at this", []provider.UserMessageImage{
		{MediaType: "image/png", RawBytes: []byte{1}},
	}, true)
	require.Len(t, ctx.Segments, 1)
	require.Len(t, ctx.Segments[0].UserImages, 1)
}
