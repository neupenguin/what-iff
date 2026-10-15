package user

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/theimaginaryfoundation/what-iff/internal/datastore"
	"github.com/theimaginaryfoundation/what-iff/internal/handlers/handlerutils"
	"github.com/theimaginaryfoundation/what-iff/internal/middleware"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

func (h *Handler) GetUserPreferences(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context (set by auth middleware)
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		handlerutils.RespondWithError(w, h.logger, http.StatusUnauthorized, handlerutils.CodeNotSet, "Unauthorized", nil)
		return
	}

	prefs, err := h.store.GetUserPreferences(r.Context(), userID)
	if err != nil {
		handlerutils.RespondWithError(w, h.logger, http.StatusInternalServerError, handlerutils.CodeNotSet, "Error fetching user preferences", err)
		return
	}

	handlerutils.RespondWithJSON(w, h.logger, http.StatusOK, h.withImageGenerationProviders(prefs))
}

func (h *Handler) UpdateUserPreferences(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context (set by auth middleware)
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		handlerutils.RespondWithError(w, h.logger, http.StatusUnauthorized, handlerutils.CodeNotSet, "Unauthorized", nil)
		return
	}

	// Parse request body
	var req models.UserPreferences
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handlerutils.RespondWithError(w, h.logger, http.StatusBadRequest, handlerutils.CodeNotSet, "Invalid request payload", err)
		return
	}

	// Update user preferences using datastore
	prefs, err := h.store.UpdateUserPreferences(r.Context(), userID, req)
	if err == datastore.ErrExperimentalModelNotAllowed {
		handlerutils.RespondWithError(w, h.logger, http.StatusForbidden, handlerutils.CodeNotSet, "Experimental models are not enabled for this account", nil)
		return
	}
	if errors.Is(err, datastore.ErrInvalidRequestBody) {
		handlerutils.RespondWithError(w, h.logger, http.StatusBadRequest, handlerutils.CodeNotSet, err.Error(), err)
		return
	}
	if err != nil {
		handlerutils.RespondWithError(w, h.logger, http.StatusInternalServerError, handlerutils.CodeNotSet, "Error updating user preferences", err)
		return
	}

	handlerutils.RespondWithJSON(w, h.logger, http.StatusOK, h.withImageGenerationProviders(prefs))
}

// withImageGenerationProviders adds the deployment's image vendors, which the
// settings UI offers as the choices for image_generation_provider.
func (h *Handler) withImageGenerationProviders(prefs *models.UserPreferences) *models.UserPreferences {
	if prefs != nil && len(h.imageGenerationProviders) > 0 {
		prefs.ImageGenerationProviders = append([]string(nil), h.imageGenerationProviders...)
	}
	return prefs
}
