package etcdstore

import (
	"context"
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

func TestEtcdRouteDistributor_AnnounceAndAllRoutes(t *testing.T) {
	embedded, cleanup := testEmbedded(t)
	defer cleanup()

	resourceStore := NewEtcdResourceStore(embedded.Client())
	dist := NewEtcdRouteDistributor(resourceStore, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Start Run in background.
	runDone := make(chan error, 1)
	go func() { runDone <- dist.Run(ctx) }()

	route := model.TargetRoute{
		Name:          "route-1",
		Namespace:     "production",
		TargetService: "order-service",
		NodeID:        "node-abc",
		Labels:        map[string]string{"env": "prod"},
	}

	if err := dist.AnnounceRoute(ctx, route); err != nil {
		t.Fatalf("AnnounceRoute() error = %v", err)
	}

	// Wait for watcher to update cache.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		all := dist.AllRoutes()
		if len(all) == 1 && all[0].Name == "route-1" && all[0].NodeID == "node-abc" {
			return // success
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("AllRoutes() did not contain announced route within deadline; got %v", dist.AllRoutes())
}

func TestEtcdRouteDistributor_WithdrawRoute(t *testing.T) {
	embedded, cleanup := testEmbedded(t)
	defer cleanup()

	resourceStore := NewEtcdResourceStore(embedded.Client())
	dist := NewEtcdRouteDistributor(resourceStore, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go dist.Run(ctx) //nolint:errcheck

	route := model.TargetRoute{
		Name:          "route-x",
		Namespace:     "default",
		TargetService: "svc",
		NodeID:        "node-1",
	}

	if err := dist.AnnounceRoute(ctx, route); err != nil {
		t.Fatalf("AnnounceRoute() error = %v", err)
	}

	// Wait for announce to land.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(dist.AllRoutes()) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := dist.WithdrawRoute(ctx, route); err != nil {
		t.Fatalf("WithdrawRoute() error = %v", err)
	}

	// Wait for withdraw to land.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(dist.AllRoutes()) == 0 {
			return // success
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("AllRoutes() still has %d routes after withdraw", len(dist.AllRoutes()))
}

func TestEtcdRouteDistributor_SeedFromExistingEtcdData(t *testing.T) {
	embedded, cleanup := testEmbedded(t)
	defer cleanup()

	resourceStore := NewEtcdResourceStore(embedded.Client())
	ctx := context.Background()

	// Write a route BEFORE Run is called (simulates a restart with existing data).
	pre := model.TargetRoute{
		Name:          "pre-existing",
		Namespace:     "ns",
		TargetService: "svc",
		NodeID:        "node-z",
	}
	if _, err := resourceStore.Put(ctx, targetRouteKey(pre.Namespace, pre.NodeID, pre.TargetService, pre.Name), routeToResource(&pre)); err != nil {
		t.Fatalf("pre-seed Put() error = %v", err)
	}

	dist := NewEtcdRouteDistributor(resourceStore, nil)

	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// loadAll is synchronous before the watch loop begins.
	go dist.Run(runCtx) //nolint:errcheck
	time.Sleep(200 * time.Millisecond) // give Run time to seed

	all := dist.AllRoutes()
	if len(all) != 1 || all[0].Name != "pre-existing" {
		t.Errorf("AllRoutes() after seed = %v, want 1 pre-existing route", all)
	}
}
