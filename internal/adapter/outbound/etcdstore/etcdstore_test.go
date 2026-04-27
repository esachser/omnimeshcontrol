package etcdstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"testing"
	"time"

	tcetcd "github.com/testcontainers/testcontainers-go/modules/etcd"
	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// testEtcdClient starts an etcd container for the test and returns a connected
// client plus a cleanup function. Each call creates an independent container so
// tests can run in parallel safely.
func testEtcdClient(t *testing.T) (*clientv3.Client, func()) {
	t.Helper()

	ctx := context.Background()

	container, err := tcetcd.Run(ctx, "gcr.io/etcd-development/etcd:v3.5.21")
	if err != nil {
		t.Fatalf("failed to start etcd container: %v", err)
	}

	endpoint, err := container.ClientEndpoint(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("failed to get etcd client endpoint: %v", err)
	}

	client, err := NewEtcdClient([]string{endpoint})
	if err != nil {
		_ = container.Terminate(ctx)
		t.Fatalf("failed to create etcd client: %v", err)
	}

	cleanup := func() {
		_ = client.Close()
		_ = container.Terminate(ctx)
	}

	return client, cleanup
}

func TestEtcdResourceStore_PutAndGet(t *testing.T) {
	client, cleanup := testEtcdClient(t)
	defer cleanup()

	store := NewEtcdResourceStore(client)
	ctx := context.Background()

	resource := &model.Resource{
		APIVersion: "core.omnimesh.io/v1",
		Kind:       "Service",
		Metadata: model.ResourceMetadata{
			Name:      "order-service",
			Namespace: "production",
		},
		Spec: json.RawMessage(`{"protocol": "HTTP", "port": 8080}`),
	}

	rev, err := store.Put(ctx, "core.omnimesh.io/v1/Service/production/order-service", resource)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if rev == 0 {
		t.Error("Put() returned revision 0")
	}

	got, err := store.Get(ctx, "core.omnimesh.io/v1/Service/production/order-service")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Metadata.Name != "order-service" {
		t.Errorf("Get() name = %q, want %q", got.Metadata.Name, "order-service")
	}
	if got.Metadata.Revision == 0 {
		t.Error("Get() returned resource with revision 0")
	}
}

func TestEtcdResourceStore_GetNotFound(t *testing.T) {
	client, cleanup := testEtcdClient(t)
	defer cleanup()

	store := NewEtcdResourceStore(client)
	ctx := context.Background()

	_, err := store.Get(ctx, "nonexistent")
	if err == nil {
		t.Error("Get() expected error for nonexistent key")
	}
}

func TestEtcdResourceStore_List(t *testing.T) {
	client, cleanup := testEtcdClient(t)
	defer cleanup()

	store := NewEtcdResourceStore(client)
	ctx := context.Background()

	// Create multiple resources.
	for i := range 3 {
		r := &model.Resource{
			APIVersion: "core.omnimesh.io/v1",
			Kind:       "Service",
			Metadata: model.ResourceMetadata{
				Name:      fmt.Sprintf("svc-%d", i),
				Namespace: "production",
			},
		}
		if _, err := store.Put(ctx, fmt.Sprintf("core.omnimesh.io/v1/Service/production/svc-%d", i), r); err != nil {
			t.Fatalf("Put() error = %v", err)
		}
	}

	resources, err := store.List(ctx, "core.omnimesh.io/v1/Service/production/")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(resources) != 3 {
		t.Errorf("List() returned %d resources, want 3", len(resources))
	}
}

func TestEtcdResourceStore_Delete(t *testing.T) {
	client, cleanup := testEtcdClient(t)
	defer cleanup()

	store := NewEtcdResourceStore(client)
	ctx := context.Background()

	r := &model.Resource{
		APIVersion: "core.omnimesh.io/v1",
		Kind:       "Service",
		Metadata:   model.ResourceMetadata{Name: "to-delete", Namespace: "test"},
	}
	if _, err := store.Put(ctx, "test/to-delete", r); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	if err := store.Delete(ctx, "test/to-delete"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Should not be found now.
	_, err := store.Get(ctx, "test/to-delete")
	if err == nil {
		t.Error("Get() after Delete() expected error")
	}
}

func TestEtcdResourceStore_CompareAndPut(t *testing.T) {
	client, cleanup := testEtcdClient(t)
	defer cleanup()

	store := NewEtcdResourceStore(client)
	ctx := context.Background()

	r := &model.Resource{
		APIVersion: "core.omnimesh.io/v1",
		Kind:       "Service",
		Metadata:   model.ResourceMetadata{Name: "cas-test", Namespace: "test"},
	}
	rev, err := store.Put(ctx, "test/cas-test", r)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	// Should succeed with correct revision.
	r.Spec = json.RawMessage(`{"updated": true}`)
	_, err = store.CompareAndPut(ctx, "test/cas-test", rev, r)
	if err != nil {
		t.Fatalf("CompareAndPut() with correct revision: error = %v", err)
	}

	// Should fail with stale revision.
	_, err = store.CompareAndPut(ctx, "test/cas-test", rev, r)
	if err == nil {
		t.Error("CompareAndPut() with stale revision: expected error")
	}
}

func TestEtcdResourceStore_Watch(t *testing.T) {
	client, cleanup := testEtcdClient(t)
	defer cleanup()

	store := NewEtcdResourceStore(client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eventCh, err := store.Watch(ctx, "core.omnimesh.io/v1/Service/production/")
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}

	// Write after starting watch.
	go func() {
		time.Sleep(100 * time.Millisecond)
		r := &model.Resource{
			APIVersion: "core.omnimesh.io/v1",
			Kind:       "Service",
			Metadata:   model.ResourceMetadata{Name: "watched", Namespace: "production"},
		}
		store.Put(context.Background(), "core.omnimesh.io/v1/Service/production/watched", r)
	}()

	select {
	case event := <-eventCh:
		if event.Type != outbound.ResourceEventPut {
			t.Errorf("Watch() event type = %v, want Put", event.Type)
		}
		if event.Resource == nil {
			t.Error("Watch() event resource is nil")
		}
	case <-ctx.Done():
		t.Error("Watch() timed out waiting for event")
	}
}

func TestEtcdStateStore_IPAllocation(t *testing.T) {
	client, cleanup := testEtcdClient(t)
	defer cleanup()

	stateStore, err := NewEtcdStateStore(client)
	if err != nil {
		t.Fatalf("NewEtcdStateStore() error = %v", err)
	}
	defer stateStore.Close()

	ctx := context.Background()
	cidr := netip.MustParsePrefix("100.64.0.0/24")
	svcID := model.ServiceID{Namespace: "production", Name: "order-service"}

	// Allocate IP.
	addr, err := stateStore.AllocateIP(ctx, svcID, cidr)
	if err != nil {
		t.Fatalf("AllocateIP() error = %v", err)
	}
	if !cidr.Contains(addr) {
		t.Errorf("AllocateIP() returned %v which is not in CIDR %v", addr, cidr)
	}

	// Get allocation.
	got, err := stateStore.GetIPAllocation(ctx, svcID)
	if err != nil {
		t.Fatalf("GetIPAllocation() error = %v", err)
	}
	if got != addr {
		t.Errorf("GetIPAllocation() = %v, want %v", got, addr)
	}

	// Duplicate allocation should fail.
	_, err = stateStore.AllocateIP(ctx, svcID, cidr)
	if err == nil {
		t.Error("AllocateIP() duplicate should return error")
	}

	// Release IP.
	if err := stateStore.ReleaseIP(ctx, svcID); err != nil {
		t.Fatalf("ReleaseIP() error = %v", err)
	}

	// Get allocation should fail after release.
	_, err = stateStore.GetIPAllocation(ctx, svcID)
	if err == nil {
		t.Error("GetIPAllocation() after release should return error")
	}
}

func TestEtcdStateStore_NodeLifecycle(t *testing.T) {
	client, cleanup := testEtcdClient(t)
	defer cleanup()

	stateStore, err := NewEtcdStateStore(client)
	if err != nil {
		t.Fatalf("NewEtcdStateStore() error = %v", err)
	}
	defer stateStore.Close()

	ctx := context.Background()
	node := &model.Node{
		ID:       "node-1",
		Platform: model.NodePlatformLinux,
		Status:   model.NodeStatusActive,
	}

	// Persist node (leaseID=0: not attached to a node lease in this low-level test).
	if err := stateStore.PersistNode(ctx, "node-1", node, 0); err != nil {
		t.Fatalf("PersistNode() error = %v", err)
	}

	// Verify node can be retrieved.
	got, err := stateStore.GetNode(ctx, "node-1")
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if got.ID != "node-1" {
		t.Errorf("GetNode() ID = %q, want %q", got.ID, "node-1")
	}
}

// Suppress unused import.
var _ = domain.ErrResourceNotFound
