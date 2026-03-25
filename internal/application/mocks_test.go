package application

import (
	"context"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/domain/service"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// --- Mock ResourceStore ---

type mockResourceStore struct {
	mu   sync.Mutex
	data map[string]*model.Resource
	rev  int64
}

func newMockResourceStore() *mockResourceStore {
	return &mockResourceStore{data: make(map[string]*model.Resource)}
}

func (m *mockResourceStore) Put(_ context.Context, key string, resource *model.Resource) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rev++
	resource.Metadata.Revision = m.rev
	m.data[key] = resource
	return m.rev, nil
}

func (m *mockResourceStore) Get(_ context.Context, key string) (*model.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.data[key]
	if !ok {
		return nil, domain.ErrResourceNotFound
	}
	return r, nil
}

func (m *mockResourceStore) List(_ context.Context, prefix string) ([]*model.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*model.Resource
	for k, v := range m.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			result = append(result, v)
		}
	}
	return result, nil
}

func (m *mockResourceStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[key]; !ok {
		return domain.ErrResourceNotFound
	}
	delete(m.data, key)
	return nil
}

func (m *mockResourceStore) DeleteByPrefix(_ context.Context, prefix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(m.data, k)
		}
	}
	return nil
}

func (m *mockResourceStore) Watch(_ context.Context, _ string) (<-chan outbound.ResourceEvent, error) {
	ch := make(chan outbound.ResourceEvent)
	close(ch)
	return ch, nil
}

func (m *mockResourceStore) CompareAndPut(_ context.Context, key string, expectedRev int64, resource *model.Resource) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.data[key]
	if !ok || existing.Metadata.Revision != expectedRev {
		return 0, domain.ErrRevisionConflict
	}
	m.rev++
	resource.Metadata.Revision = m.rev
	m.data[key] = resource
	return m.rev, nil
}

// --- Mock StateStore ---

type mockStateStore struct {
	mu     sync.Mutex
	ips    map[string]netip.Addr
	nodes  map[string]*model.Node
	nextIP netip.Addr
	routes map[string]*model.TargetRoute
}

func newMockStateStore() *mockStateStore {
	return &mockStateStore{
		ips:    make(map[string]netip.Addr),
		nodes:  make(map[string]*model.Node),
		nextIP: netip.MustParseAddr("100.64.0.1"),
		routes: make(map[string]*model.TargetRoute),
	}
}

func (m *mockStateStore) AllocateIP(_ context.Context, id model.ServiceID, _ netip.Prefix) (netip.Addr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := id.String()
	if _, ok := m.ips[key]; ok {
		return netip.Addr{}, domain.ErrIPConflict
	}
	addr := m.nextIP
	m.ips[key] = addr
	m.nextIP = m.nextIP.Next()
	return addr, nil
}

func (m *mockStateStore) ReleaseIP(_ context.Context, id model.ServiceID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := id.String()
	if _, ok := m.ips[key]; !ok {
		return domain.ErrServiceNotFound
	}
	delete(m.ips, key)
	return nil
}

func (m *mockStateStore) GetIPAllocation(_ context.Context, id model.ServiceID) (netip.Addr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	addr, ok := m.ips[id.String()]
	if !ok {
		return netip.Addr{}, domain.ErrServiceNotFound
	}
	return addr, nil
}

func (m *mockStateStore) GetNode(_ context.Context, nodeID string) (*model.Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	node, ok := m.nodes[nodeID]
	if !ok {
		return nil, domain.ErrNodeNotFound
	}
	return node, nil
}

func (m *mockStateStore) PersistNode(_ context.Context, nodeID string, node *model.Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodes[nodeID] = node
	return nil
}

func (m *mockStateStore) MarkNodeLost(_ context.Context, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodes[nodeID]
	if !ok {
		return domain.ErrNodeNotFound
	}
	n.Status = model.NodeStatusLost
	return nil
}

func (m *mockStateStore) DeregisterNode(_ context.Context, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.nodes[nodeID]; !ok {
		return domain.ErrNodeNotFound
	}
	delete(m.nodes, nodeID)
	return nil
}

func (m *mockStateStore) formatMockRouteKey(namespace, nodeID, serviceName, name string) string {
	return nodeID + "/" + namespace + "/" + serviceName + "/" + name
}

func (m *mockStateStore) PersistTargetRoute(_ context.Context, route *model.TargetRoute) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := m.formatMockRouteKey(route.Namespace, route.NodeID, route.TargetService, route.Name)
	if m.routes == nil {
		m.routes = make(map[string]*model.TargetRoute)
	}
	m.routes[key] = route
	return nil
}

func (m *mockStateStore) DeleteTargetRoute(_ context.Context, namespace, nodeID, serviceName, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := m.formatMockRouteKey(namespace, nodeID, serviceName, name)
	delete(m.routes, key)
	return nil
}

func (m *mockStateStore) GetTargetRoute(_ context.Context, namespace, nodeID, serviceName, name string) (*model.TargetRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := m.formatMockRouteKey(namespace, nodeID, serviceName, name)
	if rt, ok := m.routes[key]; ok {
		return rt, nil
	}
	return nil, domain.ErrResourceNotFound
}

func (m *mockStateStore) ListTargetRoutes(_ context.Context, namespace string) ([]*model.TargetRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*model.TargetRoute
	for _, rt := range m.routes {
		if namespace == "" || rt.Namespace == namespace {
			out = append(out, rt)
		}
	}
	return out, nil
}

func (m *mockStateStore) ListTargetRoutesByNode(_ context.Context, nodeID string) ([]*model.TargetRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*model.TargetRoute
	for _, rt := range m.routes {
		if rt.NodeID == nodeID {
			out = append(out, rt)
		}
	}
	return out, nil
}

func (m *mockStateStore) DeleteTargetRoutesByNode(_ context.Context, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, rt := range m.routes {
		if rt.NodeID == nodeID {
			delete(m.routes, k)
		}
	}
	return nil
}

func (m *mockStateStore) ClearTargetRoutes(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes = make(map[string]*model.TargetRoute)
	return nil
}

func (m *mockStateStore) WatchTargetRoutes(_ context.Context) (<-chan outbound.TargetRouteStateEvent, error) {
	return nil, nil
}

func (m *mockStateStore) IsLeader() bool                                         { return true }
func (m *mockStateStore) WaitForLeader(_ context.Context, _ time.Duration) error { return nil }
func (m *mockStateStore) LeaderAddr() string                                     { return "localhost:2379" }

// --- Mock DNSRecordManager ---

type mockDNSManager struct {
	mu      sync.Mutex
	records map[string]netip.Addr
}

func newMockDNSManager() *mockDNSManager {
	return &mockDNSManager{records: make(map[string]netip.Addr)}
}

func (m *mockDNSManager) UpsertRecord(_ context.Context, fqdn string, addr netip.Addr) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[fqdn] = addr
	return nil
}

func (m *mockDNSManager) DeleteRecord(_ context.Context, fqdn string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, fqdn)
	return nil
}

// --- Mock RouteDistributor ---

type mockRouteDistributor struct {
	mu     sync.RWMutex
	routes map[string]model.TargetRoute
}

func newMockRouteDistributor() *mockRouteDistributor {
	return &mockRouteDistributor{routes: make(map[string]model.TargetRoute)}
}

func (m *mockRouteDistributor) AnnounceRoute(_ context.Context, route model.TargetRoute) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := route.Namespace + "/" + route.NodeID + "/" + route.TargetService + "/" + route.Name
	m.routes[key] = route
	return nil
}

func (m *mockRouteDistributor) WithdrawRoute(_ context.Context, route model.TargetRoute) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := route.Namespace + "/" + route.NodeID + "/" + route.TargetService + "/" + route.Name
	delete(m.routes, key)
	return nil
}

func (m *mockRouteDistributor) AllRoutes() []model.TargetRoute {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var all []model.TargetRoute
	for _, r := range m.routes {
		all = append(all, r)
	}
	return all
}

// --- Mock IdentityManager ---

type mockIdentityManager struct {
	mu         sync.Mutex
	identities map[string]model.ServiceIdentity
}

func newMockIdentityManager() *mockIdentityManager {
	return &mockIdentityManager{identities: make(map[string]model.ServiceIdentity)}
}

func (m *mockIdentityManager) RegisterWorkload(_ context.Context, identity model.ServiceIdentity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.identities[identity.ServiceID.String()] = identity
	return nil
}

func (m *mockIdentityManager) DeregisterWorkload(_ context.Context, id model.ServiceID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.identities, id.String())
	return nil
}

// --- Mock NodeTracker ---

type mockNodeTracker struct {
	mu        sync.Mutex
	nodes     map[string]bool
	callbacks []service.NodeLivenessCallback
}

func newMockNodeTracker() *mockNodeTracker {
	return &mockNodeTracker{nodes: make(map[string]bool)}
}

func (m *mockNodeTracker) TrackNode(nodeID string) {
	m.mu.Lock()
	m.nodes[nodeID] = true
	m.mu.Unlock()
}
func (m *mockNodeTracker) OnDisconnect(nodeID string) {
	m.mu.Lock()
	m.nodes[nodeID] = false
	m.mu.Unlock()
}
func (m *mockNodeTracker) UntrackNode(nodeID string) {
	m.mu.Lock()
	delete(m.nodes, nodeID)
	m.mu.Unlock()
}
func (m *mockNodeTracker) IsAlive(nodeID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nodes[nodeID]
}
func (m *mockNodeTracker) RegisterCallback(callback service.NodeLivenessCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callbacks = append(m.callbacks, callback)
}

// --- Mock TrafficPolicyStore ---

type mockTrafficPolicyStore struct {
	mu       sync.Mutex
	policies map[string]*model.TrafficPolicy
}

func newMockTrafficPolicyStore() *mockTrafficPolicyStore {
	return &mockTrafficPolicyStore{policies: make(map[string]*model.TrafficPolicy)}
}

func (m *mockTrafficPolicyStore) PutTrafficPolicy(_ context.Context, policy *model.TrafficPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := policy.Namespace + "/" + policy.Name
	m.policies[key] = policy
	return nil
}

func (m *mockTrafficPolicyStore) GetTrafficPolicy(_ context.Context, namespace, name string) (*model.TrafficPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.policies[namespace+"/"+name]
	if !ok {
		return nil, domain.ErrPolicyNotFound
	}
	return p, nil
}

func (m *mockTrafficPolicyStore) GetPolicyByService(_ context.Context, serviceID model.ServiceID) (*model.TrafficPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.policies {
		if p.TargetService == serviceID {
			return p, nil
		}
	}
	return nil, domain.ErrPolicyNotFound
}

func (m *mockTrafficPolicyStore) ListTrafficPolicies(_ context.Context, namespace string) ([]*model.TrafficPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*model.TrafficPolicy
	for _, p := range m.policies {
		if p.Namespace == namespace {
			result = append(result, p)
		}
	}
	return result, nil
}

func (m *mockTrafficPolicyStore) DeleteTrafficPolicy(_ context.Context, namespace, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.policies, namespace+"/"+name)
	return nil
}

func (m *mockTrafficPolicyStore) WatchTrafficPolicies(_ context.Context) (<-chan outbound.TrafficPolicyEvent, error) {
	ch := make(chan outbound.TrafficPolicyEvent)
	close(ch)
	return ch, nil
}

// --- Mock EndpointHealthStore ---

type mockEndpointHealthStore struct {
	mu     sync.Mutex
	health map[string]*model.EndpointHealth
}

func newMockEndpointHealthStore() *mockEndpointHealthStore {
	return &mockEndpointHealthStore{health: make(map[string]*model.EndpointHealth)}
}

func (m *mockEndpointHealthStore) PutEndpointHealth(_ context.Context, health *model.EndpointHealth) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s/%s", health.ServiceID, health.Address)
	m.health[key] = health
	return nil
}

func (m *mockEndpointHealthStore) GetEndpointHealth(_ context.Context, serviceID model.ServiceID, addr string) (*model.EndpointHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s/%s", serviceID, addr)
	h, ok := m.health[key]
	if !ok {
		return nil, fmt.Errorf("health not found")
	}
	return h, nil
}

func (m *mockEndpointHealthStore) ListEndpointHealth(_ context.Context, serviceID model.ServiceID) ([]*model.EndpointHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*model.EndpointHealth
	prefix := serviceID.String() + "/"
	for k, v := range m.health {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			result = append(result, v)
		}
	}
	return result, nil
}

func (m *mockEndpointHealthStore) WatchEndpointHealth(_ context.Context) (<-chan outbound.EndpointHealthEvent, error) {
	ch := make(chan outbound.EndpointHealthEvent)
	close(ch)
	return ch, nil
}

// --- Mock DependencyStore ---

type mockDependencyStore struct {
	mu   sync.Mutex
	deps map[string]*model.ServiceDependencies
}

func newMockDependencyStore() *mockDependencyStore {
	return &mockDependencyStore{deps: make(map[string]*model.ServiceDependencies)}
}

func (m *mockDependencyStore) PutDependencies(_ context.Context, deps *model.ServiceDependencies) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deps[deps.ServiceID.String()] = deps
	return nil
}

func (m *mockDependencyStore) GetDependencies(_ context.Context, serviceID model.ServiceID) (*model.ServiceDependencies, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deps[serviceID.String()]
	if !ok {
		return nil, fmt.Errorf("dependencies not found")
	}
	return d, nil
}

func (m *mockDependencyStore) WatchDependencies(_ context.Context) (<-chan outbound.DependencyEvent, error) {
	ch := make(chan outbound.DependencyEvent)
	close(ch)
	return ch, nil
}

// --- Mock RouteManager ---

type mockRouteManager struct {
	mu     sync.Mutex
	routes map[string]*model.TargetRoute
}

func newMockRouteManager() *mockRouteManager {
	return &mockRouteManager{routes: make(map[string]*model.TargetRoute)}
}

func routeManagerKey(namespace, nodeId, serviceName, name string) string {
	return fmt.Sprintf("%s/%s/%s/%s", namespace, nodeId, serviceName, name)
}

func (m *mockRouteManager) Apply(_ context.Context, route *model.TargetRoute) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := routeManagerKey(route.Namespace, route.NodeID, route.TargetService, route.Name)
	cp := *route
	m.routes[key] = &cp
	return nil
}

func (m *mockRouteManager) Get(_ context.Context, namespace, name, serviceName, nodeId string) (*model.TargetRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := routeManagerKey(namespace, nodeId, serviceName, name)
	r, ok := m.routes[key]
	if !ok {
		return nil, domain.ErrResourceNotFound
	}
	cp := *r
	return &cp, nil
}

func (m *mockRouteManager) List(_ context.Context, namespace string) ([]*model.TargetRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*model.TargetRoute
	for _, r := range m.routes {
		if namespace == "" || r.Namespace == namespace {
			cp := *r
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (m *mockRouteManager) Delete(_ context.Context, namespace, name, serviceName, nodeId string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := routeManagerKey(namespace, nodeId, serviceName, name)
	if _, ok := m.routes[key]; !ok {
		return domain.ErrResourceNotFound
	}
	delete(m.routes, key)
	return nil
}

func (m *mockRouteManager) DeleteByNodeId(_ context.Context, nodeId string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, r := range m.routes {
		if r.NodeID == nodeId {
			delete(m.routes, key)
		}
	}
	return nil
}

func (m *mockRouteManager) ClearRoutes(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes = make(map[string]*model.TargetRoute)
	return nil
}
