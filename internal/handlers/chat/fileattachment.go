package chat

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/theimaginaryfoundation/what-iff/internal/handlers/handlerutils"
	"github.com/theimaginaryfoundation/what-iff/internal/middleware"
)

func (h *Handler) CreateFileAttachment(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		handlerutils.RespondWithError(w, h.logger, http.StatusUnauthorized, handlerutils.CodeNotSet, "Unauthorized", nil)
		return
	}

	vars := mux.Vars(r)
	chatIDStr := vars["chatId"]

	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		handlerutils.RespondWithError(w, h.logger, http.StatusBadRequest, handlerutils.CodeNotSet, "Invalid chat ID", err)
		return
	}

	fileAttachment, tempFilePath, err := handlerutils.UploadFileAttachment(w, r, h.logger, userID)
	if err != nil {
		return
	}

	createdAttachment, err := handlerutils.StoreChatAttachment(r.Context(), h.logger, handlerutils.AttachmentStorage{
		Records:  h.ds,
		Files:    h.agent.FileStore(),
		Pipeline: h.agent.ChunkPipeline(),
	}, userID, chatID, fileAttachment, tempFilePath)
	if err != nil {
		handlerutils.RespondWithUploadError(w, h.logger, err)
		return
	}

	handlerutils.RespondWithJSON(w, h.logger, http.StatusCreated, createdAttachment)
}
