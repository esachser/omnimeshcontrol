# RFC-0001: Omnimesh Control Plane — Initial Architecture

Status: **Draft**

## Problem Statement

Enterprise environments run services across heterogeneous infrastructure: bare-metal Linux servers, Windows workstations, macOS developer machines, and Kubernetes clusters. Existing service mesh solutions (Istio, Linkerd, Consul Connect) are heavily coupled to Kubernetes or Linux, leaving multi-OS environments without a unified networking, identity, and observability layer.

**Omnimesh** aims to be a cross-platform service mesh whose control plane can manage services regardless of the underlying operating system or orchestrator. The control plane must:

1. Assign each service a stable, mesh-internal IPv4 address.
2. Provide cryptographic identity and mutual TLS via SPIFFE/SPIRE.
3. Maintain DNS resolution for mesh services through CoreDNS, integrated with the enterprise DNS infrastructure.
4. Distribute routing information to nodes through pluggable transport mechanisms.
5. Accept service registration both from a CLI and from self-registering agent nodes.

---

## Proposed Solution

### 1. High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                      Omnimesh Control Plane                         │
│                                                                     │
│  ┌──────────┐  ┌────────────┐  ┌────────────┐  ┌───────────────┐   │
│  │ Service  │  │  IP Addr   │  │  Identity  │  │    Route      │   │
│  │ Registry │  │  Manager   │  │  Manager   │  │  Distributor  │   │
│  │          │  │ (IPAM)     │  │ (SPIFFE)   │  │  (pluggable)  │   │
│  └────┬─────┘  └─────┬──────┘  └─────┬──────┘  └──────┬────────┘   │
│       │              │               │                │             │
│  ┌────┴──────────────┴───────────────┴────────────────┴──────────┐  │
│  │                   Application Services                        │  │
│  └───────────────────────────┬───────────────────────────────────┘  │
│                              │                                      │
│  ┌─────────┐  ┌──────────┐  │  ┌───────────┐  ┌────────────────┐   │
│  │  gRPC   │  │   CLI    │  │  │  CoreDNS  │  │ Route Adapter  │   │
│  │ Server  │  │ Adapter  │  │  │  Adapter  │  │ (BGP/gRPC/NATS)│   │
│  └────┬────┘  └────┬─────┘  │  └─────┬─────┘  └──────┬─────────┘   │
│       │            │        │        │               │              │
└───────┼────────────┼────────┼────────┼───────────────┼──────────────┘
        │            │        │        │               │
   Agent Nodes     Admin   SPIRE    CoreDNS      BGP Peers /
   (self-register) (CLI)  Server   Instance     gRPC Nodes /
                                                NATS Cluster
```

### 2. Cross-Platform Support & Ambient Mode

The control plane binary itself runs on **Linux, Windows, and macOS**. Every platform uses a **per-node Omnimesh Agent** that transparently intercepts mesh traffic — there are no sidecar proxies.

This follows the **ambient mode** model (inspired by Istio Ambient Mesh): instead of injecting a sidecar proxy into every workload, a shared node-level agent handles mTLS termination, routing, and identity on behalf of all services running on that node. This design has several advantages:

- **No application modification** — services are unaware of the mesh; the agent intercepts traffic transparently.
- **Lower resource overhead** — one agent per node instead of one sidecar per pod/process.
- **Unified model** — the same per-node agent architecture works identically on bare-metal Linux, Windows, macOS, and Kubernetes nodes.
- **Simpler operations** — no mutating webhooks, no sidecar lifecycle management, no pod restart requirements.

| Platform    | Agent Connectivity | Traffic Interception              |
| ----------- | ------------------ | --------------------------------- |
| Linux       | gRPC               | iptables / eBPF / nftables        |
| Windows     | gRPC               | WFP (Windows Filtering Platform)  |
| macOS       | gRPC               | pf (packet filter)                |
| Kubernetes  | gRPC (in-cluster)  | Node-level agent (ambient mode), eBPF / iptables redirect |

#### Ambient Mode on Kubernetes

On Kubernetes nodes, the Omnimesh Agent runs as a **DaemonSet** (one pod per node). It intercepts traffic destined for mesh services using eBPF programs or iptables rules at the node level — without injecting any container into application pods.

```
┌─────────────────── Kubernetes Node ───────────────────┐
│                                                       │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐             │
│  │  Pod A   │  │  Pod B   │  │  Pod C   │  (no        │
│  │ (app)    │  │ (app)    │  │ (app)    │   sidecars) │
│  └────┬─────┘  └────┬─────┘  └────┬─────┘             │
│       │              │              │                  │
│  ─────┴──────────────┴──────────────┴───── node net ── │
│                      │                                 │
│              ┌───────┴────────┐                        │
│              │  Omnimesh      │  ← DaemonSet pod       │
│              │  Agent         │    intercepts mesh      │
│              │  (ambient)     │    traffic via eBPF /   │
│              │                │    iptables redirect    │
│              └───────┬────────┘                        │
│                      │ gRPC                            │
└──────────────────────┼────────────────────────────────┘
                       │
               ┌───────┴────────┐
               │  Omnimesh      │
               │  Control Plane │
               └────────────────┘
```

The agent handles:

- **mTLS termination/origination** — using SVIDs obtained from the local SPIRE Agent.
- **Mesh route enforcement** — redirecting traffic to mesh IPs through the local agent.
- **Service discovery** — reporting locally running services to the control plane.
- **Health checking** — monitoring local service health and reporting status.

Platform-specific behavior lives exclusively in agent-side adapters. The control plane itself is OS-agnostic — it deals only with abstract service, identity, and routing concepts.

### 3. Identity & Security — SPIFFE / SPIRE

All mesh services receive a **SPIFFE ID** in the form:

```
spiffe://<trust-domain>/ns/<namespace>/sa/<service-name>
```

**SPIRE** acts as the identity provider:

- The control plane integrates with SPIRE Server to register workload entries.
- Agent nodes run a SPIRE Agent that attests workloads and delivers SVIDs (X.509 or JWT).
- mTLS between services is established using the SVIDs — the control plane configures which identities are authorized to communicate.

#### Control Plane ↔ SPIRE Interaction

```
┌──────────────┐       Workload Registration        ┌──────────────┐
│  Omnimesh    │ ──────────────────────────────────► │  SPIRE       │
│  Control     │       (register / deregister)       │  Server      │
│  Plane       │ ◄────────────────────────────────── │              │
│              │       SVID Status / Trust Bundles    │              │
└──────────────┘                                     └──────┬───────┘
                                                           │
                                                    Attestation
                                                           │
                                                    ┌──────┴───────┐
                                                    │  SPIRE Agent │
                                                    │  (per node)  │
                                                    └──────────────┘
```

The control plane owns an **outbound port** `IdentityManager` with operations like:

- `RegisterWorkload(ctx, service) error`
- `DeregisterWorkload(ctx, service) error`
- `GetTrustBundle(ctx, trustDomain) (Bundle, error)`

The SPIRE adapter implements this port. If a different identity provider is needed in the future, only the adapter changes.

### 4. Mesh IP Addressing (IPAM)

Each service in the mesh is assigned a unique **IPv4 address** from a configurable CIDR block (e.g., `100.64.0.0/10`).

#### Domain Model

```go
// Example domain concepts (not final API)

type MeshNetwork struct {
    CIDR       netip.Prefix   // e.g., 100.64.0.0/10
    Allocations map[ServiceID]netip.Addr
}

type ServiceID struct {
    Namespace string
    Name      string
}
```

#### Allocation Rules

1. The control plane maintains the authoritative IP allocation table.
2. IPs are assigned on `RegisterService` and released on `DeregisterService`.
3. Allocations are persisted to survive control plane restarts.
4. Conflicts are detected and rejected at assignment time.
5. The mesh CIDR is configurable at startup and cannot overlap with the enterprise network.

### 5. DNS — CoreDNS Integration

Mesh service names are resolvable via a **CoreDNS** instance managed alongside the control plane.

#### DNS Resolution Flow

```
┌──────────┐  query: myservice.omnimesh.local   ┌──────────┐
│  Client  │ ──────────────────────────────────► │ CoreDNS  │
│          │ ◄────────────────────────────────── │          │
│          │  answer: 100.64.0.5                 │          │
└──────────┘                                     └────┬─────┘
                                                      │
                                          ┌───────────┴──────────┐
                                          │                      │
                                    omnimesh.local          everything else
                                    zone (plugin)           ──► enterprise DNS
                                          │                     (forward plugin)
                                          │
                                   ┌──────┴──────┐
                                   │  Omnimesh   │
                                   │  Control    │
                                   │  Plane      │
                                   └─────────────┘
```

#### How It Works

1. CoreDNS runs a custom plugin (or uses the `grpc` / `file` plugin) that sources records from the Omnimesh control plane.
2. When the control plane assigns or changes an IP, it pushes the updated DNS record to CoreDNS.
3. CoreDNS uses the `forward` plugin to proxy all non-mesh queries to the enterprise DNS server(s).
4. The mesh DNS zone is configurable (e.g., `omnimesh.local`, `mesh.corp.example.com`).

#### Outbound Port

```go
type DNSRecordManager interface {
    UpsertRecord(ctx context.Context, fqdn string, addr netip.Addr) error
    DeleteRecord(ctx context.Context, fqdn string) error
}
```

The CoreDNS adapter implements this port. This allows future alternatives (e.g., direct etcd writes, cloud DNS APIs) without domain changes.

### 6. Route Distribution — Pluggable Transport

The control plane must inform nodes about how to reach mesh IPs. The actual transport is **pluggable** — different environments may prefer different mechanisms.

#### Supported Strategies

| Strategy       | Use Case                             | How It Works                                                   |
| -------------- | ------------------------------------ | -------------------------------------------------------------- |
| **BGP Anycast**| Data center / on-prem networks       | Control plane peers with network routers via BGP to announce mesh routes |
| **gRPC Push**  | Direct node communication            | Control plane streams route updates to agents via gRPC         |
| **NATS Pub/Sub**| Event-driven / large-scale clusters | Control plane publishes route updates to a NATS subject; agents subscribe |

#### Architecture

```
                     ┌───────────────────────┐
                     │    Route Distributor  │  (outbound port)
                     │    Interface          │
                     └─────────┬─────────────┘
                               │
              ┌────────────────┼────────────────┐
              │                │                │
     ┌────────┴───────┐ ┌─────┴──────┐ ┌───────┴────────┐
     │  BGP Adapter   │ │ gRPC Push  │ │ NATS Adapter   │
     │ (GoBGP / FRR)  │ │ Adapter    │ │                │
     └────────────────┘ └────────────┘ └────────────────┘
```

#### Outbound Port

```go
type RouteDistributor interface {
    AnnounceRoute(ctx context.Context, route MeshRoute) error
    WithdrawRoute(ctx context.Context, route MeshRoute) error
}

type MeshRoute struct {
    ServiceID   ServiceID
    MeshAddr    netip.Addr
    TargetNodes []NodeEndpoint
}
```

Multiple adapters can be active simultaneously (e.g., BGP for datacenter nodes + gRPC push for Kubernetes agents). The application layer fans out to all configured distributors.

### 7. Service Registration

Services enter the mesh through two mechanisms:

#### 7.1 CLI Registration (Administrative)

An administrator uses the `omnimeshctl` CLI to register, update, or deregister services:

```bash
# Register a new service
omnimeshctl service register \
  --name my-api \
  --namespace production \
  --endpoints 10.0.1.5:8080,10.0.1.6:8080

# Deregister a service
omnimeshctl service deregister --name my-api --namespace production

# List services
omnimeshctl service list --namespace production
```

The CLI communicates with the control plane via gRPC.

#### 7.2 Agent Self-Registration (Automatic / Ambient)

Omnimesh Agents running on each node (as a system service on bare-metal/VMs, or as a DaemonSet on Kubernetes) can register themselves and the services they discover locally:

```
┌───────────────┐   gRPC: RegisterNode / RegisterService   ┌──────────────┐
│  Omnimesh     │ ────────────────────────────────────────► │  Control     │
│  Agent (node) │ ◄──────────────────────────────────────── │  Plane       │
│  (ambient)    │   gRPC: RouteUpdates stream               │              │
└───────────────┘                                           └──────────────┘
```

- Agents register themselves with a `RegisterNode` call providing platform info (OS, K8s node name, capabilities).
- On Kubernetes, agents automatically discover pods on their node via the kubelet API or the downward API and register them as mesh services.
- On non-Kubernetes platforms, agents discover local services via configuration files or OS-level process inspection.
- The control plane pushes route updates and configuration back to the agent over a bidirectional gRPC stream.
- If an agent disconnects, the control plane marks its services as unhealthy after a configurable grace period.

### 8. Hexagonal Architecture Mapping

Following the project's architectural rules, here is how the components map to the hex arch layers:

#### Domain Layer (`internal/domain`)

- `model.Service` — service entity with namespace, name, mesh IP, endpoints, health.
- `model.MeshNetwork` — CIDR block, IP allocation table.
- `model.Node` — agent node entity with platform (linux/windows/macos/k8s-node), ambient mode capabilities, status, registered services.
- `model.MeshRoute` — route to a mesh IP via one or more node endpoints.
- `service.IPAllocator` — domain logic for IP assignment from a CIDR.

#### Ports (`internal/port`)

**Inbound (use cases):**

- `ServiceRegistrar` — register / deregister / list services.
- `NodeRegistrar` — register / deregister / heartbeat nodes.

**Outbound (infrastructure contracts):**

- `ServiceRepository` — persist and query services.
- `NodeRepository` — persist and query nodes.
- `IdentityManager` — SPIRE workload registration.
- `DNSRecordManager` — CoreDNS record management.
- `RouteDistributor` — announce / withdraw mesh routes.

#### Application Layer (`internal/application`)

- `ServiceManager` — orchestrates `ServiceRegistrar` use case: validates input → allocates IP → persists service → registers SPIFFE identity → upserts DNS → announces route.
- `NodeManager` — orchestrates node lifecycle, health tracking.

#### Adapters (`internal/adapter`)

**Inbound:**

- `grpc` — gRPC server for agent and CLI communication.
- `cli` — `omnimeshctl` command-line tool (separate binary in `cmd/omnimeshctl/`).

**Outbound:**

- `spire` — SPIRE Server API client (implements `IdentityManager`).
- `coredns` — CoreDNS record pusher (implements `DNSRecordManager`).
- `bgp` — BGP speaker adapter (implements `RouteDistributor`).
- `grpcpush` — gRPC streaming route push (implements `RouteDistributor`).
- `nats` — NATS publisher adapter (implements `RouteDistributor`).
- `persistence` — service/node storage (bolt, SQLite, or PostgreSQL).

---

## Trade-offs and Drawbacks

| Concern | Discussion |
| ------- | ---------- |
| **IPv4 only** | IPv6 support is deferred. The chosen CIDR (e.g., `100.64.0.0/10`) provides ~4M addresses, sufficient for initial adoption. IPv6 can be added as a future RFC. |
| **CoreDNS dependency** | Tight coupling to CoreDNS as the DNS layer. Mitigated by the `DNSRecordManager` port — alternative DNS backends can be swapped via adapters. |
| **SPIRE dependency** | SPIRE must be deployed alongside the control plane. For environments that cannot run SPIRE, a future adapter could support alternative CAs, but SPIFFE identity format remains the standard. |
| **Multiple route transports** | Supporting BGP, gRPC, and NATS adds complexity. However, the pluggable port design isolates each adapter. Only the adapters needed for a given deployment are enabled. |
| **Single control plane** | This RFC does not address HA or multi-control-plane federation. A follow-up RFC should cover leader election and state replication. |
| **Agent trust** | Agent self-registration requires trusting the agent. Node attestation via SPIRE mitigates this, but a malicious agent could register rogue services. Authorization policies are needed (future RFC). |

---

## Future Work (Subsequent RFCs)

- **RFC-0002**: High availability — control plane replication, leader election, state sync.
- **RFC-0003**: Traffic policies — routing rules, retries, circuit breaking, load balancing.
- **RFC-0004**: Authorization policies — service-to-service access control rules.
- **RFC-0005**: Observability — metrics collection, distributed tracing, access logging.
- **RFC-0006**: Kubernetes integration — CRD-based configuration, operator, DaemonSet deployment.
- **RFC-0007**: Waypoint proxies — optional L7 processing nodes for advanced traffic management (header routing, retries, rate limiting) beyond what the ambient agent provides.

---

## References

- [SPIFFE Specification](https://spiffe.io/docs/latest/spiffe-about/overview/)
- [SPIRE Documentation](https://spiffe.io/docs/latest/spire-about/)
- [CoreDNS Manual](https://coredns.io/manual/toc/)
- [GoBGP Documentation](https://github.com/osrg/gobgp)
- [NATS Documentation](https://docs.nats.io/)
- [RFC 6598 — Shared Address Space (100.64.0.0/10)](https://datatracker.ietf.org/doc/html/rfc6598)
- [Istio Ambient Mesh](https://istio.io/latest/docs/ambient/overview/)
