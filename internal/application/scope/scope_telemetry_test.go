package scope

import (
	"context"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/application/authz"
	"github.com/esachser/omnimeshcontrol/internal/application/mapper"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	domainservice "github.com/esachser/omnimeshcontrol/internal/domain/service"
)

func TestAgentScopeAppService_Telemetry(t *testing.T) {
	resources := apptest.NewMockResourceStore()
	state := apptest.NewMockStateStore()
	policies := apptest.NewMockTrafficPolicyStore()
	health := apptest.NewMockEndpointHealthStore()
	deps := apptest.NewMockDependencyStore()
	routes := apptest.NewMockRouteDistributor()
	authzApp := authz.NewAuthzAppService(resources, deps, domainservice.NewAuthzEvaluator(), nil)

	_, _ = resources.Put(context.Background(), "core.omnimesh.io/v1/Service/prod/api", mapper.ServiceToResource(&model.Service{
		ID:       model.ServiceID{Namespace: "prod", Name: "api"},
		Protocol: model.ServiceProtocolHTTP,
		Port:     8080,
	}))

	metrics := apptest.NewRecordingMetricsRecorder()
	tracer := apptest.NewRecordingTraceExporter()

	scopeApp := NewAgentScopeAppService(resources, state, policies, health, deps, routes, nil, authzApp, nil, nil)
	scopeApp.SetTelemetry(metrics, tracer)

	sc, err := scopeApp.GetAgentScope(context.Background(), "node-1")
	if err != nil {
		t.Fatalf("GetAgentScope() error = %v", err)
	}
	if len(sc.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(sc.Services))
	}

	hist := metrics.FindByName(model.MetricCPScopeServicesPerAgent)
	if len(hist) != 1 || hist[0].Value != 1 {
		t.Errorf("scope.services_per_agent = %+v, want value 1", hist)
	}
	if len(tracer.Spans) != 1 || !tracer.Spans[0].Ended {
		t.Errorf("expected 1 ended span, got %+v", tracer.Spans)
	}
}
