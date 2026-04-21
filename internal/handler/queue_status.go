package handler

import (
	"net/http"
	"time"

	"github.com/rate-limited-api/internal/model"
	"github.com/rate-limited-api/internal/queue"
)

// QueueStatusHandler handles GET /queue/{id}.
type QueueStatusHandler struct {
	Queue *queue.RequestQueue
}

// ServeHTTP returns the status of a queued request.
func (h *QueueStatusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET method is allowed on this endpoint.")
		return
	}

	// Extract queue ID from query parameter
	queueID := r.URL.Query().Get("id")
	if queueID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Query parameter 'id' is required.")
		return
	}

	item := h.Queue.GetQueuedRequest(queueID)
	if item == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Queued request not found.")
		return
	}

	writeJSON(w, http.StatusOK, model.APIResponse{
		Success: true,
		Data: map[string]interface{}{
			"queue_id":     item.ID,
			"request_id":   item.RequestID,
			"user_id":      item.UserID,
			"status":       item.Status,
			"attempts":     item.Attempts,
			"max_retries":  item.MaxRetries,
			"queued_at":    item.QueuedAt.UTC().Format(time.RFC3339Nano),
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}
