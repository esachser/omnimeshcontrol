package service

import (
	"context"
	"net/netip"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/application/namespace"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestServiceAppService_Telemetry(t *testing.T) {
	resources := apptest.NewMockResourceStore()
	state := apptest.NewMockStateStore()
	dns := apptest.NewMockDNSManager()
	identity := apptest.NewMockIdentityManager()
	metrics := apptest.NewRecordingMetricsRecorder()
	tracer := apptest.NewRecordingTraceExporter()

	nsApp := namespace.NewNamespaceAppService(resources, netip.MustParsePrefix("100.64.0.0/10"), nil)
	if err := nsApp.Apply(context.Background(), &model.Namespace{Name: "prod"}); err != nil {
		t.Fatalf("Apply namespace error = %v", err)
	}

	svcApp := NewServiceAppService(ServiceAppServiceConfig{
		Resources:  resources,
		State:      state,
		DNS:        dns,
		Identity:   identity,
		Namespaces: nsApp,
		DNSZone:    "omnimesh.local",
		Metrics:    metrics,
		Tracer:     tracer,
	})

	ctx := context.Background()
	svc := &model.Service{
		ID:       model.ServiceID{Namespace: "prod", Name: "api"},
		Protocol: model.ServiceProtocolHTTP,
		Port:     8080,
	}
	if err := svcApp.Apply(ctx, svc); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if got := metrics.CountByName(model.MetricCPIdentityRegistrationsTotal); got != 1 {
		t.Errorf("identity.registrations.total = %d, want 1", got)
	}
	if got := metrics.CountByName(model.MetricCPIdentityRegistrationsDurationMs); got != 1 {
		t.Errorf("identity.registrations.duration_ms = %d, want 1", got)
	}
	if got := metrics.CountByName(model.MetricCPDNSUpsertsTotal); got != 1 {
		t.Errorf("dns.upserts.total = %d, want 1", got)
	}
	if got := metrics.CountByName(model.MetricCPDNSUpsertsDurationMs); got != 1 {
		t.Errorf("dns.upserts.duration_ms = %d, want 1", got)
	}

	if err := svcApp.Delete(ctx, "prod", "api"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if got := metrics.CountByName(model.MetricCPDNSDeletesTotal); got != 1 {
		t.Errorf("dns.deletes.total = %d, want 1", got)
	}
	if got := metrics.CountByName(model.MetricCPIdentityDeregistrationsTotal); got != 1 {
		t.Errorf("identity.deregistrations.total = %d, want 1", got)
	}
	if len(tracer.Spans) != 2 {
		t.Errorf("expected 2 spans (Apply, Delete), got %d", len(tracer.Spans))
	}
}
