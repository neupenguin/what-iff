package schedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/openai/openai-go/v3/responses"
	"github.com/stretchr/testify/require"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// paramsRecorder captures the params Parse sends and fails the call, so the test stops before
// any response parsing.
type paramsRecorder struct {
	params responses.ResponseNewParams
}

func (r *paramsRecorder) CallWithRetry(_ context.Context, params responses.ResponseNewParams) (*responses.Response, error) {
	r.params = params
	return nil, errors.New("recorded")
}

func TestParse_OmitsTemperature(t *testing.T) {
	t.Parallel()
	rec := &paramsRecorder{}
	_, err := Parse(context.Background(), rec, uuid.New(), "every day at 9am", "UTC", time.Now())
	require.Error(t, err)
	require.Equal(t, models.UtilityModelName, rec.params.Model)
	require.False(t, rec.params.Temperature.Valid(), "requests never set temperature")
}
