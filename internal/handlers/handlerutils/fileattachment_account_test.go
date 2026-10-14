package handlerutils

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/theimaginaryfoundation/what-iff/internal/agent/filechunker"
	"github.com/theimaginaryfoundation/what-iff/internal/apicontext"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The upload's embeddings run in the background, after the request has ended.
// They must still run as the uploader, so they are made with that account's
// provider key rather than with none.
func TestTriggerAsyncFileChunkingEmbedsAsTheUploader(t *testing.T) {
	owner := uuid.New()
	var seen atomic.Value
	client := openai.NewClient(
		option.WithAPIKey("unused"),
		option.WithMaxRetries(0),
		option.WithHTTPClient(&http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if id, ok := apicontext.UserIDFrom(r.Context()); ok {
				seen.Store(id)
			}
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody, Request: r}, nil
		})}),
	)
	pipeline := filechunker.NewFileChunkPipeline(&client, nil, zap.NewNop())
	path := tempUpload(t, 4)

	TriggerAsyncFileChunking(zap.NewNop(), owner, pipeline, uuid.New(), path, "notes.txt")

	requireRemoved(t, path)
	require.Equal(t, owner, seen.Load(), "the embedding request carries the uploader")
}
