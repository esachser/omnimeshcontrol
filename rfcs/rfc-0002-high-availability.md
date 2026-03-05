# RFC-0002: High Availability & State Storage — Embedded etcd

Status: **Draft**

## Problem Statement

RFC-0001 acknowledges a single control plane as a limitation. A single instance is a single point of failure: if it crashes or loses connectivity, the mesh loses the ability to register services, allocate IPs, issue identities, update DNS, and distribute routes.

The control plane also needs a **persistent, replicated state store** for services, IP allocations, node registrations, and configuration data — not just consensus.

**This RFC addresses:**

1. **State Storage** — persistent, queryable storage for all control plane data.
2. **State Replication** — keeping stored state consistent across 3–5 control plane replicas.
3. **Leader Election** — ensuring exactly one instance is the active writer at any time.
4. **Failover** — automatically promoting a follower when the leader becomes unavailable.

---

## Decision: Embedded etcd

After evaluating three options — embedded etcd, HashiCorp Raft (with custom FSM), and embedded NATS JetStream — **embedded etcd** was chosen as the HA and storage backbone for the Omnimesh control plane.

### Why etcd

#### 1. Consensus + Storage + Watch in One Package

etcd provides a complete distributed KV store with linearizable consistency. Unlike HashiCorp Raft (which only provides consensus, requiring weeks of custom FSM, serialization, watch/event, and transaction code), etcd gives us everything the control plane needs immediately:

| Capability | etcd | HashiCorp Raft | NATS JetStream |
| --- | --- | --- | --- |
| KV Storage | ✅ Built-in | ❌ Build FSM + backing store | ✅ Built-in |
| Transactions (atomic IP alloc) | ✅ `Txn` API | ❌ Custom FSM logic | ❌ Single-key only |
| Watch / Subscribe | ✅ `Watch` API | ❌ Custom event bus | ✅ KV Watch |
| Leases / TTL | ✅ `Lease` API | ❌ Custom heartbeat system | ❌ Custom |
| Prefix Queries | ✅ `GetPrefix` | ❌ Custom indexing | ✅ Per-bucket |
| Consistency | Linearizable | Linearizable | Eventually consistent |

Estimated time to HA MVP: **2–3 weeks** with etcd vs. **6–10 weeks** with HashiCorp Raft.

#### 2. Native CoreDNS Integration

This is a decisive advantage. CoreDNS has a [built-in etcd plugin](https://coredns.io/plugins/etcd/) that reads DNS records directly from etcd keys. By embedding etcd in the control plane, **CoreDNS can read mesh DNS records directly from the same etcd cluster** — no custom plugin, no gRPC bridge, no intermediary.

```
┌──────────────┐     reads records from etcd directly    ┌──────────────┐
│   CoreDNS    │ ◄────────────────────────────────────── │  Embedded    │
│   Instance   │     (etcd plugin, native)               │  etcd        │
│              │                                          │  (inside     │
│   etcd plugin│                                          │  control     │
│   configured │                                          │  plane)      │
└──────────────┘                                          └──────────────┘
```

When using CoreDNS, the control plane simply writes DNS records to etcd in the expected format, and CoreDNS picks them up natively — no custom plugin, no intermediary push. This is simpler, faster, and already production-proven (CoreDNS + etcd is how Kubernetes DNS works in many deployments).

The RFC-0001 `DNSRecordManager` outbound port is maintained as-is — the etcd/CoreDNS integration is one adapter implementation of that port. Other DNS solutions (e.g., cloud DNS APIs, PowerDNS, direct zone file management) can be supported by plugging in a different adapter. The etcd adapter writes records in CoreDNS's expected key format:

```
/skydns/local/omnimesh/{service-name} → {"host": "100.64.0.5"}
```

#### 3. Transactions Guarantee Safe IP Allocation

IP allocation is the most correctness-critical operation in the control plane. Duplicate IP assignments cause catastrophic routing conflicts. etcd's `Txn` API provides atomic compare-and-swap:

```go
// Atomic: if IP not allocated → assign it AND register the service
txn := client.Txn(ctx).
    If(clientv3.Compare(clientv3.Version(ipKey), "=", 0)).  // IP not taken
    Then(
        clientv3.OpPut(ipKey, serviceID),                    // claim the IP
        clientv3.OpPut(serviceKey, serviceJSON),             // register the service
    )
resp, err := txn.Commit()
```

Neither NATS JetStream (no multi-key transactions) nor a custom Raft FSM (must be hand-coded and thoroughly tested) provide this safety as directly.

#### 4. Leases for Node Liveness

etcd leases provide a natural mechanism for node heartbeat and automatic disconnect detection. **Importantly, agents never interact with etcd directly** — the control plane's application layer manages etcd leases internally, triggered by agent gRPC calls:

```
Agent (gRPC)              Application Service          EtcdStateStore
    │                            │                            │
    │  RegisterNode(req)         │                            │
    │───────────────────────────►│                            │
    │                            │ domain validation          │
    │                            │ RegisterNode(node, 30s)    │
    │                            │───────────────────────────►│
    │                            │   Grant lease (30s)        │
    │                            │   Put node key with lease  │
    │                            │ leaseID                    │
    │                            │◄───────────────────────────│
    │  ok (internalized leaseID) │                            │
    │◄───────────────────────────│                            │
    │                            │                            │
    │  Heartbeat() (periodic)    │                            │
    │───────────────────────────►│                            │
    │                            │ RenewNode(leaseID)         │
    │                            │───────────────────────────►│
    │                            │   KeepAlive on lease       │
    │                            │◄───────────────────────────│
    │  ok                        │                            │
    │◄───────────────────────────│                            │
    │                            │                            │
    ╳  Agent disconnects         │                            │
                                 │         (30s passes)       │
                                 │   Lease expires            │
                                 │   → node key deleted       │
                                 │   → Watch fires            │
                                 │◄───────────────────────────│
                                 │ mark node's services       │
                                 │ as unhealthy               │
```

The agent only knows about its gRPC contract (`RegisterNode`, `Heartbeat`). The fact that leases exist in etcd is an implementation detail hidden behind the `StateStore` outbound port.

#### 5. Battle-Tested

etcd powers every Kubernetes cluster. It has been through more production hours, failure modes, and edge cases than any alternative. For infrastructure-critical software like a service mesh control plane, this track record matters.

### Trade-offs Accepted

| Concern | Mitigation |
| --- | --- |
| **Binary size (~30–50 MB)** | Acceptable for a control plane binary. This is not an edge/embedded device. |
| **Memory (~200–500 MB)** | The control plane runs on dedicated infrastructure nodes, not on every mesh agent. 3–5 instances with 500 MB each is reasonable. |
| **Embedding complexity** | Well-documented `embed` package. K3s (lightweight Kubernetes) embeds etcd successfully as a reference implementation. |
| **gRPC port conflicts** | Solved by binding the embedded etcd to a separate port (e.g., `2379` for etcd client, `2380` for peer) distinct from the control plane's gRPC server port. |
| **Cross-platform** | etcd works on Windows and macOS. Omnimesh CI will test on all three platforms. The control plane is the only component that embeds etcd; agents do not. |

---

## Architecture

### 1. Cluster Overview

```
                    Agents / CLI
                         │
                         │ gRPC (only external interface)
                         │
┌────────────────────────┼──── Omnimesh HA Cluster (3 nodes) ────────────┐
│                        │                                               │
│                        ▼                                               │
│  ┌─────────────────────────────┐         ┌─────────────────────────┐   │
│  │  Control Plane (Leader)     │         │ Control Plane (Follower)│   │
│  │                             │         │                         │   │
│  │  ┌───────────────────────┐  │         │  ┌───────────────────┐  │   │
│  │  │  gRPC Server          │  │         │  │  gRPC Server      │  │   │
│  │  │  :8443 (inbound)      │  │         │  │  :8443 (inbound)  │  │   │
│  │  └───────────┬───────────┘  │         │  └────────┬──────────┘  │   │
│  │              │              │         │           │             │   │
│  │  ┌───────────┴───────────┐  │         │  ┌────────┴──────────┐  │   │
│  │  │  Application          │  │         │  │  Application      │  │   │
│  │  │  Services             │  │         │  │  Services         │  │   │
│  │  │  (domain logic)       │  │         │  │  (domain logic)   │  │   │
│  │  └───────────┬───────────┘  │         │  └────────┬──────────┘  │   │
│  │              │              │         │           │             │   │
│  │  ┌───────────┴───────────┐  │  Raft   │  ┌────────┴──────────┐  │   │
│  │  │  Embedded etcd        │  │  RPCs   │  │  Embedded etcd    │  │   │
│  │  │  (outbound,           │──┼─────────┼──│  (outbound,       │  │   │
│  │  │   internal only)      │  │  Log    │  │   internal only)  │  │   │
│  │  │  :2379 (local)        │  │  Repl.  │  │  :2379 (local)    │  │   │
│  │  │  :2380 (peer)         │  │         │  │  :2380 (peer)     │  │   │
│  │  └───────────────────────┘  │         │  └───────────────────┘  │   │
│  └─────────────────────────────┘         └─────────────────────────┘   │
│                                                                        │
│                    ┌─────────────────────────┐                          │
│                    │ Control Plane (Follower)│                         │
│                    │  (same structure)       │                          │
│                    └─────────────────────────┘                          │
│                                                                        │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  CoreDNS Instance(s)                                            │   │
│  │  ┌─────────┐                                                    │   │
│  │  │  etcd   │──── reads /skydns/ keys directly from etcd ───────►│   │
│  │  │  plugin │     (only infra-level component allowed to)        │   │
│  │  └─────────┘                                                    │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                        │
│  NOTE: etcd is NEVER exposed to agents or CLI.                         │
│  All external access flows through the gRPC API → domain logic → etcd. │
└────────────────────────────────────────────────────────────────────────┘
```

### 2. etcd Key Space Design

The key space is split into two top-level areas following the declarative resource model from RFC-0001: **resources** (user-defined, declarative) and **state** (system-derived, computed). CoreDNS records use the `/skydns/` prefix (CoreDNS etcd plugin convention).

```
/omnimesh/
│
├── resources/                                         ← User-defined declarative resources
│   │
│   ├── core.omnimesh.io/v1/
│   │   ├── namespaces/
│   │   │   ├── production                             → Namespace resource JSON
│   │   │   ├── staging                                → Namespace resource JSON
│   │   │   └── monitoring                             → Namespace resource JSON
│   │   │
│   │   ├── services/
│   │   │   ├── production/
│   │   │   │   ├── order-service                      → Service resource JSON
│   │   │   │   └── payment-service                    → Service resource JSON
│   │   │   └── staging/
│   │   │       └── order-service                      → Service resource JSON
│   │   │
│   │   └── nodes/
│   │       ├── node-1                                 → Node resource JSON
│   │       └── node-2                                 → Node resource JSON
│   │
│   ├── traffic.omnimesh.io/v1/
│   │   └── trafficpolicies/
│   │       └── production/
│   │           └── order-service-policy               → TrafficPolicy resource JSON
│   │
│   └── security.omnimesh.io/v1/
│       └── authorizationpolicies/
│           └── production/
│               └── order-to-payment                   → AuthorizationPolicy resource JSON
│
├── state/                                             ← System-derived internal state
│   ├── allocations/
│   │   ├── by-service/{namespace}/{name}              → IP address string
│   │   └── by-ip/{ip}                                 → ServiceID JSON (reverse index)
│   │
│   ├── health/{namespace}/{service}/{endpoint}        → EndpointHealth JSON
│   │
│   └── dependencies/{namespace}/{service}             → Computed ServiceDependencies JSON
│
/skydns/                                               ← CoreDNS integration
└── local/omnimesh/{service-name}                      → {"host": "100.64.x.x"}
                                                          (CoreDNS etcd plugin format)
```

| Query | Key Prefix | Scan |
| --- | --- | --- |
| List all namespaces | `/omnimesh/resources/core.omnimesh.io/v1/namespaces/` | ✅ Single prefix |
| List all services | `/omnimesh/resources/core.omnimesh.io/v1/services/` | ✅ Single prefix |
| List services in `production` | `.../services/production/` | ✅ Single prefix |
| List all traffic policies | `/omnimesh/resources/traffic.omnimesh.io/v1/trafficpolicies/` | ✅ Single prefix |
| List all resources | `/omnimesh/resources/` | ✅ Single prefix |

Operators write to `resources/`; the system computes `state/`. This separation ensures what the operator controls is clearly distinct from what the system derives.

### 3. Key Operations Mapping

All operations are initiated by agents/CLI through the **gRPC inbound API** (including `Apply`), processed by **application services** (domain logic), and persisted via the **`ResourceStore`** and **`StateStore`** outbound ports to etcd.

| Agent/CLI Action (gRPC) | Application Layer | etcd Implementation (internal) |
| --- | --- | --- |
| `Apply(Service)` | Validates → allocates IP → registers identity | `Txn`: check IP not taken → put resource + allocation + DNS (atomic) |
| `Delete(Service)` | Validates → releases IP → deregisters identity | `Txn`: delete resource + allocation + DNS (atomic) |
| `List(Service)` | Returns from store | `Get` with prefix `.../services/{ns}/` |
| `Apply(Node)` | Validates platform/capabilities → stores node | `Put` node resource with 30s `Lease` (internally managed) |
| `Heartbeat(req)` | Renews node liveness | `KeepAlive` on lease (agent never sees the lease) |
| *(agent disconnects)* | Watch detects → marks services unhealthy | Lease expires → key deleted → Watch fires |
| *(service applied)* | DNS adapter writes record | Write `/skydns/` key → CoreDNS reads directly |
| *(route changes)* | Route distributor pushes to agents | Watch on `.../services/` → push via adapters |

### 4. Hexagonal Architecture Mapping

> **Architectural invariant**: etcd is strictly an outbound (infrastructure) concern. Agents and CLI connect exclusively through the gRPC inbound port. All data flows through domain logic before reaching etcd. This ensures:
> - **Input validation** — all writes are validated by domain rules before persistence.
> - **Domain events** — side effects (identity registration, route distribution) are triggered by application logic, not by raw etcd watches.
> - **Testability** — application logic can be tested with a mock `StateStore`, without etcd.
> - **Substitutability** — etcd can be replaced by a different backend without changing agents, CLI, or domain logic.

#### Outbound Ports (`internal/port/outbound/`)

```go
// ResourceStore is the generic storage interface for all declarative resources.
// Maps to /omnimesh/resources/ keys in etcd.
type ResourceStore interface {
    Put(ctx context.Context, key string, resource *domain.Resource) (revision int64, err error)
    Get(ctx context.Context, key string) (*domain.Resource, error)
    List(ctx context.Context, prefix string) ([]*domain.Resource, error)
    Delete(ctx context.Context, key string) error
    Watch(ctx context.Context, prefix string) (<-chan ResourceEvent, error)

    // CompareAndPut for optimistic concurrency
    CompareAndPut(ctx context.Context, key string, expectedRev int64, resource *domain.Resource) (int64, error)
}

// StateStore handles system-derived state and etcd-specific operations.
// Maps to /omnimesh/state/ keys in etcd.
type StateStore interface {
    // IP allocation (atomic: check + assign via etcd Txn)
    AllocateIP(ctx context.Context, id domain.ServiceID, cidr netip.Prefix) (netip.Addr, error)
    ReleaseIP(ctx context.Context, id domain.ServiceID) error
    GetIPAllocation(ctx context.Context, id domain.ServiceID) (netip.Addr, error)

    // Node lease management
    GrantNodeLease(ctx context.Context, nodeID string, ttl time.Duration) (LeaseID, error)
    RenewNodeLease(ctx context.Context, leaseID LeaseID) error

    // Leadership
    IsLeader() bool
    WaitForLeader(ctx context.Context, timeout time.Duration) error
    LeaderAddr() string
}
```

#### DNS Port — `DNSRecordManager` (Maintained from RFC-0001)

The `DNSRecordManager` outbound port defined in RFC-0001 is preserved. The etcd/CoreDNS integration is the **default adapter**, but the port allows alternative DNS backends to be plugged in.

```go
// Port (internal/port/outbound/) — unchanged from RFC-0001
type DNSRecordManager interface {
    UpsertRecord(ctx context.Context, fqdn string, addr netip.Addr) error
    DeleteRecord(ctx context.Context, fqdn string) error
}
```

The default adapter writes `/skydns/` keys so CoreDNS reads them directly:

```go
// Default adapter (internal/adapter/outbound/etcddns/)
type EtcdDNSRecordManager struct {
    client *clientv3.Client
    zone   string  // e.g., "omnimesh.local"
}

func (m *EtcdDNSRecordManager) UpsertRecord(ctx context.Context, fqdn string, addr netip.Addr) error {
    // Convert "myservice.omnimesh.local" to etcd key "/skydns/local/omnimesh/myservice"
    key := fqdnToSkyDNSKey(fqdn)
    val := fmt.Sprintf(`{"host":"%s"}`, addr)
    _, err := m.client.Put(ctx, key, val)
    return err
}
```

Alternative adapters (e.g., cloud DNS APIs, PowerDNS, RFC 2136 dynamic updates) implement the same `DNSRecordManager` interface without changes to the application or domain layers.

#### Adapter (`internal/adapter/outbound/etcdstore/`)

| File | Purpose |
| --- | --- |
| `embed.go` | Embedded etcd server setup, bootstrap, peer configuration |
| `resource_store.go` | `EtcdResourceStore` implementing the generic `ResourceStore` outbound port |
| `state_store.go` | `EtcdStateStore` implementing the `StateStore` outbound port (IP allocations, leases, leadership) |
| `txn.go` | Transaction helpers for atomic IP allocation and service registration |
| `watch.go` | Watch wrappers that convert etcd watch events to domain events |
| `lease.go` | Lease management for node liveness |

#### Domain Layer — No Changes

The domain layer remains pure. It does not know about etcd, Raft, or replication.

#### Application Layer — Minor Changes

Application services receive a `StateStore` port via constructor injection. The application service checks `IsLeader()` before accepting writes, returning an `ErrNotLeader` error that the gRPC adapter translates to a redirect.

### 5. Request Flow: Register Service (Write Path)

```
Agent (gRPC)          gRPC Adapter          Application Service       EtcdStateStore
    │                      │                        │                        │
    │ RegisterService(req) │                        │                        │
    │─────────────────────►│                        │                        │
    │                      │ RegisterService(cmd)   │                        │
    │                      │───────────────────────►│                        │
    │                      │                        │ IsLeader()?            │
    │                      │                        │───────────────────────►│
    │                      │                        │ true                   │
    │                      │                        │◄───────────────────────│
    │                      │                        │ AllocateIP(id, cidr)   │
    │                      │                        │───────────────────────►│
    │                      │                        │   etcd Txn:            │
    │                      │                        │   IF ip_key not exist  │
    │                      │                        │   THEN put ip + svc   │
    │                      │                        │        + skydns key    │
    │                      │                        │   (replicated to       │
    │                      │                        │    quorum, committed)  │
    │                      │                        │ ip, ok                 │
    │                      │                        │◄───────────────────────│
    │                      │ response               │                        │
    │                      │◄───────────────────────│                        │
    │ response             │                        │                        │
    │◄─────────────────────│                        │                        │
```

### 6. Watch-Driven Side Effects

State changes automatically trigger downstream actions via etcd watches:

```
  etcd Watch on /omnimesh/services/*
       │
       ├──► IdentityManager adapter → SPIRE workload registration
       │
  etcd Watch on /skydns/*
       │
       └──► (no-op — CoreDNS reads directly from etcd)

  etcd Watch on /omnimesh/routes/*
       │
       └──► RouteDistributor adapter → push to agents (BGP / gRPC / NATS)

  etcd Watch on /omnimesh/nodes/* (lease expiry events)
       │
       └──► NodeManager → mark services unhealthy, withdraw routes
```

### 7. Follower Write Forwarding

When a follower receives a write request:

1. `StateStore.IsLeader()` returns `false`.
2. The application service returns `ErrNotLeader` containing `LeaderAddr()`.
3. The gRPC adapter returns a gRPC `UNAVAILABLE` status with the leader address in trailing metadata.
4. The client retries against the leader.

Alternatively, the follower can transparently forward the request to the leader's gRPC endpoint.

### 8. Cluster Bootstrap & Lifecycle

| Scenario | Behavior |
| --- | --- |
| **First node (bootstrap)** | Starts embedded etcd with `InitialCluster` pointing to itself. Becomes leader immediately. |
| **Adding a second/third node** | New node starts with `InitialCluster` listing all peers. Joins the existing cluster via etcd's peer protocol. |
| **Restart after crash** | etcd restores from WAL + snapshots on disk. Rejoins the cluster automatically. |
| **Rolling upgrade** | Graceful leadership transfer → upgrade one node at a time → cluster remains available throughout. |

### 9. Configuration

```yaml
cluster:
  # Unique node ID (defaults to hostname)
  node_id: "node-1"

  # Control plane gRPC server address
  grpc_addr: "10.0.1.1:8443"

  # etcd peer and client addresses
  etcd:
    data_dir: "/var/lib/omnimesh/etcd"
    client_addr: "10.0.1.1:2379"
    peer_addr: "10.0.1.1:2380"

    # Initial cluster members (used only on first boot)
    initial_cluster:
      - name: "node-1"
        peer_url: "https://10.0.1.1:2380"
      - name: "node-2"
        peer_url: "https://10.0.1.2:2380"
      - name: "node-3"
        peer_url: "https://10.0.1.3:2380"

    # TLS (required for production)
    tls:
      cert_file: "/etc/omnimesh/etcd-server.crt"
      key_file: "/etc/omnimesh/etcd-server.key"
      ca_file: "/etc/omnimesh/etcd-ca.crt"

    # Tuning
    snapshot_count: 10000
    heartbeat_interval_ms: 100
    election_timeout_ms: 1000

# CoreDNS reads from the same etcd cluster
dns:
  zone: "omnimesh.local"
  skydns_prefix: "/skydns"
```

---

## Trade-offs and Drawbacks

| Concern | Discussion |
| --- | --- |
| **Memory overhead** | ~200–500 MB per instance. Acceptable for a control plane running on infrastructure nodes (not on every mesh agent). |
| **Binary size** | ~30–50 MB added. Acceptable for a server binary. |
| **Embedding complexity** | Mitigated by well-documented `embed` package and K3s as a reference implementation. |
| **Port management** | etcd requires its own client (2379) and peer (2380) ports. These are separate from the control plane gRPC port. |
| **etcd version coupling** | The embedded etcd version is pinned. Upgrades require testing but are infrequent. |
| **Cross-platform testing** | etcd is primarily optimized for Linux but works on Windows/macOS. Omnimesh CI must test all three. Only the control plane embeds etcd; agents do not. |
| **Minimum 3 nodes for HA** | Raft quorum requires 2 of 3 (or 3 of 5). Single-node works for dev/test without fault tolerance. |

---

## Future Considerations

- **Multi-region federation** — cross-region etcd clusters add latency. A separate RFC should cover multi-region with async replication or independent clusters with federation.
- **Read replicas** — etcd `Learner` nodes can serve stale reads to reduce load on voting members.
- **Compaction policy** — etcd retains all revisions by default. A periodic compaction job should be configured to reclaim disk space.
- **Observability** — etcd exposes Prometheus metrics. These should be wired into Omnimesh's OpenTelemetry pipeline.
- **Backup & restore** — etcd's `snapshot save` / `snapshot restore` provides disaster recovery. An automated backup schedule should be documented.

---

## References

- [etcd Documentation](https://etcd.io/docs/)
- [etcd Embed Package](https://pkg.go.dev/go.etcd.io/etcd/server/v3/embed)
- [CoreDNS etcd Plugin](https://coredns.io/plugins/etcd/)
- [K3s Embedded etcd](https://docs.k3s.io/datastore/ha-embedded)
- [HashiCorp Raft — GitHub](https://github.com/hashicorp/raft) (evaluated, not chosen)
- [NATS JetStream](https://docs.nats.io/nats-concepts/jetstream) (evaluated, not chosen)
- [Raft Consensus Algorithm — Paper](https://raft.github.io/raft.pdf)
- [RFC-0001: Omnimesh Control Plane — Initial Architecture](./rfc-0001-initial-design.md)
