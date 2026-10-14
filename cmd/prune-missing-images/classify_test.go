package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"github.com/theimaginaryfoundation/what-iff/internal/storage"
)

// fakeStore answers downloads from objects; a key in errs fails the lookup.
type fakeStore struct {
	objects map[string][]byte
	errs    map[string]error
	asked   []string
}

func (f *fakeStore) download(_ context.Context, key string) ([]byte, error) {
	f.asked = append(f.asked, key)
	if err := f.errs[key]; err != nil {
		return nil, err
	}
	return f.objects[key], nil // nil, nil when absent, as the real stores answer
}

func legacyImage(userID uuid.UUID) *models.FileAttachment {
	chat := uuid.New()
	return &models.FileAttachment{ID: uuid.New(), UserID: userID, Name: "cat.png", FileType: "image/png", ChatID: &chat}
}

func TestClassifyFindsTheImageUnderTheGalleryKey(t *testing.T) {
	user := uuid.New()
	att := legacyImage(user)
	key := storage.FileKeyForImage(user, att.ID, att.Name)
	store := &fakeStore{objects: map[string][]byte{key: []byte("png")}}

	v, found, err := classify(t.Context(), store.download, user, att)

	require.NoError(t, err)
	assert.Equal(t, verdictPresent, v)
	assert.Equal(t, key, found, "the key to backfill into s3_key")
	assert.Equal(t, []string{key}, store.asked, "stops at the first hit")
}

func TestClassifyFindsAnImageFiledUnderTheChat(t *testing.T) {
	user := uuid.New()
	att := legacyImage(user)
	chatKey := storage.FileKeyForChat(user, *att.ChatID, att.ID, att.Name)
	store := &fakeStore{objects: map[string][]byte{chatKey: []byte("png")}}

	v, found, err := classify(t.Context(), store.download, user, att)

	require.NoError(t, err)
	assert.Equal(t, verdictPresent, v)
	assert.Equal(t, chatKey, found)
}

func TestClassifyIsMissingOnlyWhenEveryKeyAnswersNotFound(t *testing.T) {
	user := uuid.New()
	att := legacyImage(user)
	store := &fakeStore{}

	v, found, err := classify(t.Context(), store.download, user, att)

	require.NoError(t, err)
	assert.Equal(t, verdictMissing, v)
	assert.Empty(t, found)
	assert.GreaterOrEqual(t, len(store.asked), 2, "every place the image could live was checked")
}

// A failed lookup might have been the one key that holds the image, so it can never be missing.
func TestClassifyNeverCallsAFailedLookupMissing(t *testing.T) {
	user := uuid.New()
	att := legacyImage(user)
	galleryKey := storage.FileKeyForImage(user, att.ID, att.Name)
	store := &fakeStore{errs: map[string]error{galleryKey: errors.New("access denied")}}

	v, _, err := classify(t.Context(), store.download, user, att)

	assert.Equal(t, verdictUnverified, v)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access denied")
}

// A hit on a later key settles it even if an earlier lookup failed.
func TestClassifyPrefersFoundBytesOverAnEarlierLookupError(t *testing.T) {
	user := uuid.New()
	att := legacyImage(user)
	galleryKey := storage.FileKeyForImage(user, att.ID, att.Name)
	chatKey := storage.FileKeyForChat(user, *att.ChatID, att.ID, att.Name)
	store := &fakeStore{
		errs:    map[string]error{galleryKey: errors.New("timeout")},
		objects: map[string][]byte{chatKey: []byte("png")},
	}

	v, found, err := classify(t.Context(), store.download, user, att)

	require.NoError(t, err)
	assert.Equal(t, verdictPresent, v)
	assert.Equal(t, chatKey, found)
}

func TestCandidateKeysAreDeduplicatedAndSkipEmptyOnes(t *testing.T) {
	user := uuid.New()
	att := &models.FileAttachment{ID: uuid.New(), Name: "a.png", FileType: "image/png"}
	att.S3Key = storage.FileKeyForImage(user, att.ID, att.Name)

	keys := candidateKeys(user, att)

	assert.Equal(t, []string{att.S3Key}, keys, "the stored key and the derived gallery key are the same object")
}

// probeStore is what stops a wrong store (say, an empty local directory) from making every image
// look missing, so it must say no unless a known image can really be read back.
type probeFake struct {
	objects map[string][]byte
	err     error
}

func (p probeFake) UploadFile(context.Context, string, []byte, string) error { return nil }
func (p probeFake) DeleteFile(context.Context, string) error                 { return nil }
func (p probeFake) DownloadFile(_ context.Context, key string) ([]byte, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.objects[key], nil
}

func TestProbeStore(t *testing.T) {
	tests := []struct {
		name  string
		store probeFake
		known []string
		want  bool
	}{
		{"a known image reads back", probeFake{objects: map[string][]byte{"k1": []byte("x")}}, []string{"missing", "k1"}, true},
		{"none of the known images are there (wrong store)", probeFake{}, []string{"k1", "k2"}, false},
		{"the store errors", probeFake{err: errors.New("denied")}, []string{"k1"}, false},
		{"there is nothing to test against", probeFake{}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, probeStore(t.Context(), tt.store, tt.known))
		})
	}
}

func TestSplitGroupsRowsByVerdictInOrder(t *testing.T) {
	a, b, c, d := row{verdict: verdictMissing}, row{verdict: verdictPresent}, row{verdict: verdictUnverified}, row{verdict: verdictMissing}
	a.foundKey, d.foundKey = "a", "d"

	present, missing, unverified := split([]row{a, b, c, d})

	assert.Equal(t, []row{b}, present)
	assert.Equal(t, []row{a, d}, missing)
	assert.Equal(t, []row{c}, unverified)
}
