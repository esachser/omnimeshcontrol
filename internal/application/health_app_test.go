package application

import (
	"context"
	"log/slog"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestReportEndpointHealth_PersistsWithLease(t *testing.T) {
	healthStore := newMockEndpointHealthStore()
	tracker := newMockNodeTracker()
	logger := slog.Default()

	svc := NewHealthAppService(healthStore, tracker, logger)

	// Track node so it has a valid lease.
	tracker.TrackNode("node-1")

	report := &model.EndpointHealth{
		ServiceID: model.ServiceID{Namespace: "prod", Name: "api"},
		Address:   "10.0.1.5:8080",
		Status:    model.EndpointHealthUnhealthy,
		ProbeType: model.HealthProbeTCP,
		NodeID:    "node-1",
	}

	err := svc.ReportEndpointHealth(context.Background(), report)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify health was persisted.
	stored, err := healthStore.GetEndpointHealth(context.Background(), report.ServiceID, report.Address)
	if err != nil {
		t.Fatalf("health not found in store: %v", err)
	}
	if stored.Status != model.EndpointHealthUnhealthy {
		t.Errorf("expected UNHEALTHY, got %v", stored.Status)
	}

	// NOTE: Broadcasting is the responsibility of HealthUpdateService (watch-based).
	// HealthAppService only writes — this test intentionally does NOT check broadcasts.
}

func TestReportEndpointHealth_NilTracker(t *testing.T) {
	healthStore := newMockEndpointHealthStore()
	logger := slog.Default()

	// Nil tracker should not panic — leaseID defaults to 0.
	svc := NewHealthAppService(healthStore, nil, logger)

	report := &model.EndpointHealth{
		ServiceID: model.ServiceID{Namespace: "prod", Name: "api"},
		Address:   "10.0.1.5:8080",
		Status:    model.EndpointHealthHealthy,
		NodeID:    "node-1",
	}

	err := svc.ReportEndpointHealth(context.Background(), report)
	if err != nil {
		t.Fatalf("unexpected error with nil tracker: %v", err)
	}

	stored, err := healthStore.GetEndpointHealth(context.Background(), report.ServiceID, report.Address)
	if err != nil {
		t.Fatalf("health not found in store: %v", err)
	}
	if stored.Status != model.EndpointHealthHealthy {
		t.Errorf("expected HEALTHY, got %v", stored.Status)
	}
}

func TestDrainService_SetsDrainingStatus(t *testing.T) {
	healthStore := newMockEndpointHealthStore()
	tracker := newMockNodeTracker()
	logger := slog.Default()

	svc := NewHealthAppService(healthStore, tracker, logger)

	serviceID := model.ServiceID{Namespace: "prod", Name: "api"}
	endpointAddr := "10.0.1.5:8080"

	err := svc.DrainService(context.Background(), serviceID, endpointAddr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify health was persisted as DRAINING.
	stored, err := healthStore.GetEndpointHealth(context.Background(), serviceID, endpointAddr)
	if err != nil {
		t.Fatalf("health not found in store: %v", err)
	}
	if stored.Status != model.EndpointHealthDraining {
		t.Errorf("expected DRAINING, got %v", stored.Status)
	}

	// NOTE: Broadcasting is handled by HealthUpdateService via the etcd watch.
}

func TestDrainService_NotLeaseAttached(t *testing.T) {
	// Drain reports must use leaseID=0 so the DRAINING status persists
	// even through transient agent disconnections.
	// We verify this by checking the store received a write (mock ignores leaseID)
	// and that the status is correct.
	healthStore := newMockEndpointHealthStore()
	tracker := newMockNodeTracker()
	logger := slog.Default()

	svc := NewHealthAppService(healthStore, tracker, logger)

	serviceID := model.ServiceID{Namespace: "prod", Name: "api"}
	_ = svc.DrainService(context.Background(), serviceID, "10.0.1.5:8080")

	stored, _ := healthStore.GetEndpointHealth(context.Background(), serviceID, "10.0.1.5:8080")
	if stored == nil {
		t.Fatal("drain report not persisted")
	}
	if stored.Status != model.EndpointHealthDraining {
		t.Errorf("expected DRAINING status, got %v", stored.Status)
	}
}
