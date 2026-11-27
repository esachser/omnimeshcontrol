package etcdstore

import (
	"context"
	"encoding/json"
	"net/netip"
	"testing"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/application/apptest"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

type stubEtcdMetricsSource struct {
	resources [][]byte
	allocated int
	isLeader  bool
	dbSize    int64
	peers     int
}

func (s *stubEtcdMetricsSource) ListRawResources(_ context.Context) ([][]byte, error) {
	return s.resources, nil
}

func (s *stubEtcdMetricsSource) CountAllocatedIPs(_ context.Context) (int, error) {
	return s.allocated, nil
}

func (s *stubEtcdMetricsSource) ClusterStatus(_ context.Context) (bool, int64, int, error) {
	return s.isLeader, s.dbSize, s.peers, nil
}

func TestMetricsCollector_CollectOnce(t *testing.T) {
	svcRes, _ := json.Marshal(model.Resource{
		APIVersion: "core.omnimesh.io/v1",
		Kind:       "Service",
		Metadata:   model.ResourceMetadata{Name: "api", Namespace: "prod"},
	})
	nsRes, _ := json.Marshal(model.Resource{
		APIVersion: "core.omnimesh.io/v1",
		Kind:       "Namespace",
		Metadata:   model.ResourceMetadata{Name: "prod"},
		Spec:       json.RawMessage(`{"cidr":"100.64.0.0/24","isolation":"strict"}`),
	})

	source := &stubEtcdMetricsSource{
		resources: [][]byte{svcRes, nsRes},
		allocated: 5,
		isLeader:  true,
		dbSize:    4096,
		peers:     3,
	}

	rec := apptest.NewRecordingMetricsRecorder()
	collector := newMetricsCollectorWithSource(
		source,
		netip.MustParsePrefix("100.64.0.0/10"),
		time.Second,
		rec,
		nil,
	)

	collector.CollectOnce(context.Background())

	if got := rec.CountByName(model.MetricCPResourcesTotal); got != 2 {
		t.Fatalf("resources.total gauge updates = %d, want 2", got)
	}

	alloc := rec.FindByName(model.MetricCPIpamAllocated)
	if len(alloc) != 1 || alloc[0].Value != 5 {
		t.Errorf("ipam.allocated = %+v, want 5", alloc)
	}
	capMetrics := rec.FindByName(model.MetricCPIpamCapacity)
	if len(capMetrics) != 1 || capMetrics[0].Value != 254 {
		t.Errorf("ipam.capacity = %+v, want 254", capMetrics)
	}
	util := rec.FindByName(model.MetricCPIpamUtilizationRatio)
	if len(util) != 1 || util[0].Value <= 0 {
		t.Errorf("ipam.utilization_ratio = %+v, want > 0", util)
	}

	leader := rec.FindByName(model.MetricCPEtcdLeader)
	if len(leader) != 1 || leader[0].Value != 1 {
		t.Errorf("etcd.leader = %+v, want 1", leader)
	}
	dbSize := rec.FindByName(model.MetricCPEtcdDBSizeBytes)
	if len(dbSize) != 1 || dbSize[0].Value != 4096 {
		t.Errorf("etcd.db_size_bytes = %+v, want 4096", dbSize)
	}
	peers := rec.FindByName(model.MetricCPEtcdPeersConnected)
	if len(peers) != 1 || peers[0].Value != 3 {
		t.Errorf("etcd.peers.connected = %+v, want 3", peers)
	}

	// Verify removed resources are zeroed on the next collection pass.
	source.resources = [][]byte{nsRes}
	collector.CollectOnce(context.Background())

	var foundZeroedService bool
	for _, m := range rec.FindByName(model.MetricCPResourcesTotal) {
		if m.Labels["kind"] == "Service" && m.Value == 0 {
			foundZeroedService = true
		}
	}
	if !foundZeroedService {
		t.Error("expected Service resource gauge to be zeroed after deletion")
	}
}
