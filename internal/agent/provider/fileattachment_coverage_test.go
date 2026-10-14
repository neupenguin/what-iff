package provider

import (
	"context"
	"encoding/json"
	"testing"

	entsql "entgo.io/ent/dialect/sql"

	"entgo.io/ent/dialect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/require"
	"github.com/theimaginaryfoundation/what-iff/ent"
	"github.com/theimaginaryfoundation/what-iff/internal/datastore"
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
