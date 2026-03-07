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
	ipNextKey        = statePrefix + "ip-next" // 4 bytes: next IP to allocate
	ipFreeKey        = statePrefix + "ip-free" // JSON array of freed IPs (for reuse)
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
//
// Strategy (O(1) with distributed safety):
//  1. Check if this service already has an allocation → ErrIPConflict.
//  2. Try to pop an IP from the free list (reuse freed IPs).
//  3. If free list is empty, increment the counter at /omnimesh/state/ip-next via CAS.
//  4. Write the service→IP mapping atomically (only if key doesn't exist yet).
//
// All writes use etcd Txn for distributed lock-free safety.
func (s *EtcdStateStore) AllocateIP(ctx context.Context, id model.ServiceID, cidr netip.Prefix) (netip.Addr, error) {
	allocKey := ipAllocPrefix + id.String()

	// Step 1: Check if already allocated.
	resp, err := s.client.Get(ctx, allocKey)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("state store: get allocation %q: %w", id, err)
	}
	if len(resp.Kvs) > 0 {
		return netip.Addr{}, fmt.Errorf("state store: allocate IP for %q: %w", id, domain.ErrIPConflict)
	}

	// Step 2: Try to pop from free list first (reuse deleted IPs).
	addr, fromFree, err := s.popFreeIP(ctx)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("state store: pop free IP: %w", err)
	}

	// Step 3: If no free IP available, allocate from counter.
	if !fromFree {
		addr, err = s.allocateFromCounter(ctx, cidr)
		if err != nil {
			return netip.Addr{}, err
		}
	}

	// Step 4: Atomically write the service→IP mapping (only if key doesn't exist).
	addrBytes := addr.As4()
	txnResp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.CreateRevision(allocKey), "=", 0)).
		Then(clientv3.OpPut(allocKey, string(addrBytes[:]))).
		Commit()
	if err != nil {
		return netip.Addr{}, fmt.Errorf("state store: txn allocate IP: %w", err)
	}
	if !txnResp.Succeeded {
		// Another instance allocated for this service concurrently — return the freed IP.
		s.pushFreeIP(ctx, addr)
		return netip.Addr{}, fmt.Errorf("state store: allocate IP for %q: %w", id, domain.ErrIPConflict)
	}

	return addr, nil
}

// allocateFromCounter atomically increments the IP counter via CAS and returns the next IP.
func (s *EtcdStateStore) allocateFromCounter(ctx context.Context, cidr netip.Prefix) (netip.Addr, error) {
	for {
		resp, err := s.client.Get(ctx, ipNextKey)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("state store: get ip-next: %w", err)
		}

		var nextAddr netip.Addr
		var currentRev int64

		if len(resp.Kvs) == 0 {
			// First allocation ever: start from network address + 1.
			nextAddr = cidr.Addr().Next()
			currentRev = 0
		} else {
			val := resp.Kvs[0].Value
			if len(val) != 4 {
				return netip.Addr{}, fmt.Errorf("state store: invalid ip-next data (len=%d)", len(val))
			}
			nextAddr = netip.AddrFrom4([4]byte(val))
			currentRev = resp.Kvs[0].ModRevision
		}

		if !cidr.Contains(nextAddr) {
			return netip.Addr{}, fmt.Errorf("state store: %w", domain.ErrIPExhausted)
		}

		// Write the incremented counter via CAS.
		successor := nextAddr.Next()
		succBytes := successor.As4()

		var cmp clientv3.Cmp
		if currentRev == 0 {
			cmp = clientv3.Compare(clientv3.CreateRevision(ipNextKey), "=", 0)
		} else {
			cmp = clientv3.Compare(clientv3.ModRevision(ipNextKey), "=", currentRev)
		}

		txnResp, err := s.client.Txn(ctx).
			If(cmp).
			Then(clientv3.OpPut(ipNextKey, string(succBytes[:]))).
			Commit()
		if err != nil {
			return netip.Addr{}, fmt.Errorf("state store: txn ip-next: %w", err)
		}
		if txnResp.Succeeded {
			return nextAddr, nil
		}
		// CAS failed — another instance incremented. Retry.
	}
}

// Binary free list format:
// The ip-free key stores a flat byte slice of packed IP entries.
// Each entry: [tag][addr_bytes]
//   - tag 0x04: IPv4, followed by 4 bytes  (entry size = 5)
//   - tag 0x06: IPv6, followed by 16 bytes (entry size = 17)

const (
	ipTagV4   byte = 0x04
	ipTagV6   byte = 0x06
	ipEntryV4      = 1 + 4  // tag + 4 bytes
	ipEntryV6      = 1 + 16 // tag + 16 bytes
)

// marshalIP encodes an IP address to its binary representation with tag byte.
func marshalIP(addr netip.Addr) []byte {
	if addr.Is4() {
		b := addr.As4()
		return append([]byte{ipTagV4}, b[:]...)
	}
	b := addr.As16()
	return append([]byte{ipTagV6}, b[:]...)
}

// popFirstIP reads the first IP entry from a packed byte slice.
// Returns the IP, the remaining bytes after the entry, and success.
func popFirstIP(data []byte) (netip.Addr, []byte, bool) {
	if len(data) == 0 {
		return netip.Addr{}, data, false
	}

	tag := data[0]
	switch tag {
	case ipTagV4:
		if len(data) < ipEntryV4 {
			return netip.Addr{}, data, false
		}
		return netip.AddrFrom4([4]byte(data[1:ipEntryV4])), data[ipEntryV4:], true
	case ipTagV6:
		if len(data) < ipEntryV6 {
			return netip.Addr{}, data, false
		}
		return netip.AddrFrom16([16]byte(data[1:ipEntryV6])), data[ipEntryV6:], true
	default:
		return netip.Addr{}, data, false
	}
}

// popFreeIP atomically pops the first IP from the binary free list via CAS.
// Returns (addr, true, nil) if an IP was popped, or (Addr{}, false, nil) if the list is empty.
func (s *EtcdStateStore) popFreeIP(ctx context.Context) (netip.Addr, bool, error) {
	resp, err := s.client.Get(ctx, ipFreeKey)
	if err != nil {
		return netip.Addr{}, false, err
	}
	if len(resp.Kvs) == 0 || len(resp.Kvs[0].Value) == 0 {
		return netip.Addr{}, false, nil
	}

	addr, remaining, ok := popFirstIP(resp.Kvs[0].Value)
	if !ok {
		return netip.Addr{}, false, nil
	}

	// CAS: only update if revision hasn't changed.
	txnResp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.ModRevision(ipFreeKey), "=", resp.Kvs[0].ModRevision)).
		Then(clientv3.OpPut(ipFreeKey, string(remaining))).
		Commit()
	if err != nil {
		return netip.Addr{}, false, err
	}
	if !txnResp.Succeeded {
		return netip.Addr{}, false, nil
	}

	return addr, true, nil
}

// pushFreeIP atomically appends an IP to the binary free list via CAS.
func (s *EtcdStateStore) pushFreeIP(ctx context.Context, addr netip.Addr) {
	entry := marshalIP(addr)

	for {
		resp, err := s.client.Get(ctx, ipFreeKey)
		if err != nil {
			return
		}

		var existing []byte
		var rev int64
		if len(resp.Kvs) > 0 {
			existing = resp.Kvs[0].Value
			rev = resp.Kvs[0].ModRevision
		}

		newData := make([]byte, len(existing)+len(entry))
		copy(newData, existing)
		copy(newData[len(existing):], entry)

		var cmp clientv3.Cmp
		if rev == 0 {
			cmp = clientv3.Compare(clientv3.CreateRevision(ipFreeKey), "=", 0)
		} else {
			cmp = clientv3.Compare(clientv3.ModRevision(ipFreeKey), "=", rev)
		}

		txnResp, err := s.client.Txn(ctx).
			If(cmp).
			Then(clientv3.OpPut(ipFreeKey, string(newData))).
			Commit()
		if err != nil {
			return
		}
		if txnResp.Succeeded {
			return
		}
		// CAS failed — retry.
	}
}

// ReleaseIP releases the mesh IP allocated to a service, pushing it onto the free list for reuse.
func (s *EtcdStateStore) ReleaseIP(ctx context.Context, id model.ServiceID) error {
	allocKey := ipAllocPrefix + id.String()

	// Read the allocated IP first so we can push it to the free list.
	resp, err := s.client.Get(ctx, allocKey)
	if err != nil {
		return fmt.Errorf("state store: get IP for release %q: %w", id, err)
	}
	if len(resp.Kvs) == 0 {
		return fmt.Errorf("state store: release IP for %q: %w", id, domain.ErrServiceNotFound)
	}

	// Parse the IP from the raw bytes.
	val := resp.Kvs[0].Value
	var addr netip.Addr
	switch len(val) {
	case 4:
		addr = netip.AddrFrom4([4]byte(val))
	case 16:
		addr = netip.AddrFrom16([16]byte(val))
	}

	// Delete the allocation.
	delResp, err := s.client.Delete(ctx, allocKey)
	if err != nil {
		return fmt.Errorf("state store: release IP for %q: %w", id, err)
	}
	if delResp.Deleted == 0 {
		return fmt.Errorf("state store: release IP for %q: %w", id, domain.ErrServiceNotFound)
	}

	// Push the freed IP onto the free list for reuse.
	if addr.IsValid() {
		s.pushFreeIP(ctx, addr)
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
