package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	entsql "entgo.io/ent/dialect/sql"

	"entgo.io/ent/dialect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/require"
	"github.com/theimaginaryfoundation/what-iff/ent"
	"github.com/theimaginaryfoundation/what-iff/internal/datastore"
	"github.com/theimaginaryfoundation/what-iff/internal/utils"
	"go.uber.org/zap"
)

// newFileAttachmentTestDatastore builds a Datastore backed by go-sqlmock, mirroring the
// agent package's own testsupport helper (internal/agent/testsupport_test.go), which this
// package does not otherwise have a copy of.
func newFileAttachmentTestDatastore(t *testing.T) (*datastore.Datastore, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	drv := entsql.OpenDB(dialect.SQLite, db)
	client := ent.NewClient(ent.Driver(drv))
	ds, err := datastore.NewDatastore(client, db, zap.NewNop(), "12345678901234567890123456789012", nil)
	require.NoError(t, err)
	return ds, mock, func() {
		_ = client.Close()
	}
}

// --- UploadFileAttachment ---

func TestUploadFileAttachment_Success(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"file-123","object":"file","bytes":10,"created_at":1,"filename":"a.txt","purpose":"user_data","status":"processed"}`))
	}))
	defer srv.Close()

	a := newTestOpenAIProvider(srv.URL)
	id, err := a.UploadFileAttachment(context.Background(), uuid.New(), nil, strings.NewReader("hello"), "a.txt", utils.FileTypeInfo{ContentType: "text/plain"})
	require.NoError(t, err)
	require.Equal(t, "file-123", id)
}

func TestUploadFileAttachment_ErrorIsReturned(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	a := newTestOpenAIProvider(srv.URL)
	_, err := a.UploadFileAttachment(context.Background(), uuid.New(), nil, strings.NewReader("hello"), "a.txt", utils.FileTypeInfo{ContentType: "text/plain"})
	require.Error(t, err)
}

// --- DeleteFileAttachment ---

func TestDeleteFileAttachment_Success(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"file-123","object":"file","deleted":true}`))
	}))
	defer srv.Close()

	a := newTestOpenAIProvider(srv.URL)
	err := a.DeleteFileAttachment(context.Background(), "file-123")
	require.NoError(t, err)
}

func TestDeleteFileAttachment_ErrorIsReturned(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	a := newTestOpenAIProvider(srv.URL)
	err := a.DeleteFileAttachment(context.Background(), "file-123")
	require.Error(t, err)
}

// --- SaveMessageAttachments ---

func TestSaveMessageAttachments_NoMatchingOutputTypeIsNoOp(t *testing.T) {
	t.Parallel()
	a := newTestOpenAIProvider("http://unused.invalid")
	resp := &responses.Response{Output: []responses.ResponseOutputItemUnion{{Type: "reasoning"}}}
	err := a.SaveMessageAttachments(context.Background(), uuid.New(), uuid.New(), resp)
	require.NoError(t, err)
}

func TestSaveMessageAttachments_ImageGenerationCallDispatchesToSaveImageAttachment(t *testing.T) {
	t.Parallel()
	ds, mock, cleanup := newFileAttachmentTestDatastore(t)
	defer cleanup()

	// CreateFileAttachment's user-exists check fails fast (not found), so
	// saveImageAttachment logs and returns without needing a fileStore.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	a := NewOpenAIProvider(ds, nil, nil, nil)
	var respObj responses.Response
	require.NoError(t, json.Unmarshal([]byte(`{"output":[{"type":"image_generation_call","id":"img_1","result":"aGVsbG8="}]}`), &respObj))

	err := a.SaveMessageAttachments(context.Background(), uuid.New(), uuid.New(), &respObj)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- processAnnotations ---

// --- saveInterpreterAttachment ---

// --- saveImageAttachment ---

func TestSaveImageAttachment_CreateFileAttachmentErrorLogsAndReturns(t *testing.T) {
	t.Parallel()
	ds, mock, cleanup := newFileAttachmentTestDatastore(t)
	defer cleanup()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	a := NewOpenAIProvider(ds, nil, nil, nil)
	var image responses.ResponseOutputItemImageGenerationCall
	require.NoError(t, json.Unmarshal([]byte(`{"id":"img_1","type":"image_generation_call","result":"aGVsbG8=","status":"completed"}`), &image))

	require.NotPanics(t, func() {
		a.saveImageAttachment(context.Background(), uuid.New(), uuid.New(), image)
	})
	require.NoError(t, mock.ExpectationsWereMet())
}

// --- saveInterpreterAttachments ---