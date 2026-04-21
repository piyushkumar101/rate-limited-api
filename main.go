// Rate-Limited API Service
//
// A production-grade HTTP API service with per-user sliding window
// rate limiting, designed to handle concurrent requests correctly.
//
// Architecture:
//   - net/http standard library for the HTTP server
//   - sync.Mutex-protected in-memory store for thread safety
//   - Sliding window algorithm for accurate rate limiting
//   - Graceful shutdown with configurable timeout
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rate-limited-api/internal/handler"
	"github.com/rate-limited-api/internal/middleware"
	"github.com/rate-limited-api/internal/store"
)

func main() {
	// ── Configuration ────────────────────────────────────────────
	port := getEnv("PORT", "3000")
	host := getEnv("HOST", "0.0.0.0")
	startTime := time.Now()

	// ── Initialize Store ─────────────────────────────────────────
	memStore := store.New(store.DefaultConfig())

	// ── Initialize Handlers ──────────────────────────────────────
	requestHandler := &handler.RequestHandler{Store: memStore}
	statsHandler := &handler.StatsHandler{Store: memStore}
	healthHandler := &handler.HealthHandler{StartTime: startTime}

	// ── Setup Routes ─────────────────────────────────────────────
	mux := http.NewServeMux()
	mux.Handle("/request", requestHandler)
	mux.Handle("/stats", statsHandler)
	mux.Handle("/health", healthHandler)

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
		fmt.Fprintf(w, `{"service":"Rate-Limited API","version":"1.0.0","endpoints":["/request","/stats","/health"]}`)
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
	go func() {
		log.Println("╔══════════════════════════════════════════════════════╗")
		log.Println("║  Rate-Limited API Service                            ║")
		log.Printf( "║  Running on http://%s                         ║\n", addr)
		log.Println("║                                                      ║")
		log.Println("║  Endpoints:                                          ║")
		log.Println("║    POST /request  → Submit a request                 ║")
		log.Println("║    GET  /stats    → View per-user statistics         ║")
		log.Println("║    GET  /health   → Health check                     ║")
		log.Println("║                                                      ║")
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
