package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/adapter/outbound/otelmetrics"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

type stubTargetProvider struct {
	targets []model.MetricTargetGroup
	err     error
}

func (s *stubTargetProvider) ListMetricsTargets(_ context.Context) ([]model.MetricTargetGroup, error) {
	return s.targets, s.err
}

func TestObservabilityServer_MetricsTargetsAndHealthz(t *testing.T) {
	cfg := model.DefaultTelemetryConfig()
	rec, err := otelmetrics.NewRecorder(context.Background(), cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	defer func() { _ = rec.Shutdown(context.Background()) }()

	rec.IncrCounter(context.Background(), model.MetricCPGRPCRequestsTotal, map[string]string{
		"method":      "/omnimesh.v1.ControlPlaneService/ApplyResource",
		"status_code": "OK",
	})

	provider := &stubTargetProvider{
		targets: []model.MetricTargetGroup{
			{
				Targets: []string{"10.0.0.5:9091"},
				Labels: map[string]string{
					"job":      "omnimesh-agent",
					"node_id":  "node-1",
					"platform": "linux",
				},
			},
		},
	}

	srv := NewObservabilityServer(":0", rec.Handler(), provider, slog.Default())

	// 1. /metrics (Prometheus text exposition)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	resp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d, want 200", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "omnimesh_cp_grpc_requests_total") {
		t.Errorf("expected metric in /metrics response, got:\n%s", resp.Body.String())
	}

	// 2. /metrics with OpenMetrics Accept header
	omReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	omReq.Header.Set("Accept", "application/openmetrics-text; version=1.0.0")
	omResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(omResp, omReq)
	if omResp.Code != http.StatusOK {
		t.Fatalf("GET /metrics OpenMetrics status = %d, want 200", omResp.Code)
	}
	if !strings.Contains(omResp.Header().Get("Content-Type"), "application/openmetrics-text") {
		t.Errorf("expected OpenMetrics Content-Type, got %q", omResp.Header().Get("Content-Type"))
	}

	// 3. /api/v1/targets
	tgtReq := httptest.NewRequest(http.MethodGet, "/api/v1/targets", nil)
	tgtResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(tgtResp, tgtReq)
	if tgtResp.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/targets status = %d, want 200", tgtResp.Code)
	}
	var gotTargets []model.MetricTargetGroup
	if err := json.Unmarshal(tgtResp.Body.Bytes(), &gotTargets); err != nil {
		t.Fatalf("decode targets JSON: %v", err)
	}
	if len(gotTargets) != 1 || gotTargets[0].Targets[0] != "10.0.0.5:9091" {
		t.Errorf("unexpected targets JSON: %+v", gotTargets)
	}

	// 4. /healthz
	hzReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	hzResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(hzResp, hzReq)
	if hzResp.Code != http.StatusOK || !strings.Contains(hzResp.Body.String(), `"ok"`) {
		t.Errorf("GET /healthz = (%d, %q), want 200 ok", hzResp.Code, hzResp.Body.String())
	}
}

func TestObservabilityServer_ErrorAndMissingMetricsHandler(t *testing.T) {
	srv := NewObservabilityServer(":0", nil, &stubTargetProvider{err: errors.New("boom")}, slog.Default())

	mResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(mResp, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if mResp.Code != http.StatusNotFound {
		t.Errorf("GET /metrics without handler status = %d, want 404", mResp.Code)
	}

	tResp := httptest.NewRecorder()
	srv.Handler().ServeHTTP(tResp, httptest.NewRequest(http.MethodGet, "/api/v1/targets", nil))
	if tResp.Code != http.StatusInternalServerError {
		t.Errorf("GET /api/v1/targets on error status = %d, want 500", tResp.Code)
	}
}
