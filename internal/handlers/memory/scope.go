package memory

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/theimaginaryfoundation/what-iff/internal/datastore"
	"github.com/theimaginaryfoundation/what-iff/internal/handlers/handlerutils"
	"github.com/theimaginaryfoundation/what-iff/internal/middleware"
	"github.com/theimaginaryfoundation/what-iff/internal/models"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

// MoveMemoryScope moves one memory across the thread boundary: a thread memory to global or
// personality, or a global/personality memory back to a thread (datastore.MoveMemoryScope).
// Content is unchanged, so nothing is re-embedded.
func (h *Handler) MoveMemoryScope(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		handlerutils.RespondWithError(w, h.logger, http.StatusUnauthorized, handlerutils.CodeNotSet, "Unauthorized", nil)
		return
	}

	memoryID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		handlerutils.RespondWithError(w, h.logger, http.StatusBadRequest, handlerutils.CodeNotSet, "Invalid memory ID", err)
		return
	}

	var move models.MemoryScopeMove
	if err := json.NewDecoder(r.Body).Decode(&move); err != nil {
		handlerutils.RespondWithError(w, h.logger, http.StatusBadRequest, handlerutils.CodeNotSet, "Invalid request body", err)
		return
	}

	moved, err := h.ds.MoveMemoryScope(r.Context(), userID, memoryID, move)
	switch {
	case err == nil:
		handlerutils.RespondWithJSON(w, h.logger, http.StatusOK, moved)
	case errors.Is(err, datastore.ErrMemoryNotFound):
		handlerutils.RespondWithError(w, h.logger, http.StatusNotFound, handlerutils.CodeNotSet, "Memory not found", err)
	case errors.Is(err, datastore.ErrChatNotFound), errors.Is(err, datastore.ErrPersonalityNotFound):
		handlerutils.RespondWithError(w, h.logger, http.StatusNotFound, handlerutils.CodeNotSet, err.Error(), err)
	case errors.Is(err, datastore.ErrMemoryScopeMove):
		handlerutils.RespondWithError(w, h.logger, http.StatusConflict, handlerutils.CodeNotSet, err.Error(), err)
	case errors.Is(err, datastore.ErrInvalidRequestBody):
		handlerutils.RespondWithError(w, h.logger, http.StatusBadRequest, handlerutils.CodeNotSet, err.Error(), err)
	default:
		h.logger.Error("failed to move memory scope", zap.String("user_id", userID.String()), zap.String("memory_id", memoryID.String()), zap.Error(err))
		handlerutils.RespondWithError(w, h.logger, http.StatusInternalServerError, handlerutils.CodeNotSet, "Failed to move memory", err)
	}
}
