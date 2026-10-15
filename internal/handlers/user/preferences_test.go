package user

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/theimaginaryfoundation/what-iff/internal/datastore"
	"github.com/theimaginaryfoundation/what-iff/internal/middleware"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
	"go.uber.org/zap"
)

func preferencesRequest(method, body string) *http.Request {
	req := httptest.NewRequest(method, "/user/preferences", strings.NewReader(body))
	return req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, uuid.New()))
}

func TestPreferences_ReportTheDeploymentsImageVendors(t *testing.T) {
	store := &mockUserStore{prefs: &models.UserPreferences{Theme: "dark", ImageGenerationProvider: "google"}}
	h := NewHandler(store, zap.NewNop(), nil, "test").WithImageGenerationProviders([]string{"openai", "google"})

	for _, tc := range []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
		req  *http.Request
	}{
		{"GET", h.GetUserPreferences, preferencesRequest(http.MethodGet, "")},
		{"PUT", h.UpdateUserPreferences, preferencesRequest(http.MethodPut, `{"image_generation_provider":"google"}`)},
	} {
		rr := httptest.NewRecorder()
		tc.call(rr, tc.req)
		require.Equal(t, http.StatusOK, rr.Code, tc.name)
		var got models.UserPreferences
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got), tc.name)
		require.Equal(t, "google", got.ImageGenerationProvider, tc.name)
		require.Equal(t, []string{"openai", "google"}, got.ImageGenerationProviders, tc.name)
	}
}

func TestUpdatePreferences_UnknownImageVendorIsBadRequest(t *testing.T) {
	store := &mockUserStore{updatePrefsErr: fmt.Errorf("%w: unknown image_generation_provider \"deepseek\"", datastore.ErrInvalidRequestBody)}
	h := NewHandler(store, zap.NewNop(), nil, "test")

	rr := httptest.NewRecorder()
	h.UpdateUserPreferences(rr, preferencesRequest(http.MethodPut, `{"image_generation_provider":"deepseek"}`))

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "deepseek")
}
