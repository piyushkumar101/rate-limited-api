package handler

import (
	"net/http"
	"time"

	"github.com/rate-limited-api/internal/model"
	"github.com/rate-limited-api/internal/store"
)

// StatsHandler handles GET /stats.
type StatsHandler struct {
	Store *store.MemoryStore
}

// ServeHTTP returns per-user request statistics.
func (h *StatsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET method is allowed on this endpoint.")
		return
	}

	stats := h.Store.GetStats()

	writeJSON(w, http.StatusOK, model.APIResponse{
		Success:   true,
		Data:      stats,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}
