// Package api implements the WindowProof HTTP JSON surface: strict request
// decoding, stable error responses, the full task-lifecycle route set and the
// embedded operations page.
package api

import (
	"net/http"

	"github.com/windowproof/fenestration/internal/service"
)

// Server hosts the WindowProof HTTP API. It depends only on the application
// service so every handler maps one-to-one onto a documented command.
type Server struct {
	svc *service.Service
}

// NewServer constructs a Server backed by the given application service.
func NewServer(svc *service.Service) *Server {
	return &Server{svc: svc}
}

// Handler returns the root HTTP handler with every documented route registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /", s.handleWeb)

	mux.HandleFunc("POST /api/v1/tasks", s.handleCreateTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/lock", s.handleLock)
	mux.HandleFunc("POST /api/v1/tasks/{id}/installation", s.handleInstallation)
	mux.HandleFunc("POST /api/v1/tasks/{id}/replace-chamber", s.handleReplaceChamber)
	mux.HandleFunc("POST /api/v1/tasks/{id}/pressure-steps", s.handlePressureStep)
	mux.HandleFunc("POST /api/v1/tasks/{id}/spray-checkpoints", s.handleSprayCheckpoint)
	mux.HandleFunc("POST /api/v1/tasks/{id}/instrument-attempts/{attemptID}/retry", s.handleRetryAttempt)
	mux.HandleFunc("POST /api/v1/tasks/{id}/defects", s.handleDefect)
	mux.HandleFunc("POST /api/v1/tasks/{id}/repairs", s.handleRepair)
	mux.HandleFunc("POST /api/v1/tasks/{id}/defects/{evidenceID}/close", s.handleCloseDefect)
	mux.HandleFunc("POST /api/v1/tasks/{id}/reviews", s.handleReview)
	mux.HandleFunc("POST /api/v1/tasks/{id}/release", s.handleRelease)
	mux.HandleFunc("POST /api/v1/tasks/{id}/quarantine", s.handleQuarantine)
	mux.HandleFunc("POST /api/v1/tasks/{id}/cancel", s.handleCancel)
	mux.HandleFunc("GET /api/v1/tasks/{id}", s.handleGetTask)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
}
