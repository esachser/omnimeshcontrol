// Package httpadapter implements the inbound HTTP adapter for observability
// endpoints: /metrics (Prometheus/OpenMetrics), /api/v1/targets (Prometheus HTTP SD),
// and /healthz (RFC-0005 §9).
package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
)

// ObservabilityServer serves Prometheus metrics, HTTP service discovery targets,
// and health checks over HTTP.
type ObservabilityServer struct {
	server         *http.Server
	addr           string
	metricsHandler http.Handler
	targetProvider inbound.MetricsTargetProvider
	logger         *slog.Logger
}

// NewObservabilityServer constructs an ObservabilityServer bound to addr.
func NewObservabilityServer(
	addr string,
	metricsHandler http.Handler,
	targetProvider inbound.MetricsTargetProvider,
	logger *slog.Logger,
) *ObservabilityServer {
	if logger == nil {
		logger = slog.Default()
	}

	s := &ObservabilityServer{
		addr:           addr,
		metricsHandler: metricsHandler,
		targetProvider: targetProvider,
		logger:         logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("GET /api/v1/targets", s.handleTargets)
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	s.server = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Handler returns the underlying HTTP handler (useful for httptest).
func (s *ObservabilityServer) Handler() http.Handler {
	return s.server.Handler
}

// Start begins serving HTTP requests until the server is shut down.
func (s *ObservabilityServer) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.logger.Info("observability HTTP server listening", "addr", ln.Addr().String())
	err = s.server.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully stops the HTTP server.
func (s *ObservabilityServer) Shutdown(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (s *ObservabilityServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.metricsHandler == nil {
		http.NotFound(w, r)
		return
	}
	s.metricsHandler.ServeHTTP(w, r)
}

func (s *ObservabilityServer) handleTargets(w http.ResponseWriter, r *http.Request) {
	targets := []model.MetricTargetGroup{}
	if s.targetProvider != nil {
		var err error
		targets, err = s.targetProvider.ListMetricsTargets(r.Context())
		if err != nil {
			s.logger.Warn("failed to list metrics targets", "error", err)
			http.Error(w, "failed to list metrics targets", http.StatusInternalServerError)
			return
		}
		if targets == nil {
			targets = []model.MetricTargetGroup{}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(targets); err != nil {
		s.logger.Warn("failed to encode metrics targets", "error", err)
	}
}

func (s *ObservabilityServer) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
