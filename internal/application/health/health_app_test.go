package health

import (
	"context"
	"log/slog"
	"testing"

	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestReportEndpointHealth_PersistsWithLease(t *testing.T) {
	healthStore := apptest.NewMockEndpointHealthStore()
	tracker := apptest.NewMockNodeTracker()
	logger := slog.Default()

	svc := NewHealthAppService(healthStore, tracker, logger)

	tracker.TrackNode("node-1")

	report := &model.EndpointHealth{
		ServiceID: model.ServiceID{Namespace: "prod", Name: "api"},
		RouteName: "edp1",
		Status:    model.EndpointHealthUnhealthy,
		ProbeType: model.HealthProbeTCP,
		NodeID:    "node-1",
	}

	err := svc.ReportEndpointHealth(context.Background(), report)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored, err := healthStore.GetEndpointHealth(context.Background(), report.ServiceID, report.RouteName)
	if err != nil {
		t.Fatalf("health not found in store: %v", err)
	}
	if stored.Status != model.EndpointHealthUnhealthy {
		t.Errorf("expected UNHEALTHY, got %v", stored.Status)
	}
}

func TestReportEndpointHealth_NilTracker(t *testing.T) {
	healthStore := apptest.NewMockEndpointHealthStore()
	logger := slog.Default()

	svc := NewHealthAppService(healthStore, nil, logger)

	report := &model.EndpointHealth{
		ServiceID: model.ServiceID{Namespace: "prod", Name: "api"},
		RouteName: "edp1",
		Status:    model.EndpointHealthHealthy,
		NodeID:    "node-1",
	}

	err := svc.ReportEndpointHealth(context.Background(), report)
	if err != nil {
		t.Fatalf("unexpected error with nil tracker: %v", err)
	}

	stored, err := healthStore.GetEndpointHealth(context.Background(), report.ServiceID, report.RouteName)
	if err != nil {
		t.Fatalf("health not found in store: %v", err)
	}
	if stored.Status != model.EndpointHealthHealthy {
		t.Errorf("expected HEALTHY, got %v", stored.Status)
	}
}

func TestDrainService_SetsDrainingStatus(t *testing.T) {
	healthStore := apptest.NewMockEndpointHealthStore()
	tracker := apptest.NewMockNodeTracker()
	logger := slog.Default()

	svc := NewHealthAppService(healthStore, tracker, logger)

	serviceID := model.ServiceID{Namespace: "prod", Name: "api"}
	routeName := "edp1"

	err := svc.DrainService(context.Background(), serviceID, routeName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored, err := healthStore.GetEndpointHealth(context.Background(), serviceID, routeName)
	if err != nil {
		t.Fatalf("health not found in store: %v", err)
	}
	if stored.Status != model.EndpointHealthDraining {
		t.Errorf("expected DRAINING, got %v", stored.Status)
	}
}

func TestDrainService_NotLeaseAttached(t *testing.T) {
	healthStore := apptest.NewMockEndpointHealthStore()
	tracker := apptest.NewMockNodeTracker()
	logger := slog.Default()

	svc := NewHealthAppService(healthStore, tracker, logger)

	serviceID := model.ServiceID{Namespace: "prod", Name: "api"}
	_ = svc.DrainService(context.Background(), serviceID, "edp1")

	stored, _ := healthStore.GetEndpointHealth(context.Background(), serviceID, "edp1")
	if stored == nil {
		t.Fatal("drain report not persisted")
	}
	if stored.Status != model.EndpointHealthDraining {
		t.Errorf("expected DRAINING status, got %v", stored.Status)
	}
}
