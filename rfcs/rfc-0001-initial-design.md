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

### 1. Declarative Resource Model

All mesh configuration is expressed as **typed resources** following a format inspired by Kubernetes CRDs. Every object in the control plane — services, policies, nodes, namespaces — is a resource with a uniform structure:

```go
// Domain model — internal/domain/model/

type Resource struct {
    APIVersion string           // e.g., "core.omnimesh.io/v1"
    Kind       string           // e.g., "Service", "TrafficPolicy"
    Metadata   ResourceMetadata
    Spec       interface{}      // Kind-specific specification
}

type ResourceMetadata struct {
    Name        string
    Namespace   string            // Empty for cluster-scoped resources
    Labels      map[string]string // User-defined key-value labels
    Annotations map[string]string // Non-identifying metadata
    CreatedAt   time.Time
    UpdatedAt   time.Time
    Revision    int64             // etcd mod revision — for optimistic concurrency
}
```

#### 1.1 API Groups

Resources are organized into **API groups** that correspond to functional domains:

| API Group | Description | Resource Kinds |
| --- | --- | --- |
| `core.omnimesh.io/v1` | Core mesh infrastructure | `Namespace`, `Service`, `Node` |
| `traffic.omnimesh.io/v1` | Traffic management | `TrafficPolicy` |
| `security.omnimesh.io/v1` | Authorization & security | `AuthorizationPolicy` |

API groups provide **versioning** (breaking changes become v2 while v1 keeps working), **scoping** (operators reason about which domain a resource belongs to), and **extensibility** (new groups added without touching existing ones).

#### 1.2 Resource Scoping

| Scope | Meaning | Examples |
| --- | --- | --- |
| **Namespaced** | Lives within a namespace. Name is unique within its namespace. | `Service`, `TrafficPolicy`, `AuthorizationPolicy` |
| **Cluster-scoped** | Global. Name is unique across the entire mesh. | `Namespace`, `Node` |

#### 1.3 `omnimeshctl apply` — Declarative Configuration

The primary way to manage mesh configuration is `omnimeshctl apply`:

```bash
# Apply a single resource from a YAML file
omnimeshctl apply -f service.yaml

# Apply all YAML files in a directory
omnimeshctl apply -f ./production/

# Dry-run (validate without applying)
omnimeshctl apply -f service.yaml --dry-run

# Delete a resource described in a file
omnimeshctl delete -f service.yaml
```

`apply` is an **upsert** operation:
1. If the resource does not exist → **create** it.
2. If the resource exists → **update** it (merge spec, preserve system metadata like `createdAt`).
3. **Optimistic concurrency** — `metadata.revision` is checked on update. Conflicts are rejected.

A single YAML file can contain multiple resources, separated by `---`.

Imperative CLI commands (`service register`, etc.) remain as **convenience wrappers** around `apply` — they construct resources internally and call the same apply path. `apply` is preferred for production, automation, and GitOps workflows.

#### 1.4 Reading Resources

```bash
# Get a specific resource
omnimeshctl get service order-service -n production

# List resources of a kind
omnimeshctl get services -n production
omnimeshctl get namespaces
omnimeshctl get nodes

# Output as YAML (for re-applying or version control)
omnimeshctl get service order-service -n production -o yaml

# List across all namespaces
omnimeshctl get services --all-namespaces

# Filter by labels
omnimeshctl get services -l team=commerce -n production
```

#### 1.5 Labels and Selectors

Resources support **labels** — arbitrary key-value pairs for user-defined organization:

```yaml
metadata:
  labels:
    team: commerce
    tier: backend
    environment: production
```

Labels enable filtering in `omnimeshctl get` queries, grouping in observability dashboards, and future policy targeting by label selector.

---

### 2. High-Level Architecture

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

### 3. Cross-Platform Support & Ambient Mode

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

### 4. Identity & Security — SPIFFE / SPIRE

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

### 5. Namespace — First-Class Resource

Namespaces are explicit, creatable, listable, and deletable resources. They group services and policies into organizational and isolation units.

```yaml
apiVersion: core.omnimesh.io/v1
kind: Namespace
metadata:
  name: production
  labels:
    environment: prod
    team: platform
spec:
  isolation: strict    # "strict" (default-deny within) or "open"
```

- A namespace must exist before any namespaced resource can be created within it.
- Deleting a namespace that contains resources is rejected (unless `--force` is used).
- Namespace isolation mode controls authorization defaults within the namespace (see RFC-0004).

### 6. Mesh IP Addressing (IPAM)

Each service in the mesh is assigned a unique **IPv4 address** from a configurable CIDR block (e.g., `100.64.0.0/10`). **Every service gets an IP assigned at registration time, even if no other service calls it yet.** This allows operators to pre-configure authorization policies and routing before callers exist.

#### Allocation Rules

1. The control plane maintains the authoritative IP allocation table.
2. IPs are assigned on service `apply` and released on service `delete`.
3. Allocations are persisted to survive control plane restarts.
4. Conflicts are detected and rejected at assignment time.
5. The mesh CIDR is configurable at startup and cannot overlap with the enterprise network.

### 7. DNS — CoreDNS Integration

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

### 8. Route Distribution — Pluggable Transport

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

### 9. Core Resource Definitions

#### 9.1 Service (`core.omnimesh.io/v1`)

**Scope**: Namespaced

```yaml
apiVersion: core.omnimesh.io/v1
kind: Service
metadata:
  name: order-service
  namespace: production
  labels:
    team: commerce
    tier: backend
spec:
  protocol: http                  # tcp, http, grpc — determines agent L4/L7 capabilities
  endpoints:
    - address: "10.0.1.5:8080"
      weight: 100
    - address: "10.0.1.6:8080"
      weight: 100
  healthCheck:
    protocol: http
    path: "/healthz"
    intervalSec: 10
    timeoutSec: 3
    healthyAfter: 2
    unhealthyAfter: 3
  dependsOn:                      # Explicit upstream dependencies
    - payment-service
    - inventory-service
```

The `protocol` field (TCP, HTTP, gRPC) determines what level of traffic control the ambient agent can apply — see RFC-0003 for details.

#### 9.2 Node (`core.omnimesh.io/v1`)

**Scope**: Cluster

```yaml
apiVersion: core.omnimesh.io/v1
kind: Node
metadata:
  name: node-1
  labels:
    os: linux
    zone: us-east-1a
spec:
  platform: linux                 # linux, windows, macos, kubernetes
  grpcAddr: "10.0.1.5:8443"
  localServices:
    - namespace: production
      name: order-service
    - namespace: production
      name: frontend
```

### 10. Service Registration

Services enter the mesh through two mechanisms — both result in a `Service` resource being applied:

#### 10.1 CLI / Apply Registration (Administrative)

```bash
# Declarative (preferred for production/GitOps)
omnimeshctl apply -f service.yaml

# Imperative (convenience wrapper — constructs a resource and calls apply)
omnimeshctl service register \
  --name my-api \
  --namespace production \
  --protocol http \
  --endpoints 10.0.1.5:8080,10.0.1.6:8080
```

#### 10.2 Agent Self-Registration (Automatic / Ambient)

Omnimesh Agents running on each node register themselves and the services they discover locally:

```
┌───────────────┐   gRPC: Apply(Node), Apply(Service)   ┌──────────────┐
│  Omnimesh     │ ──────────────────────────────────────► │  Control     │
│  Agent (node) │ ◄──────────────────────────────────── │  Plane       │
│  (ambient)    │   gRPC: Scoped state stream             │              │
└───────────────┘                                         └──────────────┘
```

- Agents apply a `Node` resource providing platform info (OS, K8s node name, capabilities).
- On Kubernetes, agents discover pods on their node via the kubelet API and apply `Service` resources for them.
- On non-Kubernetes platforms, agents discover local services via configuration files or OS-level process inspection.
- The control plane pushes scoped route and policy updates back to the agent over a bidirectional gRPC stream.
- If an agent disconnects, the control plane marks its services as unhealthy after a configurable grace period.

### 11. Resource Validation

Every resource goes through a two-phase validation pipeline before being persisted:

**Phase 1 — Structural**: `apiVersion` is a known group/version, `kind` is valid within the group, `metadata.name` follows naming rules (lowercase alphanumeric + hyphens, 1–63 chars), `metadata.namespace` exists (for namespaced resources), `spec` conforms to the kind's schema.

**Phase 2 — Semantic**: Cross-resource validation (e.g., health check protocol must be compatible with service protocol, policy target service must exist, SPIFFE IDs must be well-formed).

### 12. Hexagonal Architecture Mapping

Following the project's architectural rules, here is how the components map to the hex arch layers:

#### Domain Layer (`internal/domain`)

- `model.Resource` — the universal resource envelope (apiVersion + kind + metadata + spec).
- `model.ResourceMetadata` — name, namespace, labels, annotations, timestamps, revision.
- `model.Service` — service entity with namespace, name, protocol, mesh IP, endpoints, health.
- `model.MeshNetwork` — CIDR block, IP allocation table.
- `model.Node` — agent node entity with platform, ambient mode capabilities, status, registered services.
- `model.MeshRoute` — route to a mesh IP via one or more node endpoints.
- `service.IPAllocator` — domain logic for IP assignment from a CIDR.
- `service.ResourceValidator` — structural and semantic validation for all resource kinds.

#### Ports (`internal/port`)

**Inbound (use cases):**

- `ResourceManager` — generic apply / get / list / delete for all resource kinds.
- `ServiceRegistrar` — register / deregister / list services (delegates to `ResourceManager`).
- `NodeRegistrar` — register / deregister / heartbeat nodes.

**Outbound (infrastructure contracts):**

- `ResourceStore` — generic storage interface for all resources (put, get, list, delete, watch, compare-and-put for optimistic concurrency).
- `IdentityManager` — SPIRE workload registration.
- `DNSRecordManager` — CoreDNS record management.
- `RouteDistributor` — announce / withdraw mesh routes.

#### Application Layer (`internal/application`)

- `ResourceService` — generic apply/get/list/delete. Parses `apiVersion` + `kind`, routes to kind-specific lifecycle services for validation and side effects.
- `ServiceLifecycle` — handles Service-specific side effects: validates input → allocates IP → persists service → registers SPIFFE identity → upserts DNS → announces route.
- `NamespaceLifecycle` — handles Namespace-specific side effects: deletion protection (cannot delete non-empty namespace).
- `NodeManager` — orchestrates node lifecycle, health tracking.

#### Adapters (`internal/adapter`)

**Inbound:**

- `grpc` — gRPC server with `Apply(resource)` / `Get(...)` / `List(...)` / `Delete(...)` generic RPCs, plus convenience RPCs for agents and CLI.
- `cli` — `omnimeshctl` command-line tool (separate binary in `cmd/omnimeshctl/`).

**Outbound:**

- `spire` — SPIRE Server API client (implements `IdentityManager`).
- `coredns` — CoreDNS record pusher (implements `DNSRecordManager`).
- `bgp` — BGP speaker adapter (implements `RouteDistributor`).
- `grpcpush` — gRPC streaming route push (implements `RouteDistributor`).
- `nats` — NATS publisher adapter (implements `RouteDistributor`).
- `etcdstore` — generic `ResourceStore` implementation + state storage.

---

## Trade-offs and Drawbacks

| Concern | Discussion |
| ------- | ---------- |
| **IPv4 only** | IPv6 support is deferred. The chosen CIDR (e.g., `100.64.0.0/10`) provides ~4M addresses, sufficient for initial adoption. IPv6 can be added as a future RFC. |
| **CoreDNS dependency** | Tight coupling to CoreDNS as the DNS layer. Mitigated by the `DNSRecordManager` port — alternative DNS backends can be swapped via adapters. |
| **SPIRE dependency** | SPIRE must be deployed alongside the control plane. For environments that cannot run SPIRE, a future adapter could support alternative CAs, but SPIFFE identity format remains the standard. |
| **Multiple route transports** | Supporting BGP, gRPC, and NATS adds complexity. However, the pluggable port design isolates each adapter. Only the adapters needed for a given deployment are enabled. |
| **Single control plane** | This RFC does not address HA or multi-control-plane federation. A follow-up RFC should cover leader election and state replication. |
| **Agent trust** | Agent self-registration requires trusting the agent. Node attestation via SPIRE mitigates this, but a malicious agent could register rogue services. Authorization policies are needed (see RFC-0004). |
| **CRD model complexity** | The declarative resource model adds structural overhead. However, establishing this pattern early avoids painful migrations later and enables GitOps workflows. |

---

## Future Work (Subsequent RFCs)

- **RFC-0002**: High availability — control plane replication, leader election, state sync, etcd key organization.
- **RFC-0003**: Traffic policies — routing rules, retries, circuit breaking, rate limiting, load balancing.
- **RFC-0004**: Authorization policies — service-to-service access control rules (SPIFFE-based).
- **RFC-0005**: Observability — metrics collection, distributed tracing, access logging.
- **RFC-0006**: Kubernetes integration — CRD-based configuration, operator, DaemonSet deployment.
- **RFC-0007**: Waypoint proxies — optional L7 processing nodes for advanced traffic management.

---

## References

- [SPIFFE Specification](https://spiffe.io/docs/latest/spiffe-about/overview/)
- [SPIRE Documentation](https://spiffe.io/docs/latest/spire-about/)
- [CoreDNS Manual](https://coredns.io/manual/toc/)
- [GoBGP Documentation](https://github.com/osrg/gobgp)
- [NATS Documentation](https://docs.nats.io/)
- [RFC 6598 — Shared Address Space (100.64.0.0/10)](https://datatracker.ietf.org/doc/html/rfc6598)
- [Istio Ambient Mesh](https://istio.io/latest/docs/ambient/overview/)
- [Kubernetes API Conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md)
