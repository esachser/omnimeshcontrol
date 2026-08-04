package apptest

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

type MockResourceStore struct {
	mu   sync.Mutex
	Data map[string]*model.Resource
	rev  int64
}

func NewMockResourceStore() *MockResourceStore {
	return &MockResourceStore{Data: make(map[string]*model.Resource)}
}

func (m *MockResourceStore) Put(_ context.Context, key string, resource *model.Resource) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rev++
	resource.Metadata.Revision = m.rev
	m.Data[key] = resource
	return m.rev, nil
}

func (m *MockResourceStore) Get(_ context.Context, key string) (*model.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.Data[key]
	if !ok {
		return nil, domain.ErrResourceNotFound
	}
	return r, nil
}

func (m *MockResourceStore) List(_ context.Context, prefix string) ([]*model.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*model.Resource
	for k, v := range m.Data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			result = append(result, v)
		}
	}
	return result, nil
}

func (m *MockResourceStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.Data[key]; !ok {
		return domain.ErrResourceNotFound
	}
	delete(m.Data, key)
	return nil
}

func (m *MockResourceStore) DeleteByPrefix(_ context.Context, prefix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.Data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(m.Data, k)
		}
	}
	return nil
}

func (m *MockResourceStore) Watch(_ context.Context, _ string) (<-chan outbound.ResourceEvent, error) {
	ch := make(chan outbound.ResourceEvent)
	close(ch)
	return ch, nil
}

func (m *MockResourceStore) CompareAndPut(_ context.Context, key string, expectedRev int64, resource *model.Resource) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.Data[key]
	if !ok || existing.Metadata.Revision != expectedRev {
		return 0, domain.ErrRevisionConflict
	}
	m.rev++
	resource.Metadata.Revision = m.rev
	m.Data[key] = resource
	return m.rev, nil
}

// --- Mock StateStore ---

type MockStateStore struct {
	mu     sync.Mutex
	IPs    map[string]netip.Addr
	Nodes  map[string]*model.Node
	nextIP netip.Addr
	Routes map[string]*model.TargetRoute
}

func NewMockStateStore() *MockStateStore {
	return &MockStateStore{
		IPs:    make(map[string]netip.Addr),
		Nodes:  make(map[string]*model.Node),
		nextIP: netip.MustParseAddr("100.64.0.1"),
		Routes: make(map[string]*model.TargetRoute),
	}
}

func (m *MockStateStore) AllocateIP(_ context.Context, id model.ServiceID, _ netip.Prefix) (netip.Addr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := id.String()
	if _, ok := m.IPs[key]; ok {
		return netip.Addr{}, domain.ErrIPConflict
	}
	addr := m.nextIP
	m.IPs[key] = addr
	m.nextIP = m.nextIP.Next()
	return addr, nil
}

func (m *MockStateStore) ReleaseIP(_ context.Context, id model.ServiceID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := id.String()
	if _, ok := m.IPs[key]; !ok {
		return domain.ErrServiceNotFound
	}
	delete(m.IPs, key)
	return nil
}

func (m *MockStateStore) GetIPAllocation(_ context.Context, id model.ServiceID) (netip.Addr, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	addr, ok := m.IPs[id.String()]
	if !ok {
		return netip.Addr{}, domain.ErrServiceNotFound
	}
	return addr, nil
}

func (m *MockStateStore) GetNode(_ context.Context, nodeID string) (*model.Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	node, ok := m.Nodes[nodeID]
	if !ok {
		return nil, domain.ErrNodeNotFound
	}
	return node, nil
}

func (m *MockStateStore) PersistNode(_ context.Context, nodeID string, node *model.Node, _ int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Nodes[nodeID] = node
	return nil
}

func (m *MockStateStore) formatMockRouteKey(namespace, nodeID, serviceName, name string) string {
	return nodeID + "/" + namespace + "/" + serviceName + "/" + name
}

func (m *MockStateStore) PersistTargetRoute(_ context.Context, route *model.TargetRoute, _ int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := m.formatMockRouteKey(route.Namespace, route.NodeID, route.TargetService, route.Name)
	if m.Routes == nil {
		m.Routes = make(map[string]*model.TargetRoute)
	}
	m.Routes[key] = route
	return nil
}

func (m *MockStateStore) DeleteTargetRoute(_ context.Context, namespace, nodeID, serviceName, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := m.formatMockRouteKey(namespace, nodeID, serviceName, name)
	delete(m.Routes, key)
	return nil
}

func (m *MockStateStore) GetTargetRoute(_ context.Context, namespace, nodeID, serviceName, name string) (*model.TargetRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := m.formatMockRouteKey(namespace, nodeID, serviceName, name)
	if rt, ok := m.Routes[key]; ok {
		return rt, nil
	}
	return nil, domain.ErrResourceNotFound
}

func (m *MockStateStore) ListTargetRoutes(_ context.Context, namespace string) ([]*model.TargetRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*model.TargetRoute
	for _, rt := range m.Routes {
		if namespace == "" || rt.Namespace == namespace {
			out = append(out, rt)
		}
	}
	return out, nil
}

func (m *MockStateStore) ListTargetRoutesByNode(_ context.Context, nodeID string) ([]*model.TargetRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*model.TargetRoute
	for _, rt := range m.Routes {
		if rt.NodeID == nodeID {
			out = append(out, rt)
		}
	}
	return out, nil
}

func (m *MockStateStore) ClearTargetRoutes(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Routes = make(map[string]*model.TargetRoute)
	return nil
}

func (m *MockStateStore) WatchTargetRoutes(_ context.Context) (<-chan outbound.TargetRouteStateEvent, error) {
	return nil, nil
}

func (m *MockStateStore) WatchNodes(_ context.Context) (<-chan outbound.NodeStateEvent, error) {
	ch := make(chan outbound.NodeStateEvent)
	close(ch)
	return ch, nil
}

func (m *MockStateStore) IsLeader() bool                                         { return true }
func (m *MockStateStore) WaitForLeader(_ context.Context, _ time.Duration) error { return nil }
func (m *MockStateStore) LeaderAddr() string                                     { return "localhost:2379" }

// --- Mock DNSRecordManager ---

type MockDNSManager struct {
	mu      sync.Mutex
	Records map[string]netip.Addr
}

func NewMockDNSManager() *MockDNSManager {
	return &MockDNSManager{Records: make(map[string]netip.Addr)}
}

func (m *MockDNSManager) UpsertRecord(_ context.Context, fqdn string, addr netip.Addr) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Records[fqdn] = addr
	return nil
}

func (m *MockDNSManager) DeleteRecord(_ context.Context, fqdn string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Records, fqdn)
	return nil
}

// --- Mock RouteDistributor ---

type MockRouteDistributor struct {
	mu     sync.RWMutex
	Routes map[string]model.TargetRoute
}

func NewMockRouteDistributor() *MockRouteDistributor {
	return &MockRouteDistributor{Routes: make(map[string]model.TargetRoute)}
}

func (m *MockRouteDistributor) AnnounceRoute(_ context.Context, route model.TargetRoute) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := route.Namespace + "/" + route.NodeID + "/" + route.TargetService + "/" + route.Name
	m.Routes[key] = route
	return nil
}

func (m *MockRouteDistributor) WithdrawRoute(_ context.Context, route model.TargetRoute) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := route.Namespace + "/" + route.NodeID + "/" + route.TargetService + "/" + route.Name
	delete(m.Routes, key)
	return nil
}

func (m *MockRouteDistributor) AllRoutes() []model.TargetRoute {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var all []model.TargetRoute
	for _, r := range m.Routes {
		all = append(all, r)
	}
	return all
}

// --- Mock IdentityManager ---

type MockIdentityManager struct {
	mu         sync.Mutex
	Identities map[string]model.ServiceIdentity
}

func NewMockIdentityManager() *MockIdentityManager {
	return &MockIdentityManager{Identities: make(map[string]model.ServiceIdentity)}
}

func (m *MockIdentityManager) RegisterWorkload(_ context.Context, identity model.ServiceIdentity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Identities[identity.ServiceID.String()] = identity
	return nil
}

func (m *MockIdentityManager) DeregisterWorkload(_ context.Context, id model.ServiceID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Identities, id.String())
	return nil
}

// --- Mock NodeTracker ---

type MockNodeTracker struct {
	mu        sync.Mutex
	Nodes     map[string]bool
	Callbacks []service.NodeLivenessCallback
}

func NewMockNodeTracker() *MockNodeTracker {
	return &MockNodeTracker{Nodes: make(map[string]bool)}
}

func (m *MockNodeTracker) TrackNode(nodeID string) (service.LeaseID, error) {
	m.mu.Lock()
	m.Nodes[nodeID] = true
	m.mu.Unlock()
	return 0, nil
}

func (m *MockNodeTracker) OnDisconnect(nodeID string) {
	m.mu.Lock()
	m.Nodes[nodeID] = false
	m.mu.Unlock()
}

func (m *MockNodeTracker) UntrackNode(nodeID string) {
	m.mu.Lock()
	delete(m.Nodes, nodeID)
	m.mu.Unlock()
}

func (m *MockNodeTracker) IsAlive(nodeID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Nodes[nodeID]
}

func (m *MockNodeTracker) LeaseID(_ string) service.LeaseID { return 0 }

func (m *MockNodeTracker) ConnectedNodes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []string
	for id, alive := range m.Nodes {
		if alive {
			result = append(result, id)
		}
	}
	return result
}

func (m *MockNodeTracker) RegisterCallback(callback service.NodeLivenessCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Callbacks = append(m.Callbacks, callback)
}

// --- Mock TrafficPolicyStore ---

type MockTrafficPolicyStore struct {
	mu       sync.Mutex
	Policies map[string]*model.TrafficPolicy
}

func NewMockTrafficPolicyStore() *MockTrafficPolicyStore {
	return &MockTrafficPolicyStore{Policies: make(map[string]*model.TrafficPolicy)}
}

func (m *MockTrafficPolicyStore) PutTrafficPolicy(_ context.Context, policy *model.TrafficPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := policy.Namespace + "/" + policy.Name
	m.Policies[key] = policy
	return nil
}

func (m *MockTrafficPolicyStore) GetTrafficPolicy(_ context.Context, namespace, name string) (*model.TrafficPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.Policies[namespace+"/"+name]
	if !ok {
		return nil, domain.ErrPolicyNotFound
	}
	return p, nil
}

func (m *MockTrafficPolicyStore) GetPolicyByService(_ context.Context, serviceID model.ServiceID) (*model.TrafficPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.Policies {
		if p.TargetService == serviceID {
			return p, nil
		}
	}
	return nil, domain.ErrPolicyNotFound
}

func (m *MockTrafficPolicyStore) ListTrafficPolicies(_ context.Context, namespace string) ([]*model.TrafficPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*model.TrafficPolicy
	for _, p := range m.Policies {
		if p.Namespace == namespace {
			result = append(result, p)
		}
	}
	return result, nil
}

func (m *MockTrafficPolicyStore) DeleteTrafficPolicy(_ context.Context, namespace, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Policies, namespace+"/"+name)
	return nil
}

func (m *MockTrafficPolicyStore) WatchTrafficPolicies(_ context.Context) (<-chan outbound.TrafficPolicyEvent, error) {
	ch := make(chan outbound.TrafficPolicyEvent)
	close(ch)
	return ch, nil
}

// --- Mock EndpointHealthStore ---

type MockEndpointHealthStore struct {
	mu     sync.Mutex
	Health map[string]*model.EndpointHealth
}

func NewMockEndpointHealthStore() *MockEndpointHealthStore {
	return &MockEndpointHealthStore{Health: make(map[string]*model.EndpointHealth)}
}

func (m *MockEndpointHealthStore) PutEndpointHealth(_ context.Context, health *model.EndpointHealth, _ int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s/%s", health.ServiceID, health.RouteName)
	m.Health[key] = health
	return nil
}

func (m *MockEndpointHealthStore) GetEndpointHealth(_ context.Context, serviceID model.ServiceID, addr string) (*model.EndpointHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s/%s", serviceID, addr)
	h, ok := m.Health[key]
	if !ok {
		return nil, fmt.Errorf("health not found")
	}
	return h, nil
}

func (m *MockEndpointHealthStore) ListEndpointHealth(_ context.Context, serviceID model.ServiceID) ([]*model.EndpointHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*model.EndpointHealth
	prefix := serviceID.String() + "/"
	for k, v := range m.Health {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			result = append(result, v)
		}
	}
	return result, nil
}

func (m *MockEndpointHealthStore) WatchEndpointHealth(_ context.Context) (<-chan outbound.EndpointHealthEvent, error) {
	ch := make(chan outbound.EndpointHealthEvent)
	close(ch)
	return ch, nil
}

// --- Mock DependencyStore ---

type MockDependencyStore struct {
	mu   sync.Mutex
	Deps map[string]*model.ServiceDependencies
}

func NewMockDependencyStore() *MockDependencyStore {
	return &MockDependencyStore{Deps: make(map[string]*model.ServiceDependencies)}
}

func (m *MockDependencyStore) PutDependencies(_ context.Context, deps *model.ServiceDependencies) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Deps[deps.ServiceID.String()] = deps
	return nil
}

func (m *MockDependencyStore) GetDependencies(_ context.Context, serviceID model.ServiceID) (*model.ServiceDependencies, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.Deps[serviceID.String()]
	if !ok {
		return nil, fmt.Errorf("dependencies not found")
	}
	return d, nil
}

func (m *MockDependencyStore) WatchDependencies(_ context.Context) (<-chan outbound.DependencyEvent, error) {
	ch := make(chan outbound.DependencyEvent)
	close(ch)
	return ch, nil
}

// --- Mock RouteManager ---

type MockRouteManager struct {
	mu     sync.Mutex
	Routes map[string]*model.TargetRoute
}

func NewMockRouteManager() *MockRouteManager {
	return &MockRouteManager{Routes: make(map[string]*model.TargetRoute)}
}

func routeManagerKey(namespace, nodeId, serviceName, name string) string {
	return fmt.Sprintf("%s/%s/%s/%s", namespace, nodeId, serviceName, name)
}

func (m *MockRouteManager) Apply(_ context.Context, route *model.TargetRoute) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := routeManagerKey(route.Namespace, route.NodeID, route.TargetService, route.Name)
	cp := *route
	m.Routes[key] = &cp
	return nil
}

func (m *MockRouteManager) Get(_ context.Context, namespace, name, serviceName, nodeId string) (*model.TargetRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := routeManagerKey(namespace, nodeId, serviceName, name)
	r, ok := m.Routes[key]
	if !ok {
		return nil, domain.ErrResourceNotFound
	}
	cp := *r
	return &cp, nil
}

func (m *MockRouteManager) List(_ context.Context, namespace string) ([]*model.TargetRoute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*model.TargetRoute
	for _, r := range m.Routes {
		if namespace == "" || r.Namespace == namespace {
			cp := *r
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (m *MockRouteManager) Delete(_ context.Context, namespace, name, serviceName, nodeId string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := routeManagerKey(namespace, nodeId, serviceName, name)
	if _, ok := m.Routes[key]; !ok {
		return domain.ErrResourceNotFound
	}
	delete(m.Routes, key)
	return nil
}

func (m *MockRouteManager) DeleteByNodeId(_ context.Context, nodeId string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, r := range m.Routes {
		if r.NodeID == nodeId {
			delete(m.Routes, key)
		}
	}
	return nil
}

func (m *MockRouteManager) ClearRoutes(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Routes = make(map[string]*model.TargetRoute)
	return nil
}

// --- Mock ScopeDistributor ---

type MockScopeDistributor struct {
	mu         sync.Mutex
	Broadcasts []model.ScopeUpdate
	Pushes     map[string][]model.ScopeUpdate
}

func NewMockScopeDistributor() *MockScopeDistributor {
	return &MockScopeDistributor{Pushes: make(map[string][]model.ScopeUpdate)}
}

func (m *MockScopeDistributor) BroadcastUpdate(_ context.Context, update model.ScopeUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Broadcasts = append(m.Broadcasts, update)
	return nil
}

func (m *MockScopeDistributor) PushUpdate(_ context.Context, nodeID string, update model.ScopeUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Pushes[nodeID] = append(m.Pushes[nodeID], update)
	return nil
}

// GetBroadcasts returns recorded broadcast updates.
func (m *MockScopeDistributor) GetBroadcasts() []model.ScopeUpdate {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Broadcasts
}

// GetPushes returns recorded push updates for a node.
func (m *MockScopeDistributor) GetPushes(nodeID string) []model.ScopeUpdate {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Pushes[nodeID]
}
