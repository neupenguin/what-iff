package accountexport

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/theimaginaryfoundation/what-iff/internal/exporter"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"github.com/theimaginaryfoundation/what-iff/internal/storage"
)

// fakeExportSource serves fixed account data. Its hooks let a test observe the upload while the
// build is still running, or fail the build part-way.
type fakeExportSource struct {
	convs    []exporter.ConversationInput
	personas []exporter.PersonalityInput
	memories []models.MemoryRecord
	// afterConversations runs once every conversation has been handed to the archive.
	afterConversations func()
	// convErrAt fails the walk at this conversation index (when >= 0).
	convErrAt int
	panicAt   int
}

func (s *fakeExportSource) ForEachExportConversation(_ context.Context, _ uuid.UUID, fn func(exporter.ConversationInput) error) error {
	for i, c := range s.convs {
		if i == s.convErrAt {
			return errFakeSource
		}
		if i == s.panicAt {
			panic("source exploded")
		}
		if err := fn(c); err != nil {
			return err
		}
	}
	if s.afterConversations != nil {
		s.afterConversations()
	}
	return nil
}

func (s *fakeExportSource) ExportPersonalityInputs(context.Context, uuid.UUID) ([]exporter.PersonalityInput, error) {
	return s.personas, nil
}

// ExportMemories writes the memory-export ZIP shape (JSONL per section) the datastore streams.
func (s *fakeExportSource) ExportMemories(_ context.Context, _ uuid.UUID, w io.Writer) error {
	zw := zip.NewWriter(w)
	f, err := zw.Create("user.json")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, m := range s.memories {
		if err := enc.Encode(m); err != nil {
			return err
		}
	}
	return zw.Close()
}

var errFakeSource = errors.New("source failed")

// streamingStore is a FileStore that only accepts streamed uploads. It reads in small chunks and
// records how much it has received, so a test can see the upload progress while the build runs.
type streamingStore struct {
	t         *testing.T
	chunk     int
	failAfter int64 // fail the upload once this many bytes have arrived (when > 0)

	mu       sync.Mutex
	objects  map[string][]byte
	reads    int
	received atomic.Int64
	aborted  atomic.Bool
	files    []storage.ObjectInfo
	readErr  error // the non-EOF error the stream ended with, if any
}

func newStreamingStore(t *testing.T) *streamingStore {
	return &streamingStore{t: t, chunk: 4 << 10, objects: map[string][]byte{}}
}

func (s *streamingStore) UploadStream(_ context.Context, key string, r io.Reader, _ string) error {
	var got []byte
	buf := make([]byte, s.chunk)
	for {
		n, err := r.Read(buf)
		got = append(got, buf[:n]...)
		s.mu.Lock()
		s.reads++
		s.mu.Unlock()
		s.received.Add(int64(n))
		if s.failAfter > 0 && s.received.Load() >= s.failAfter {
			s.aborted.Store(true)
			return errors.New("store rejected part")
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			s.aborted.Store(true)
			s.mu.Lock()
			s.readErr = err
			s.mu.Unlock()
			return fmt.Errorf("read stream: %w", err)
		}
	}
	s.mu.Lock()
	s.objects[key] = got
	s.mu.Unlock()
	return nil
}

func (s *streamingStore) UploadFile(context.Context, string, []byte, string) error {
	s.t.Error("account export must stream, not upload a whole []byte")
	return errors.New("unexpected UploadFile")
}

func (s *streamingStore) DownloadFile(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.objects[key], nil
}

func (s *streamingStore) DeleteFile(context.Context, string) error { return nil }

func (s *streamingStore) ListObjects(context.Context, string) ([]storage.ObjectInfo, error) {
	return s.files, nil
}

// randomText is incompressible, so the archive is large enough to need many upload reads.
func randomText(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(b)
}

func exportFixture(t *testing.T, conversations int) (*fakeExportSource, uuid.UUID) {
	t.Helper()
	userID := uuid.New()
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	src := &fakeExportSource{convErrAt: -1, panicAt: -1}
	for i := range conversations {
		at := base.Add(time.Duration(i) * time.Minute)
		src.convs = append(src.convs, exporter.ConversationInput{
			ID: uuid.New(), Title: fmt.Sprintf("chat %d", i), CreatedAt: at,
			Messages: []exporter.MessageInput{
				{Origin: models.MessageOriginUser, Text: randomText(t, 6<<10), SentAt: at},
				{Origin: models.MessageOriginAssistant, Text: "reply <&>", SentAt: at.Add(time.Second)},
			},
		})
	}
	// One conversation with no exportable message: omitted from conversations.json and its count.
	src.convs = append(src.convs, exporter.ConversationInput{
		ID: uuid.New(), Title: "empty", CreatedAt: base.Add(time.Duration(conversations) * time.Minute),
		Messages: []exporter.MessageInput{{Origin: models.MessageOriginUser, Text: "", SentAt: base}},
	})
	src.personas = []exporter.PersonalityInput{{
		ID: uuid.New(), Name: "Iff", SystemPrompt: "be kind", Scratchpad: "notes",
		CreatedAt: base, UpdatedAt: base,
	}}
	for i := range 3 {
		src.memories = append(src.memories, models.MemoryRecord{ID: uuid.New(), Content: fmt.Sprintf("memory %d", i), CreatedAt: base})
	}
	return src, userID
}

// bufferedAccountZip is the pre-streaming build: everything assembled in memory, then zipped. The
// streamed archive must match it entry for entry.
func bufferedAccountZip(t *testing.T, src *fakeExportSource, userID uuid.UUID, files []exporter.FileObject, manifest exporter.Manifest) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	tree := exporter.NewZipTree(zw)
	require.NoError(t, exporter.WriteConversations(tree, src.convs))
	require.NoError(t, exporter.WritePersonalities(tree, src.personas))
	require.NoError(t, exporter.WriteSillyTavernCards(tree, src.personas, "iffy"))
	var memBuf bytes.Buffer
	require.NoError(t, src.ExportMemories(context.Background(), userID, &memBuf))
	require.NoError(t, tree.Write("memories.zip", memBuf.Bytes()))
	require.NoError(t, exporter.WriteFilesManifest(tree, "users/"+userID.String()+"/", files))
	require.NoError(t, exporter.WriteManifest(tree, manifest))
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func readArchive(t *testing.T, data []byte) (*zip.Reader, []string, map[string][]byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	var names []string
	contents := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		require.NoError(t, err)
		b, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		names = append(names, f.Name)
		contents[f.Name] = b
	}
	return zr, names, contents
}

// The archive reaches the store through a reader while it is still being built (bytes arrive
// before the last conversation is written), and comes out entry-for-entry identical to the
// buffered build, with a nested memories.zip that opens as the memory-export format.
func TestStreamAccountZipStreamsArchiveIntoUpload(t *testing.T) {
	src, userID := exportFixture(t, 300) // ~2.4 MB of incompressible text
	store := newStreamingStore(t)
	key := "users/" + userID.String() + "/a/photo.png"
	store.files = []storage.ObjectInfo{{Key: key, Size: 42, ETag: "abc"}}
	h := &Handler{logger: zap.NewNop(), fileStore: store}

	var receivedBeforeBuildDone int64
	src.afterConversations = func() { receivedBeforeBuildDone = store.received.Load() }
	var builtSize int64
	var builtCounts map[string]int
	counts, buildErr, uploadErr := h.streamAccountZip(context.Background(), src, "exports/a.zip", userID, "me@example.com", "iffy",
		func(size int64, counts map[string]int) { builtSize, builtCounts = size, counts })
	require.NoError(t, buildErr)
	require.NoError(t, uploadErr)

	require.Positive(t, receivedBeforeBuildDone, "the upload was reading before the build finished")
	require.Greater(t, store.reads, 100, "the archive arrived in many small reads")
	stored := store.objects["exports/a.zip"]
	require.Equal(t, int64(len(stored)), builtSize, "the counted size is the uploaded size")
	// 300, not 301: the fixture's empty conversation is omitted from conversations.json, so it
	// isn't counted either.
	wantCounts := map[string]int{"conversations": 300, "personalities": 1, "files": 1}
	require.Equal(t, wantCounts, counts)
	require.Equal(t, wantCounts, builtCounts)

	zr, names, got := readArchive(t, stored)
	require.NoError(t, validateImportArchive(zr))
	var manifest exporter.Manifest
	require.NoError(t, json.Unmarshal(got["manifest.json"], &manifest))
	require.Equal(t, wantCounts, manifest.Counts)
	require.Equal(t, exporter.ManifestAccount{UserID: userID.String(), Email: "me@example.com", Username: "iffy"}, manifest.Account)

	_, wantNames, want := readArchive(t, bufferedAccountZip(t, src, userID,
		[]exporter.FileObject{{Key: key, Size: 42, ETag: "abc"}}, manifest))
	require.Equal(t, wantNames, names, "same entries in the same order")
	for _, name := range wantNames {
		require.Equalf(t, want[name], got[name], "entry %s", name)
	}

	parsed, err := exporter.ParseConversations(got[exporter.ConversationsFile])
	require.NoError(t, err)
	require.Len(t, parsed, 300, "the empty conversation is omitted from the file")

	mem := got["memories.zip"]
	memZip, err := zip.NewReader(bytes.NewReader(mem), int64(len(mem)))
	require.NoError(t, err)
	require.Len(t, memZip.File, 1)
	require.Equal(t, "user.json", memZip.File[0].Name)
}

// A build that fails part-way ends the stream with its error, so the store aborts and keeps nothing,
// and the failure is reported as a build failure.
func TestStreamAccountZipBuildFailureAbortsUpload(t *testing.T) {
	src, userID := exportFixture(t, 50)
	src.convErrAt = 40
	store := newStreamingStore(t)
	h := &Handler{logger: zap.NewNop(), fileStore: store}

	built := false
	_, buildErr, uploadErr := h.streamAccountZip(context.Background(), src, "exports/a.zip", userID, "", "", func(int64, map[string]int) { built = true })
	require.ErrorIs(t, buildErr, errFakeSource)
	require.NoError(t, uploadErr)
	require.False(t, built)
	require.True(t, store.aborted.Load())
	require.Empty(t, store.objects)
	// The upload side saw the build's own error, not the deferred "build aborted" placeholder.
	require.ErrorIs(t, store.readErr, errFakeSource)
}

// An upload that fails midway stops the build (no writer left blocked on the pipe) and is reported
// as an upload failure.
func TestStreamAccountZipUploadFailureStopsBuild(t *testing.T) {
	src, userID := exportFixture(t, 300)
	store := newStreamingStore(t)
	store.failAfter = 64 << 10
	h := &Handler{logger: zap.NewNop(), fileStore: store}

	done := make(chan struct{})
	var buildErr, uploadErr error
	go func() {
		defer close(done)
		_, buildErr, uploadErr = h.streamAccountZip(context.Background(), src, "exports/a.zip", userID, "", "", func(int64, map[string]int) {})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the build stayed blocked after the upload failed")
	}
	require.NoError(t, buildErr)
	require.ErrorContains(t, uploadErr, "store rejected part")
	require.Empty(t, store.objects)
}

// A panic in the build still ends the stream, so the upload aborts rather than waiting forever; the
// panic itself propagates to runAccountExport's recover.
func TestStreamAccountZipBuildPanicAbortsUpload(t *testing.T) {
	src, userID := exportFixture(t, 50)
	src.panicAt = 10
	store := newStreamingStore(t)
	h := &Handler{logger: zap.NewNop(), fileStore: store}

	require.PanicsWithValue(t, "source exploded", func() {
		_, _, _ = h.streamAccountZip(context.Background(), src, "exports/a.zip", userID, "", "", func(int64, map[string]int) {})
	})
	require.Eventually(t, store.aborted.Load, 5*time.Second, 10*time.Millisecond)
	require.Empty(t, store.objects)
}
