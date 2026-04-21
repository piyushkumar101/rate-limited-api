// Package model defines the data structures used across the service.
package model

import "time"

// RequestBody represents the incoming POST /request payload.
type RequestBody struct {
	UserID  string                 `json:"user_id"`
	Payload map[string]interface{} `json:"payload"`
}

// RequestRecord is a stored record of a processed request.
type RequestRecord struct {
	ID        string                 `json:"id"`
	UserID    string                 `json:"user_id"`
	Payload   map[string]interface{} `json:"payload"`
	Timestamp time.Time              `json:"timestamp"`
}

// UserStats holds per-user request statistics.
type UserStats struct {
	UserID                 string  `json:"user_id"`
	TotalRequests          int     `json:"total_requests"`
	RequestsInWindow       int     `json:"requests_in_current_window"`
	RemainingRequests      int     `json:"remaining_requests"`
	OldestRequest          *string `json:"oldest_request"`  // ISO-8601 or null
	NewestRequest          *string `json:"newest_request"`  // ISO-8601 or null
}

// StatsResponse is the aggregated response for GET /stats.
type StatsResponse struct {
	TotalUsers    int         `json:"total_users"`
	TotalRequests int         `json:"total_requests"`
	Users         []UserStats `json:"users"`
}

// RateLimitResult holds the outcome of a rate limit check.
type RateLimitResult struct {
	Allowed      bool
	Remaining    int
	ResetAt      time.Time
	RetryAfterMs int64
}

// APIResponse is the standard success response envelope.
type APIResponse struct {
	Success   bool        `json:"success"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp string      `json:"timestamp"`
}

// APIError is the standard error response envelope.
type APIError struct {
	Success   bool       `json:"success"`
	Error     ErrorDetail `json:"error"`
	Timestamp string     `json:"timestamp"`
}

// ErrorDetail describes the error.
type ErrorDetail struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	RetryAfterMs int64  `json:"retry_after_ms,omitempty"`
}

// RequestSuccessData is returned on successful POST /request.
type RequestSuccessData struct {
	RequestID         string `json:"request_id"`
	UserID            string `json:"user_id"`
	RemainingRequests int    `json:"remaining_requests"`
}

// HealthResponse is returned by GET /health.
type HealthResponse struct {
	Status    string  `json:"status"`
	Uptime    float64 `json:"uptime_seconds"`
	Timestamp string  `json:"timestamp"`
}

// QueuedResponseData is returned when a request is queued for retry.
type QueuedResponseData struct {
	QueueID       string `json:"queue_id"`
	UserID        string `json:"user_id"`
	Status        string `json:"status"`
	MaxRetries    int    `json:"max_retries"`
	Message       string `json:"message"`
}

