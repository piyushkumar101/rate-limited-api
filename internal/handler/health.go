package handler

import (
	"net/http"
	"time"

	"github.com/rate-limited-api/internal/model"
)

// HealthHandler handles GET /health.
type HealthHandler struct {
	StartTime time.Time
}

// ServeHTTP returns the service health status.
func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET method is allowed on this endpoint.")
		return
	}

	writeJSON(w, http.StatusOK, model.HealthResponse{
		Status:    "healthy",
		Uptime:    time.Since(h.StartTime).Seconds(),
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}
