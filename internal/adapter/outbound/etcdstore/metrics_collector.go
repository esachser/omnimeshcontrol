package etcdstore

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/application/mapper"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type resourceMetricKey struct {
	kind      string
	namespace string
	apiGroup  string
}

// etcdMetricsSource abstracts the etcd read/status calls needed by MetricsCollector
// so it can be unit-tested without a live etcd cluster.
type etcdMetricsSource interface {
	ListRawResources(ctx context.Context) ([][]byte, error)
	CountAllocatedIPs(ctx context.Context) (int, error)
	ClusterStatus(ctx context.Context) (isLeader bool, dbSizeBytes int64, peers int, err error)
}

type clientMetricsSource struct {
	client *clientv3.Client
}

func (s *clientMetricsSource) ListRawResources(ctx context.Context) ([][]byte, error) {
	resp, err := s.client.Get(ctx, resourcePrefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	out := make([][]byte, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		out = append(out, kv.Value)
	}
	return out, nil
}

func (s *clientMetricsSource) CountAllocatedIPs(ctx context.Context) (int, error) {
	resp, err := s.client.Get(ctx, ipAllocPrefix, clientv3.WithPrefix(), clientv3.WithCountOnly())
	if err != nil {
		return 0, err
	}
	return int(resp.Count), nil
}

func (s *clientMetricsSource) ClusterStatus(ctx context.Context) (bool, int64, int, error) {
	endpoints := s.client.Endpoints()
	var (
		isLeader    bool
		dbSizeBytes int64
	)
	if len(endpoints) > 0 {
		st, err := s.client.Status(ctx, endpoints[0])
		if err != nil {
			return false, 0, 0, err
		}
		dbSizeBytes = st.DbSize
		isLeader = st.Leader != 0 && st.Leader == st.Header.MemberId
	}
	ml, err := s.client.MemberList(ctx)
	if err != nil {
		return isLeader, dbSizeBytes, 0, err
	}
	return isLeader, dbSizeBytes, len(ml.Members), nil
}

// MetricsCollector periodically collects RFC-0005 §4.1, §4.2, and §4.4 gauges
// from etcd (resource counts, IPAM utilization, and etcd cluster health).
type MetricsCollector struct {
	source           etcdMetricsSource
	metrics          outbound.MetricsRecorder
	defaultCIDR      netip.Prefix
	interval         time.Duration
	logger           *slog.Logger
	prevResourceKeys map[resourceMetricKey]struct{}
}

// NewMetricsCollector constructs a MetricsCollector backed by client.
func NewMetricsCollector(
	client *clientv3.Client,
	defaultCIDR netip.Prefix,
	interval time.Duration,
	metrics outbound.MetricsRecorder,
	logger *slog.Logger,
) *MetricsCollector {
	return newMetricsCollectorWithSource(&clientMetricsSource{client: client}, defaultCIDR, interval, metrics, logger)
}

func newMetricsCollectorWithSource(
	source etcdMetricsSource,
	defaultCIDR netip.Prefix,
	interval time.Duration,
	metrics outbound.MetricsRecorder,
	logger *slog.Logger,
) *MetricsCollector {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &MetricsCollector{
		source:           source,
		metrics:          ensureMetrics(metrics),
		defaultCIDR:      defaultCIDR,
		interval:         interval,
		logger:           logger,
		prevResourceKeys: make(map[resourceMetricKey]struct{}),
	}
}

// Run starts the periodic collection loop until ctx is cancelled.
func (c *MetricsCollector) Run(ctx context.Context) {
	c.CollectOnce(ctx)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.CollectOnce(ctx)
		}
	}
}

// CollectOnce runs a single collection pass across resources, IPAM, and etcd status.
func (c *MetricsCollector) CollectOnce(ctx context.Context) {
	if c.source == nil {
		return
	}
	c.collectResourcesAndIPAM(ctx)
	c.collectClusterStatus(ctx)
}

func (c *MetricsCollector) collectResourcesAndIPAM(ctx context.Context) {
	rawResources, err := c.source.ListRawResources(ctx)
	if err != nil {
		c.logger.Warn("etcd metrics collector: list resources failed", "error", err)
		return
	}

	counts := make(map[resourceMetricKey]int)
	var namespaces []*model.Namespace

	for _, raw := range rawResources {
		var r model.Resource
		if err := json.Unmarshal(raw, &r); err != nil {
			continue
		}
		key := resourceMetricKey{
			kind:      r.Kind,
			namespace: r.Metadata.Namespace,
			apiGroup:  apiGroupFromVersion(r.APIVersion),
		}
		counts[key]++

		if r.Kind == "Namespace" {
			if ns, mapErr := mapper.ResourceToNamespace(&r); mapErr == nil {
				namespaces = append(namespaces, ns)
			}
		}
	}

	// Zero out keys that disappeared since the last pass so Prometheus gauges don't go stale.
	for prevKey := range c.prevResourceKeys {
		if _, stillPresent := counts[prevKey]; !stillPresent {
			c.metrics.SetGauge(ctx, model.MetricCPResourcesTotal, 0, map[string]string{
				"kind":      prevKey.kind,
				"namespace": prevKey.namespace,
				"api_group": prevKey.apiGroup,
			})
		}
	}

	nextKeys := make(map[resourceMetricKey]struct{}, len(counts))
	for key, count := range counts {
		nextKeys[key] = struct{}{}
		c.metrics.SetGauge(ctx, model.MetricCPResourcesTotal, float64(count), map[string]string{
			"kind":      key.kind,
			"namespace": key.namespace,
			"api_group": key.apiGroup,
		})
	}
	c.prevResourceKeys = nextKeys

	allocated, err := c.source.CountAllocatedIPs(ctx)
	if err != nil {
		c.logger.Warn("etcd metrics collector: count allocated IPs failed", "error", err)
		return
	}

	capacity := computeIPAMCapacity(namespaces, c.defaultCIDR)
	c.metrics.SetGauge(ctx, model.MetricCPIpamAllocated, float64(allocated), nil)
	c.metrics.SetGauge(ctx, model.MetricCPIpamCapacity, float64(capacity), nil)
	ratio := 0.0
	if capacity > 0 {
		ratio = float64(allocated) / float64(capacity)
	}
	c.metrics.SetGauge(ctx, model.MetricCPIpamUtilizationRatio, ratio, nil)
}

func (c *MetricsCollector) collectClusterStatus(ctx context.Context) {
	isLeader, dbSize, peers, err := c.source.ClusterStatus(ctx)
	if err != nil {
		c.logger.Warn("etcd metrics collector: cluster status failed", "error", err)
		return
	}
	leaderVal := 0.0
	if isLeader {
		leaderVal = 1.0
	}
	c.metrics.SetGauge(ctx, model.MetricCPEtcdLeader, leaderVal, nil)
	c.metrics.SetGauge(ctx, model.MetricCPEtcdDBSizeBytes, float64(dbSize), nil)
	c.metrics.SetGauge(ctx, model.MetricCPEtcdPeersConnected, float64(peers), nil)
}

func apiGroupFromVersion(apiVersion string) string {
	idx := strings.IndexByte(apiVersion, '/')
	if idx <= 0 {
		return apiVersion
	}
	return apiVersion[:idx]
}

func computeIPAMCapacity(namespaces []*model.Namespace, defaultCIDR netip.Prefix) uint64 {
	seen := make(map[netip.Prefix]struct{})
	var total uint64
	for _, ns := range namespaces {
		if ns == nil || !ns.CIDR.IsValid() || !ns.CIDR.Addr().Is4() {
			continue
		}
		if _, dup := seen[ns.CIDR]; dup {
			continue
		}
		seen[ns.CIDR] = struct{}{}
		total += prefixCapacityV4(ns.CIDR)
	}
	if total == 0 && defaultCIDR.IsValid() && defaultCIDR.Addr().Is4() {
		total = prefixCapacityV4(defaultCIDR)
	}
	return total
}

func prefixCapacityV4(prefix netip.Prefix) uint64 {
	hostBits := 32 - prefix.Bits()
	if hostBits <= 0 {
		return 1
	}
	size := uint64(1) << hostBits
	if size <= 2 {
		return size
	}
	// Reserve network and broadcast addresses.
	return size - 2
}
