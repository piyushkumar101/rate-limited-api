package handler

import (
	"net/http"
	"time"

	"github.com/rate-limited-api/internal/model"
	"github.com/rate-limited-api/internal/queue"
	"github.com/rate-limited-api/internal/store"
)

// StatsHandler handles GET /stats.
type StatsHandler struct {
	Store store.Store
	Queue *queue.RequestQueue // nil = no queue stats
}

// ServeHTTP returns per-user request statistics.
func (h *StatsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET method is allowed on this endpoint.")
		return
	}

	stats := h.Store.GetStats()

	// Add queue stats if available
	responseData := map[string]interface{}{
		"total_users":    stats.TotalUsers,
		"total_requests": stats.TotalRequests,
		"users":          stats.Users,
	}

	if h.Queue != nil {
		qStats := h.Queue.GetStats()
		responseData["queue"] = qStats
	}

	writeJSON(w, http.StatusOK, model.APIResponse{
		Success:   true,
		Data:      responseData,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}
