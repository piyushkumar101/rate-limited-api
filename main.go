// Rate-Limited API Service
//
// A production-grade HTTP API service with per-user sliding window
// rate limiting, designed to handle concurrent requests correctly.
//
// Architecture:
//   - net/http standard library for the HTTP server
//   - Pluggable store backend (in-memory or Redis)
//   - Sliding window algorithm for accurate rate limiting
//   - Optional request queue with exponential backoff retry
//   - Graceful shutdown with configurable timeout
//
// Environment Variables:
//   - PORT:          Server port (default: 3000)
//   - HOST:          Bind address (default: 0.0.0.0)
//   - STORE_BACKEND: "memory" or "redis" (default: memory)
//   - REDIS_ADDR:    Redis address (default: localhost:6379)
//   - REDIS_PASSWORD: Redis password (default: "")
//   - REDIS_DB:      Redis database number (default: 0)
//   - ENABLE_QUEUE:  "true" to enable retry queue (default: false)
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/rate-limited-api/internal/handler"
	"github.com/rate-limited-api/internal/middleware"
	"github.com/rate-limited-api/internal/queue"
	"github.com/rate-limited-api/internal/store"
)

func main() {
	// ── Configuration ────────────────────────────────────────────
	port := getEnv("PORT", "3000")
	host := getEnv("HOST", "0.0.0.0")
	backend := getEnv("STORE_BACKEND", "memory")
	enableQueue := getEnv("ENABLE_QUEUE", "false") == "true"
	startTime := time.Now()

	storeConfig := store.DefaultConfig()

	// ── Initialize Store ─────────────────────────────────────────
	var appStore store.Store

	switch backend {
	case "redis":
		redisAddr := getEnv("REDIS_ADDR", "localhost:6379")
		redisPassword := getEnv("REDIS_PASSWORD", "")
		redisDB, _ := strconv.Atoi(getEnv("REDIS_DB", "0"))

		redisStore, err := store.NewRedisStore(redisAddr, redisPassword, redisDB, storeConfig)
		if err != nil {
			log.Fatalf("Failed to initialize Redis store: %v", err)
		}
		appStore = redisStore
		defer redisStore.Close()
		log.Println("[STORE] Using Redis backend")

	default:
		appStore = store.New(storeConfig)
		log.Println("[STORE] Using in-memory backend")
	}

	// ── Initialize Queue (Optional) ──────────────────────────────
	var requestQueue *queue.RequestQueue
	if enableQueue {
		requestQueue = queue.New(appStore, queue.DefaultConfig())
		defer requestQueue.Stop()
		log.Println("[QUEUE] Request queue enabled")
	}

	// ── Initialize Handlers ──────────────────────────────────────
	requestHandler := &handler.RequestHandler{Store: appStore, Queue: requestQueue}
	statsHandler := &handler.StatsHandler{Store: appStore, Queue: requestQueue}
	healthHandler := &handler.HealthHandler{StartTime: startTime}

	// ── Setup Routes ─────────────────────────────────────────────
	mux := http.NewServeMux()
	mux.Handle("/request", requestHandler)
	mux.Handle("/stats", statsHandler)
	mux.Handle("/health", healthHandler)

	if requestQueue != nil {
		mux.Handle("/queue", &handler.QueueStatusHandler{Queue: requestQueue})
	}

	// 404 catch-all
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"success":false,"error":{"code":"NOT_FOUND","message":"The requested endpoint does not exist."},"timestamp":"%s"}`,
				time.Now().UTC().Format(time.RFC3339Nano))
			return
		}
		// Root path — show service info
		w.Header().Set("Content-Type", "application/json")
		endpoints := `["/request","/stats","/health"]`
		if requestQueue != nil {
			endpoints = `["/request","/stats","/health","/queue"]`
		}
		fmt.Fprintf(w, `{"service":"Rate-Limited API","version":"2.0.0","backend":"%s","queue_enabled":%v,"endpoints":%s}`,
			backend, enableQueue, endpoints)
	})

	// ── Apply Middleware ─────────────────────────────────────────
	var httpHandler http.Handler = mux
	httpHandler = middleware.Logger(httpHandler)

	// ── Create Server ────────────────────────────────────────────
	addr := fmt.Sprintf("%s:%s", host, port)
	server := &http.Server{
		Addr:         addr,
		Handler:      httpHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// ── Start Server ─────────────────────────────────────────────
	queueStatus := "disabled"
	if enableQueue {
		queueStatus = "enabled"
	}

	go func() {
		log.Println("╔══════════════════════════════════════════════════════╗")
		log.Println("║  Rate-Limited API Service v2.0                      ║")
		log.Printf( "║  Running on http://%s                         ║\n", addr)
		log.Println("║                                                      ║")
		log.Println("║  Endpoints:                                          ║")
		log.Println("║    POST /request  → Submit a request                 ║")
		log.Println("║    GET  /stats    → View per-user statistics         ║")
		log.Println("║    GET  /health   → Health check                     ║")
		if requestQueue != nil {
			log.Println("║    GET  /queue    → Check queued request status      ║")
		}
		log.Println("║                                                      ║")
		log.Printf( "║  Backend: %-8s | Queue: %-8s              ║\n", backend, queueStatus)
		log.Println("║  Rate Limit: 5 requests per user per minute          ║")
		log.Println("╚══════════════════════════════════════════════════════╝")

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// ── Graceful Shutdown ────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	log.Printf("\n[%s] Shutting down gracefully...", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server closed. Goodbye!")
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
