package health

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const (
	// Database ping timeout - must be under ALB's 5s health check timeout
	dbPingTimeout = 3 * time.Second
)

// Handler handles health check requests
type Handler struct {
	db     *sql.DB
	logger *zap.Logger
}

// NewHandler creates a new health check handler
func NewHandler(db *sql.DB, logger *zap.Logger) *Handler {
	return &Handler{
		db:     db,
		logger: logger,
	}
}

// Response represents the health check response
type Response struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Live handles GET /api/health/live: process liveness only. It never touches the
// database or other dependencies, so a database blip cannot fail it. Point the ALB
// target-group health check here; failing it gets the task replaced by ECS.
func (h *Handler) Live(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := Response{Status: "healthy", Checks: map[string]string{}}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode health response", zap.Error(err))
	}
}

// Check handles GET /api/health. Kept for existing callers; it behaves exactly like
// Ready. The ALB should use /api/health/live: a database-backed check fails on every
// task at once during a DB blip, and ECS would then replace them all together.
func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	h.Ready(w, r)
}

// Ready handles GET /api/health/ready: verifies database connectivity and returns
// 503 if the ping fails. For dashboards and humans, not for the ALB.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	response := Response{
		Status: "healthy",
		Checks: make(map[string]string),
	}

	// Check database connectivity
	if err := h.checkDatabase(r.Context()); err != nil {
		h.logger.Warn("Health check: database unhealthy", zap.Error(err))
		response.Status = "unhealthy"
		response.Checks["database"] = "error"
		w.WriteHeader(http.StatusServiceUnavailable)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			h.logger.Error("Failed to encode health response", zap.Error(err))
		}
		return
	}

	response.Checks["database"] = "ok"
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("Failed to encode health response", zap.Error(err))
	}
}

// checkDatabase pings the database with timeout
func (h *Handler) checkDatabase(ctx context.Context) error {
	if h.db == nil {
		return sql.ErrConnDone
	}

	ctx, cancel := context.WithTimeout(ctx, dbPingTimeout)
	defer cancel()

	return h.db.PingContext(ctx)
}
