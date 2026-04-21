// Package handler implements the HTTP route handlers.
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rate-limited-api/internal/model"
	"github.com/rate-limited-api/internal/store"
)

// RequestHandler handles POST /request.
type RequestHandler struct {
	Store *store.MemoryStore
}

// ServeHTTP processes incoming requests with validation and rate limiting.
func (h *RequestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only POST method is allowed on this endpoint.")
		return
	}

	// Parse request body
	var body model.RequestBody
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", fmt.Sprintf("Invalid JSON body: %v", err))
		return
	}

	// Validate user_id
	body.UserID = strings.TrimSpace(body.UserID)
	if body.UserID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "user_id is required and must be a non-empty string.")
		return
	}

	// Validate payload
	if body.Payload == nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "payload is required and must be a JSON object.")
		return
	}

	// Generate unique request ID
	requestID := uuid.New().String()

	// Atomic rate limit check + record
	result, record := h.Store.CheckAndRecord(body.UserID, body.Payload, requestID)

	config := h.Store.GetConfig()

	if !result.Allowed {
		// Set rate limit headers
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(config.MaxRequests))
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetAt.Unix(), 10))
		w.Header().Set("Retry-After", strconv.FormatInt(int64(result.ResetAt.Sub(time.Now()).Seconds())+1, 10))

		writeJSON(w, http.StatusTooManyRequests, model.APIError{
			Success: false,
			Error: model.ErrorDetail{
				Code:         "RATE_LIMIT_EXCEEDED",
				Message:      fmt.Sprintf("Rate limit exceeded for user '%s'. Maximum %d requests per %v.", body.UserID, config.MaxRequests, config.WindowSize),
				RetryAfterMs: result.RetryAfterMs,
			},
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		})
		return
	}

	// Success — set rate limit headers
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(config.MaxRequests))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.ResetAt.Unix(), 10))

	writeJSON(w, http.StatusCreated, model.APIResponse{
		Success: true,
		Data: model.RequestSuccessData{
			RequestID:         record.ID,
			UserID:            record.UserID,
			RemainingRequests: result.Remaining,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, statusCode int, code, message string) {
	writeJSON(w, statusCode, model.APIError{
		Success: false,
		Error: model.ErrorDetail{
			Code:    code,
			Message: message,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
}
