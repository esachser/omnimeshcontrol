package etcdstore

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

const (
	statePrefix      = "/omnimesh/state/"
	ipAllocPrefix    = statePrefix + "ip-alloc/"
	nodePrefix       = statePrefix + "nodes/"
	leaderElectionPf = "/omnimesh/leader/"
)

// EtcdStateStore implements the StateStore outbound port using an embedded etcd instance.
type EtcdStateStore struct {
	client   *clientv3.Client
	session  *concurrency.Session
	election *concurrency.Election
	isLeader bool
}

// NewEtcdStateStore creates a new EtcdStateStore.
func NewEtcdStateStore(client *clientv3.Client) (*EtcdStateStore, error) {
	session, err := concurrency.NewSession(client, concurrency.WithTTL(15))
	if err != nil {
		return nil, fmt.Errorf("state store: failed to create session: %w", err)
	}

	election := concurrency.NewElection(session, leaderElectionPf)

	return &EtcdStateStore{
		client:   client,
		session:  session,
		election: election,
	}, nil
}

// Campaign starts a leader election campaign. Blocks until elected or context is cancelled.
func (s *EtcdStateStore) Campaign(ctx context.Context, nodeID string) error {
	if err := s.election.Campaign(ctx, nodeID); err != nil {
		return fmt.Errorf("state store: campaign: %w", err)
	}
	s.isLeader = true
	return nil
}

// Resign resigns leadership.
func (s *EtcdStateStore) Resign(ctx context.Context) error {
	s.isLeader = false
	return s.election.Resign(ctx)
}

// AllocateIP atomically allocates a mesh IP for a service.
func (s *EtcdStateStore) AllocateIP(ctx context.Context, id model.ServiceID, cidr netip.Prefix) (netip.Addr, error) {
	allocKey := ipAllocPrefix + id.String()

	// Check if already allocated.
	resp, err := s.client.Get(ctx, allocKey)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("state store: get allocation %q: %w", id, err)
	}
	if len(resp.Kvs) > 0 {
		return netip.Addr{}, fmt.Errorf("state store: allocate IP for %q: %w", id, domain.ErrIPConflict)
	}

	// Find the next free IP by scanning existing allocations.
	allocResp, err := s.client.Get(ctx, ipAllocPrefix, clientv3.WithPrefix())
	if err != nil {
		return netip.Addr{}, fmt.Errorf("state store: list allocations: %w", err)
	}

	used := make(map[netip.Addr]bool, len(allocResp.Kvs))
	for _, kv := range allocResp.Kvs {
		addr, ok := netip.AddrFromSlice(kv.Value)
		if ok {
			used[addr] = true
		}
	}

	// Find next free IP in CIDR (skip network address).
	addr := cidr.Addr().Next() // skip the network address
	for ; cidr.Contains(addr); addr = addr.Next() {
		if !used[addr] {
			break
		}
	}
	if !cidr.Contains(addr) {
		return netip.Addr{}, fmt.Errorf("state store: allocate IP for %q: %w", id, domain.ErrIPExhausted)
	}

	// Atomically write the allocation (only if key does not exist).
	addrBytes := addr.As4()
	txnResp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.CreateRevision(allocKey), "=", 0)).
		Then(clientv3.OpPut(allocKey, string(addrBytes[:]))).
		Commit()
	if err != nil {
		return netip.Addr{}, fmt.Errorf("state store: txn allocate IP: %w", err)
	}
	if !txnResp.Succeeded {
		return netip.Addr{}, fmt.Errorf("state store: allocate IP for %q: %w", id, domain.ErrIPConflict)
	}

	return addr, nil
}

// ReleaseIP releases the mesh IP allocated to a service.
func (s *EtcdStateStore) ReleaseIP(ctx context.Context, id model.ServiceID) error {
	allocKey := ipAllocPrefix + id.String()
	resp, err := s.client.Delete(ctx, allocKey)
	if err != nil {
		return fmt.Errorf("state store: release IP for %q: %w", id, err)
	}
	if resp.Deleted == 0 {
		return fmt.Errorf("state store: release IP for %q: %w", id, domain.ErrServiceNotFound)
	}
	return nil
}

// GetIPAllocation returns the mesh IP allocated to a service.
func (s *EtcdStateStore) GetIPAllocation(ctx context.Context, id model.ServiceID) (netip.Addr, error) {
	allocKey := ipAllocPrefix + id.String()
	resp, err := s.client.Get(ctx, allocKey)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("state store: get IP for %q: %w", id, err)
	}
	if len(resp.Kvs) == 0 {
		return netip.Addr{}, fmt.Errorf("state store: get IP for %q: %w", id, domain.ErrServiceNotFound)
	}

	val := resp.Kvs[0].Value
	if len(val) == 4 {
		return netip.AddrFrom4([4]byte(val)), nil
	}
	// Fallback: try 16-byte IPv6.
	if len(val) == 16 {
		return netip.AddrFrom16([16]byte(val)), nil
	}
	return netip.Addr{}, fmt.Errorf("state store: invalid IP data for %q (len=%d)", id, len(val))
}

// PersistNode persists a node's state to etcd.
func (s *EtcdStateStore) PersistNode(ctx context.Context, nodeID string, node *model.Node) error {
	data, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("state store: marshal node %q: %w", nodeID, err)
	}
	key := nodePrefix + nodeID
	if _, err := s.client.Put(ctx, key, string(data)); err != nil {
		return fmt.Errorf("state store: persist node %q: %w", nodeID, err)
	}
	return nil
}

// MarkNodeLost updates a node's persistent state to LOST.
func (s *EtcdStateStore) MarkNodeLost(ctx context.Context, nodeID string) error {
	key := nodePrefix + nodeID
	resp, err := s.client.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("state store: get node %q: %w", nodeID, err)
	}
	if len(resp.Kvs) == 0 {
		return fmt.Errorf("state store: mark lost %q: %w", nodeID, domain.ErrNodeNotFound)
	}

	var node model.Node
	if err := json.Unmarshal(resp.Kvs[0].Value, &node); err != nil {
		return fmt.Errorf("state store: unmarshal node %q: %w", nodeID, err)
	}

	node.Status = model.NodeStatusLost
	return s.PersistNode(ctx, nodeID, &node)
}

// DeregisterNode removes a node from persistent storage.
func (s *EtcdStateStore) DeregisterNode(ctx context.Context, nodeID string) error {
	key := nodePrefix + nodeID
	resp, err := s.client.Delete(ctx, key)
	if err != nil {
		return fmt.Errorf("state store: deregister node %q: %w", nodeID, err)
	}
	if resp.Deleted == 0 {
		return fmt.Errorf("state store: deregister node %q: %w", nodeID, domain.ErrNodeNotFound)
	}
	return nil
}

// IsLeader reports whether this instance is the current leader.
func (s *EtcdStateStore) IsLeader() bool {
	return s.isLeader
}

// WaitForLeader blocks until a leader is elected or the timeout expires.
func (s *EtcdStateStore) WaitForLeader(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Observe the election to find the leader.
	observeCh := s.election.Observe(ctx)
	select {
	case resp, ok := <-observeCh:
		if !ok {
			return fmt.Errorf("state store: leader observation channel closed")
		}
		_ = resp // leader was elected
		return nil
	case <-ctx.Done():
		return fmt.Errorf("state store: wait for leader: %w", ctx.Err())
	}
}

// LeaderAddr returns the address of the current leader.
func (s *EtcdStateStore) LeaderAddr() string {
	resp, err := s.election.Leader(context.Background())
	if err != nil {
		return ""
	}
	if len(resp.Kvs) == 0 {
		return ""
	}
	return string(resp.Kvs[0].Value)
}

// Close closes the session.
func (s *EtcdStateStore) Close() error {
	return s.session.Close()
}

// Suppress unused import warning for binary package (used for potential future IP encoding).
var _ = binary.BigEndian

// Compile-time interface check.
var _ outbound.StateStore = (*EtcdStateStore)(nil)
