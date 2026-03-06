# RFC-0004: Authorization Policies — SPIFFE Identity-Based Access Control

Status: **Draft**

## Problem Statement

RFC-0001 assigns every mesh service a SPIFFE identity and establishes mTLS via SPIRE-issued SVIDs. However, identity alone does not imply authorization — any service with a valid SVID can currently communicate with any other service. In a production environment this is unacceptable: a compromised frontend should not be able to call the payment database directly.

RFC-0003 introduced the **service dependency graph** for scoped agent distribution, but dependencies are currently declared manually. Authorization policies provide the authoritative, security-enforced source of truth for which services are allowed to communicate, and should automatically feed the dependency graph.

**This RFC addresses:**

1. **Identity-based authorization** — controlling service-to-service communication based on SPIFFE IDs.
2. **Default-deny posture** — no communication is allowed unless explicitly authorized (zero trust).
3. **Dependency graph integration** — authorization policies automatically populate the dependency graph from RFC-0003.
4. **Additional security layers** — namespace isolation, L7 authorization (HTTP/gRPC), deny policies, and temporal restrictions.

---

## Design Principles

1. **Zero trust, default deny** — no service-to-service communication is allowed unless an explicit ALLOW policy exists. This is the foundation of mesh security.
2. **Identity first** — all authorization decisions are based on cryptographically verified SPIFFE IDs, not network addresses. IPs can be spoofed; mTLS-verified identities cannot.
3. **Authorization feeds dependencies** — every ALLOW policy automatically creates an entry in the dependency graph. This ensures scoped distribution (RFC-0003) stays in sync with actual communication permissions.
4. **Layered security** — start with identity-based L4 authorization, add L7 rules (HTTP path/method, gRPC service/method) for services that declare HTTP or gRPC protocol.
5. **Explicit over implicit** — deny policies take precedence over allow policies. When in doubt, deny.

---

## Architecture

### 1. SPIFFE Identity Model (Recap)

Every service in the mesh receives a SPIFFE ID at registration time (RFC-0001):

```
spiffe://<trust-domain>/ns/<namespace>/sa/<service-name>
```

Examples:

```
spiffe://omnimesh.corp/ns/production/sa/order-service
spiffe://omnimesh.corp/ns/production/sa/payment-service
spiffe://omnimesh.corp/ns/staging/sa/order-service
```

These identities are materialized as **X.509 SVIDs** issued by SPIRE. The ambient agent on each node uses these SVIDs to establish mTLS between services. When an mTLS connection is established, the agent has **cryptographic proof** of the caller's SPIFFE ID — this is the foundation for authorization.

Every service has an IP assigned in the mesh (RFC-0001), even if no other service calls it yet. This allows administrators to pre-configure authorization policies for services before they have callers — when a caller is later authorized, routing is already in place.

### 2. Authorization Policy Model

An **AuthorizationPolicy** defines who can talk to whom and under what conditions.

#### 2.1 Core Model

```go
// Domain model — internal/domain/model/

type AuthorizationPolicy struct {
    Name        string
    Namespace   string
    Action      AuthzAction           // ALLOW or DENY
    From        []AuthzPeer           // Who is making the request (source)
    To          []AuthzTarget         // Who is receiving the request (destination)
    Rules       []AuthzRule           // Optional L7-level rules (HTTP/gRPC)
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type AuthzAction int

const (
    AuthzAllow AuthzAction = iota
    AuthzDeny
)
```

**YAML Resource Definition** (for `omnimeshctl apply`):

```yaml
apiVersion: security.omnimesh.io/v1
kind: AuthorizationPolicy
metadata:
  name: order-to-payment
  namespace: production
spec:
  action: allow
  from:
    - spiffeID: "spiffe://omnimesh.corp/ns/production/sa/order-service"
  to:
    - service: payment-service
      ports: [8080]
  rules:
    - http:
        methods: [POST]
        pathPrefixes: ["/api/v1/charge", "/api/v1/refund"]
```

```yaml
# Temporal policy with expiration
apiVersion: security.omnimesh.io/v1
kind: AuthorizationPolicy
metadata:
  name: temp-debug-access
  namespace: production
spec:
  action: allow
  from:
    - spiffeID: "spiffe://omnimesh.corp/ns/production/sa/debug-tool"
  to:
    - service: user-service
  validFrom: "2026-03-04T16:00:00Z"
  validUntil: "2026-03-04T18:00:00Z"
```

#### 2.2 Source Identity — `AuthzPeer`

Specifies who is allowed (or denied) to initiate communication:

```go
type AuthzPeer struct {
    // Exact SPIFFE ID match
    // e.g., "spiffe://omnimesh.corp/ns/production/sa/order-service"
    SpiffeID string

    // Namespace wildcard — matches all services in a namespace
    // e.g., "spiffe://omnimesh.corp/ns/production/sa/*"
    // Only one of SpiffeID or Namespace should be set.
    Namespace string

    // Trust domain match — matches all services in a trust domain.
    // Useful for cross-domain federation.
    // e.g., "spiffe://partner.corp/*"
    TrustDomain string
}
```

Matching priority (most specific wins):
1. **Exact SpiffeID** — matches a single service.
2. **Namespace** — matches all services in a given namespace.
3. **TrustDomain** — matches all services in a given trust domain.

#### 2.3 Destination — `AuthzTarget`

Specifies which services are being protected:

```go
type AuthzTarget struct {
    // Target service (required)
    ServiceID ServiceID

    // Optional: restrict to specific ports
    Ports []int
}
```

#### 2.4 L7 Rules — `AuthzRule`

For services with protocol `HTTP` or `GRPC` (as defined in RFC-0003), additional L7 authorization rules can restrict access at the request level:

```go
type AuthzRule struct {
    // HTTP rules — only applicable when target service protocol is HTTP
    HTTP *HTTPAuthzRule

    // gRPC rules — only applicable when target service protocol is GRPC
    GRPC *GRPCAuthzRule
}

type HTTPAuthzRule struct {
    // Allowed HTTP methods (e.g., ["GET", "POST"]). Empty = all methods.
    Methods []string

    // Allowed path prefixes (e.g., ["/api/v1/orders", "/health"]).
    // Empty = all paths.
    PathPrefixes []string

    // Allowed exact paths (e.g., ["/healthz"]). Empty = no exact match filter.
    PathExact []string

    // Required headers (all must match).
    // Useful for version-based routing authorization.
    Headers map[string]string
}

type GRPCAuthzRule struct {
    // Allowed gRPC services (e.g., ["payment.v1.PaymentService"]).
    // Empty = all services.
    Services []string

    // Allowed gRPC methods (e.g., ["Charge", "Refund"]).
    // Empty = all methods.
    Methods []string
}
```

> **Protocol validation**: L7 rules are only valid when the target service has declared the matching protocol (RFC-0003). An `HTTPAuthzRule` on a TCP service is rejected at policy creation time.

---

### 3. Default-Deny Posture

The mesh operates in **default-deny** mode:

```
┌──────────────────────────────────────────────────────────────┐
│                      Authorization Engine                     │
│                                                               │
│  Request: order-service → payment-service                     │
│                                                               │
│  1. Check DENY policies   → if any match → DENY              │
│  2. Check ALLOW policies  → if any match → ALLOW             │
│  3. No policy matches     → DEFAULT DENY                     │
│                                                               │
└──────────────────────────────────────────────────────────────┘
```

Evaluation order:

1. **DENY policies are evaluated first.** If any DENY policy matches the request, the request is denied immediately, regardless of any ALLOW policies.
2. **ALLOW policies are evaluated second.** If at least one ALLOW policy matches, the request is allowed.
3. **Default deny.** If no policy matches, the request is denied.

This ensures that explicit denials always take precedence, providing a safety mechanism against overly broad ALLOW rules.

#### 3.1 Bootstrapping: Mesh-Internal Communication

Certain control plane communications must always be permitted:

- **Agent ↔ Control Plane** gRPC traffic (registration, heartbeat, health reporting, policy streaming).
- **SPIRE Agent ↔ SPIRE Server** attestation traffic.
- **CoreDNS ↔ etcd** DNS record reads (infrastructure-level, not agent traffic).

These are excluded from the authorization engine — they are infrastructure flows, not service-to-service mesh traffic. The agent distinguishes them by destination (control plane endpoints are not mesh service IPs).

---

### 4. Authorization Enforcement

Authorization is enforced at the ambient agent level, at the point of mTLS termination:

```
Service A                  Agent (node-1)                    Agent (node-2)                Service B
    │                          │                                  │                          │
    │  request ──────────────►│                                  │                          │
    │                          │                                  │                          │
    │                          │ 1. Intercept outbound traffic   │                          │
    │                          │ 2. Establish mTLS to node-2     │                          │
    │                          │    (using Service A's SVID)     │                          │
    │                          │──── mTLS ──────────────────────►│                          │
    │                          │                                  │                          │
    │                          │                                  │ 3. Terminate mTLS        │
    │                          │                                  │ 4. Extract caller SPIFFE │
    │                          │                                  │    ID from client cert   │
    │                          │                                  │ 5. Evaluate authz policy │
    │                          │                                  │    for: caller → target  │
    │                          │                                  │                          │
    │                          │                                  │ → ALLOW?                 │
    │                          │                                  │ 6. Forward to Service B  │
    │                          │                                  │────────────────────────►│
    │                          │                                  │                          │
    │                          │                                  │ → DENY?                  │
    │                          │                                  │ Return 403 / PERMISSION_ │
    │                          │                                  │ DENIED / close connection│
```

Key points:

- **Authorization is enforced at the destination agent** (server-side), not at the source. The destination agent can cryptographically verify the caller's identity via the mTLS client certificate.
- **The source agent establishes mTLS** using the calling service's SVID. This proves the caller's identity to the destination.
- **Deny responses** depend on the service protocol:
  - **TCP**: connection is closed immediately.
  - **HTTP**: `403 Forbidden` response.
  - **gRPC**: `PERMISSION_DENIED` status code.

#### 4.1 Agent Policy Cache

Each agent caches only the authorization policies relevant to its local services (following the scoped distribution model from RFC-0003). An agent hosting `payment-service` only needs policies where `payment-service` is in the `To` field.

Policy evaluation uses an in-memory index for fast lookup:

```
Index: target SPIFFE ID → []*AuthorizationPolicy

Lookup key: (caller SPIFFE ID, target SPIFFE ID, target port)
```

---

### 5. Authorization Feeds the Dependency Graph

**This is a critical integration point with RFC-0003.** Every ALLOW policy automatically creates entries in the dependency graph:

```
Authorization Policy                      Dependency Graph
┌──────────────────────────┐              ┌────────────────────────────┐
│ ALLOW:                   │              │                            │
│   from: order-service    │  ─────────►  │ order-service              │
│   to:   payment-service  │   auto-gen   │   depends-on:              │
│                          │              │     - payment-service      │
└──────────────────────────┘              └────────────────────────────┘
```

#### 5.1 How It Works

When an authorization policy is created, updated, or deleted, the application layer automatically recalculates the dependency graph:

```
Admin/CLI                  Application Layer              etcd
    │                           │                           │
    │ CreateAuthzPolicy(        │                           │
    │   ALLOW:                  │                           │
    │   from: order-service     │                           │
    │   to: payment-service)    │                           │
    │──────────────────────────►│                           │
    │                           │ 1. Validate policy        │
    │                           │ 2. Store authz policy     │
    │                           │──────────────────────────►│
    │                           │                           │
    │                           │ 3. Extract dependency:    │
    │                           │    order-service           │
    │                           │    → payment-service       │
    │                           │                           │
    │                           │ 4. Update dependency      │
    │                           │    graph                   │
    │                           │──────────────────────────►│
    │                           │                           │
    │                           │ 5. Trigger scoped         │
    │                           │    distribution update     │
    │                           │    (agents hosting         │
    │                           │     order-service now      │
    │                           │     receive payment-service│
    │                           │     routes)                │
    │  ok                       │                           │
    │◄──────────────────────────│                           │
```

#### 5.2 Dependency Graph as Derived State

With authorization policies as the source of truth, the dependency graph becomes **derived state**:

```
Dependencies(service S) = { T | ∃ ALLOW policy from S to T }
```

Manually declared dependencies (from RFC-0003 `--depends-on`) are still supported as an **override** for cases where an ALLOW policy hasn't been created yet (e.g., during initial setup). Once an ALLOW policy exists, it takes precedence.

The dependency graph stores the origin of each dependency:

```go
type ServiceDependency struct {
    Target   ServiceID
    Origin   DependencyOrigin  // How this dependency was established
    PolicyName string         // Name of the authz policy (if origin is AuthzPolicy)
}

type DependencyOrigin int

const (
    DependencyManual     DependencyOrigin = iota  // From --depends-on CLI flag
    DependencyAuthzPolicy                          // Auto-derived from ALLOW policy
)

```

#### 5.3 Namespace-Level Policies and Dependencies

A namespace-level ALLOW policy (e.g., "all services in `monitoring` can call all services in `production`") creates dependencies for **all current services** in those namespaces. When new services are added to either namespace, the dependency graph is automatically updated.

##### Addressing Dependency Graph Explosion

Namespace-wide or wildcard policies can theoretically cause O(N×M) dependency calculations (every source service × every destination service), which defeats the memory-saving goals of scoped distribution. The control plane addresses this through **aggregate routing rules and lazy resolution**:

1. **Namespace-level wildcard routes**: Instead of pushing thousands of individual service IPs to agents, the control plane pushes a **namespace-level wildcard route** when a namespace-wide policy exists. The dependency graph stores this as a single aggregate entry:

    ```go
    type ServiceDependency struct {
        Target   ServiceID        // ServiceID.Name may be "*" for namespace-wide
        Origin   DependencyOrigin
        PolicyName string
    }
    // Example: order-service depends-on production/* (namespace wildcard)
    ```

2. **Lazy DNS resolution**: When an agent encounters traffic for a destination within a wildcard-authorized namespace, it resolves the specific mesh IP via a lightweight **local DNS lookup** to CoreDNS (already deployed alongside the control plane). The agent caches the resolved IP-to-route mapping for subsequent requests.

3. **On-demand scope expansion**: After a DNS-resolved route is used, the agent requests the specific service's traffic policy and health data from the control plane in the background. The control plane adds the individual service to the agent's scope for future incremental updates.

```
Agent (node-1)                        CoreDNS                     Control Plane
    │                                    │                              │
    │ (has wildcard route for             │                              │
    │  namespace "production")            │                              │
    │                                    │                              │
    │ Traffic to unknown-service          │                              │
    │                                    │                              │
    │ DNS: unknown-service.omnimesh.local ───────────────────────────►│
    │ 100.64.0.42                        │                              │
    │◄───────────────────────────────────│                              │
    │                                    │                              │
    │ Cache: unknown-service → 100.64.0.42                               │
    │ Route traffic via HBONE                                            │
    │                                                                    │
    │ (background) Request scope expansion for unknown-service           │
    ───────────────────────────────────────────────────────────────────►│
    │                                                                    │
```

This approach ensures:
- **Memory stays proportional to actual traffic**: agents only hold routes for services they actually communicate with, not for every service authorized by a wildcard policy.
- **Dependency graph stays compact**: a namespace-wide policy creates one aggregate dependency entry, not N×M individual entries.
- **CoreDNS is already deployed**: no additional infrastructure is needed for the DNS resolution step.

---

### 6. Additional Security Layers

Beyond basic SPIFFE ID authorization, the following mechanisms improve security posture:

#### 6.1 Deny Policies (Explicit Exclusion)

DENY policies provide an explicit override mechanism. They are essential for:

- **Revoking access** in emergencies without modifying ALLOW policies.
- **Narrowing broad ALLOW rules** (e.g., allow all of `production` namespace, but deny `debug-service` specifically).
- **Temporary lockdowns** during incidents.

```bash
# Emergency: block a compromised service from calling anything
omnimeshctl authz create \
  --name block-compromised-frontend \
  --namespace production \
  --action deny \
  --from-spiffe "spiffe://omnimesh.corp/ns/production/sa/frontend-v2" \
  --to-all
```

Since DENY is evaluated before ALLOW, this immediately blocks the service regardless of any existing ALLOW policies.

#### 6.2 L7 Authorization (HTTP/gRPC Granularity)

For services with protocol `HTTP` or `GRPC` (RFC-0003), authorization can go beyond "can A talk to B?" and control **what operations** A can perform:

```bash
# Allow order-service to call payment-service, but only POST /api/v1/charge
omnimeshctl authz create \
  --name order-to-payment \
  --namespace production \
  --action allow \
  --from-service order-service \
  --to-service payment-service \
  --http-methods POST \
  --http-paths "/api/v1/charge"

# Allow monitoring to read health endpoints only
omnimeshctl authz create \
  --name monitoring-health-read \
  --namespace production \
  --action allow \
  --from-namespace monitoring \
  --to-namespace production \
  --http-methods GET \
  --http-paths "/healthz,/readyz,/metrics"
```

This prevents a compromised service from accessing more of the target than intended — even if it has a valid SVID and an ALLOW policy, it can only call the permitted methods and paths.

> **Protocol constraint**: L7 rules require the target service to declare protocol `HTTP` or `GRPC`. For TCP services, only L4 identity-based authorization is supported.

#### 6.3 Namespace Isolation

Namespace isolation is configured directly on the `Namespace` resource (RFC-0001):

```yaml
apiVersion: core.omnimesh.io/v1
kind: Namespace
metadata:
  name: production
spec:
  isolation: strict    # "strict" (default) or "open"
```

| Mode | Behavior |
| --- | --- |
| **strict** (default) | Services within the same namespace cannot communicate without an explicit ALLOW policy. Prevents lateral movement if a service is compromised. |
| **open** | Services within the namespace can communicate freely. Cross-namespace still requires explicit ALLOW. Useful for dev/staging. |

#### 6.4 Temporal Restrictions

Authorization policies can include optional time-based restrictions:

```go
type AuthorizationPolicy struct {
    // ... existing fields ...

    // Optional: time-bound policy. nil = no time restriction.
    ValidFrom   *time.Time    // Policy becomes active at this time
    ValidUntil  *time.Time    // Policy expires at this time (auto-deleted or marked inactive)
}
```

Use cases:
- **Temporary access grants** for debugging: "Allow `debug-tool` to call `user-service` for the next 2 hours."
- **Maintenance windows**: "Allow `migration-worker` to call `legacy-db-service` until March 15th."
- **Scheduled revocations**: "This emergency access expires in 4 hours."

The control plane evaluates `ValidFrom` and `ValidUntil` and automatically deactivates expired policies. Expired ALLOW policies trigger automatic dependency graph recalculation.

#### 6.5 Dry-Run Mode (Audit Without Enforcement)

Before enforcing a new set of authorization policies (especially during initial deployment when moving to default-deny), administrators need to understand what traffic would be blocked:

```go
type AuthzAction int

const (
    AuthzAllow   AuthzAction = iota
    AuthzDeny
    AuthzDryRun  // Log the decision but do not enforce (allow all)
)
```

In **DryRun** mode:
- The agent evaluates policies normally and logs what it would allow/deny.
- All traffic is actually **allowed** regardless of the evaluation result.
- The control plane collects dry-run logs to show administrators what would break.

This allows organizations to deploy authorization policies incrementally:

1. Start with all services in `DryRun` — observe what would be blocked.
2. Create ALLOW policies for legitimate traffic.
3. Switch to `AuthzAllow` / `AuthzDeny` enforcement when confident.

---

### 7. Policy Evaluation Flow

The complete evaluation flow at the destination agent:

```
Incoming mTLS Connection
         │
         ▼
┌─────────────────────────┐
│ Extract caller SPIFFE   │
│ ID from client cert     │
│ (cryptographic proof)   │
└───────────┬─────────────┘
            │
            ▼
┌─────────────────────────┐
│ Look up policies for    │
│ target service          │
└───────────┬─────────────┘
            │
            ▼
┌─────────────────────────┐     ┌──────┐
│ Check DENY policies     │────►│ DENY │──► Close / 403 / PERMISSION_DENIED
│ (caller ID, target,     │ yes └──────┘
│  port, L7 attributes)   │
└───────────┬─────────────┘
            │ no match
            ▼
┌─────────────────────────┐     ┌──────┐
│ Check ALLOW policies    │────►│ALLOW │──► Forward request
│ (caller ID, target,     │ yes └──────┘
│  port, L7 attributes)   │
└───────────┬─────────────┘
            │ no match
            ▼
      ┌───────────┐
      │DEFAULT    │──► Close / 403 / PERMISSION_DENIED
      │DENY       │
      └───────────┘
```

For **L7 policies** (HTTP/gRPC), the L7 attributes are evaluated as an additional filter within the matching policy. A policy that matches on caller and target but fails the L7 rule check is treated as a non-match (not a denial).

---

### 8. Key Space

Following the key space design from RFC-0002, authorization resources are stored under:

- **Resources** (user-defined, under `/omnimesh/resources/`):
  - `/omnimesh/resources/security.omnimesh.io/v1/authorizationpolicies/{namespace}/{name}` → AuthorizationPolicy resource JSON

- **State** (system-derived, under `/omnimesh/state/`):
  - `/omnimesh/state/dependencies/{namespace}/{service-name}` → Computed ServiceDependencies JSON (updated by authz policy changes)

Namespace isolation is stored as part of the `Namespace` resource itself at `/omnimesh/resources/core.omnimesh.io/v1/namespaces/{name}`.

Authorization policy resources are watched by the application layer. On any change:
1. Policy is validated and persisted.
2. Dependency graph is recalculated.
3. Affected agents receive updated policies and routes via scoped distribution.

---

### 9. Hexagonal Architecture Mapping

#### Domain Layer (`internal/domain/model/`)

New domain types:

- `AuthorizationPolicy` — the aggregate root for authorization rules.
- `AuthzPeer` — source identity specification (SPIFFE ID, namespace, trust domain).
- `AuthzTarget` — destination service specification with optional ports.
- `AuthzRule` — L7 authorization rules (HTTP, gRPC).
- `HTTPAuthzRule` — HTTP-specific rules (methods, paths, headers).
- `GRPCAuthzRule` — gRPC-specific rules (services, methods).
- `NamespacePolicy` — namespace isolation mode.
- `ServiceDependency` — extended with `Origin` and `PolicyName` fields.

New domain errors:

- `ErrAuthzPolicyNotFound` — authorization policy does not exist.
- `ErrAuthzPolicyConflict` — conflicting policy (e.g., duplicate name).
- `ErrInvalidAuthzConfig` — invalid policy configuration.
- `ErrProtocolMismatch` — L7 rule on a TCP service.
- `ErrPolicyExpired` — attempt to use an expired temporal policy.

New domain service:

- `AuthzEvaluator` — pure domain logic for evaluating a request against a set of policies. Takes (caller SPIFFE ID, target ServiceID, port, L7 attributes) and returns ALLOW/DENY. This is a domain service because authorization is a core business rule, not infrastructure.

#### Ports (`internal/port/`)

**Inbound (use cases):**

```go
// AuthorizationManager defines inbound use cases for authorization policy management.
type AuthorizationManager interface {
    CreatePolicy(ctx context.Context, policy *domain.AuthorizationPolicy) error
    UpdatePolicy(ctx context.Context, policy *domain.AuthorizationPolicy) error
    GetPolicy(ctx context.Context, namespace, name string) (*domain.AuthorizationPolicy, error)
    DeletePolicy(ctx context.Context, namespace, name string) error
    ListPolicies(ctx context.Context, namespace string) ([]*domain.AuthorizationPolicy, error)

    // EvaluateAccess checks if a caller can reach a target. Used by agents.
    EvaluateAccess(ctx context.Context, req *domain.AuthzRequest) (domain.AuthzDecision, error)

    // Namespace isolation
    SetNamespacePolicy(ctx context.Context, policy *domain.NamespacePolicy) error
    GetNamespacePolicy(ctx context.Context, namespace string) (*domain.NamespacePolicy, error)
}
```

**Outbound (infrastructure contracts):**

```go
// AuthzPolicyStore persists and queries authorization policies.
type AuthzPolicyStore interface {
    PutPolicy(ctx context.Context, policy *domain.AuthorizationPolicy) error
    GetPolicy(ctx context.Context, namespace, name string) (*domain.AuthorizationPolicy, error)
    ListPolicies(ctx context.Context, namespace string) ([]*domain.AuthorizationPolicy, error)
    ListPoliciesByTarget(ctx context.Context, serviceID domain.ServiceID) ([]*domain.AuthorizationPolicy, error)
    DeletePolicy(ctx context.Context, namespace, name string) error
    WatchPolicies(ctx context.Context) (<-chan AuthzPolicyEvent, error)
}

// DependencyStore (extended from RFC-0003)
type DependencyStore interface {
    PutDependencies(ctx context.Context, deps *domain.ServiceDependencies) error
    GetDependencies(ctx context.Context, serviceID domain.ServiceID) (*domain.ServiceDependencies, error)
    RecalculateDependencies(ctx context.Context, serviceID domain.ServiceID) error
    WatchDependencies(ctx context.Context) (<-chan DependencyEvent, error)
}
```

#### Application Layer (`internal/application/`)

- `AuthorizationService` — orchestrates policy CRUD: validates policy → validates protocol compatibility for L7 rules → persists → recalculates dependency graph → triggers scoped distribution to affected agents.
- `DependencyService` (extended from RFC-0003) — now also receives dependency updates from authorization policy changes.

#### Adapters

**Inbound:**

- `grpc` — extends the existing gRPC server with:
  - `CreateAuthzPolicy` / `UpdateAuthzPolicy` / `DeleteAuthzPolicy` / `ListAuthzPolicies` RPCs.
  - `EvaluateAccess` RPC (for on-demand agent queries during on-demand service resolution from RFC-0003).
  - Authorization policy push on the scoped bidirectional agent stream.

**Outbound:**

- `etcdstore` — extends the existing etcd adapter with:
  - Authorization policy storage under `/omnimesh/resources/security.omnimesh.io/v1/authorizationpolicies/` (via generic `ResourceStore`).
  - Watch on authorization policy keys.
  - Updates to dependency state under `/omnimesh/state/dependencies/`.

---

### 10. CLI Extensions

```bash
# Declarative — apply an AuthorizationPolicy resource (preferred for production/GitOps)
omnimeshctl apply -f authz-policy.yaml

# Apply from stdin
omnimeshctl apply -f - <<EOF
apiVersion: security.omnimesh.io/v1
kind: AuthorizationPolicy
metadata:
  name: order-to-payment
  namespace: production
spec:
  action: allow
  from:
    - spiffeID: "spiffe://omnimesh.corp/ns/production/sa/order-service"
  to:
    - service: payment-service
  rules:
    - http:
        methods: [POST]
        pathPrefixes: ["/api/v1/charge", "/api/v1/refund"]
EOF

# Imperative convenience wrappers
omnimeshctl authz create \
  --name order-to-payment \
  --namespace production \
  --action allow \
  --from-service order-service \
  --to-service payment-service

# Emergency DENY (blocks immediately)
omnimeshctl authz create \
  --name block-compromised \
  --namespace production \
  --action deny \
  --from-service compromised-frontend \
  --to-all

# Temporal policy (2 hours of debug access)
omnimeshctl authz create \
  --name temp-debug-access \
  --namespace production \
  --action allow \
  --from-service debug-tool \
  --to-service user-service \
  --valid-for 2h

# Namespace isolation (set via Namespace resource)
omnimeshctl apply -f - <<EOF
apiVersion: core.omnimesh.io/v1
kind: Namespace
metadata:
  name: staging
spec:
  isolation: open
EOF

# Get / delete / list
omnimeshctl get authorizationpolicy order-to-payment -n production
omnimeshctl get authorizationpolicy order-to-payment -n production -o yaml
omnimeshctl get authorizationpolicies -n production
omnimeshctl delete authorizationpolicy temp-debug-access -n production

# View what a service is allowed to call (derived from policies)
omnimeshctl authz show-access --service order-service --namespace production
```

---

### 11. Configuration

```yaml
authorization:
  # Global default mode — "strict" (default-deny) or "permissive" (allow-all, for migration)
  default_mode: "strict"

  # Namespace defaults
  namespace_default_isolation: "strict"

  # Temporal policy settings
  temporal:
    max_valid_duration: "168h"          # Maximum duration for temporal policies (7 days)
    expiry_check_interval: "60s"        # How often to check for expired policies

  # Dry-run settings
  dry_run:
    log_interval: "10s"                 # Aggregate dry-run logs every 10 seconds
    report_to_control_plane: true       # Send dry-run reports to control plane

  # Performance
  policy_cache_size: 10000              # Max cached policies per agent
```

---

## Trade-offs and Drawbacks

| Concern | Discussion |
| --- | --- |
| **Default-deny is disruptive** | Moving to default-deny in an existing mesh blocks all traffic without policies. Mitigated by DryRun mode and the `permissive` global mode for gradual migration. |
| **Agent policy evaluation overhead** | Every request requires policy lookup. Mitigated by in-memory indexing and scoped distribution (agents only hold policies for their local services). |
| **L7 authorization requires protocol awareness** | HTTP path/method and gRPC service/method inspection requires the agent to parse L7 traffic. This is already required by RFC-0003 for retries and rate limiting, so no additional overhead. |
| **Dependency graph coupling** | Authorization policies automatically modify the dependency graph, which affects scoped distribution. A misconfigured policy could send incorrect data to agents. Mitigated by validation and by keeping manual dependencies as an override. |
| **Temporal policy GC** | Expired policies must be cleaned up. The control plane runs a periodic check, adding minor overhead. |
| **Dry-run logging volume** | In DryRun mode, every request generates a log entry. In high-traffic environments, this could be substantial. Mitigated by log aggregation and sampling. |
| **Namespace-wide policies and dependency explosion** | A policy allowing an entire namespace to call another namespace creates O(n×m) dependency entries. Mitigated by lazy expansion — dependencies are computed only for services that actually exist. |

---

## Future Work

- **RFC-0005**: Observability — authorization audit logs, policy violation metrics, dry-run reports dashboard.
- **RFC-0007**: Waypoint proxies — advanced L7 authorization (JWT validation, external authorization webhooks).
- **Request-level attributes** — authorization based on request headers, JWT claims, or custom metadata (requires L7 waypoint proxy).
- **External authorization** — delegation to an external authorization service (OPA, Cedar) for complex policy decisions.
- **Cross-cluster federation** — authorization across trust domains for multi-cluster deployments.
- **Policy inheritance** — hierarchical policies (global → namespace → service) with inheritance and override rules.
- **Automatic policy suggestion** — observe traffic in DryRun mode and suggest ALLOW policies for observed legitimate traffic patterns.

---

## References

- [RFC-0001: Omnimesh Control Plane — Initial Architecture](./rfc-0001-initial-design.md)
- [RFC-0002: High Availability & State Storage — Embedded etcd](./rfc-0002-high-availability.md)
- [RFC-0003: Traffic Policies — Retries, Circuit Breaking, Rate Limiting, Health Detection & Load Balancing](./rfc-0003-traffic-policies.md)
- [SPIFFE Specification](https://spiffe.io/docs/latest/spiffe-about/overview/)
- [SPIRE Documentation](https://spiffe.io/docs/latest/spire-about/)
- [Istio Authorization Policy](https://istio.io/latest/docs/reference/config/security/authorization-policy/)
- [NIST Zero Trust Architecture (SP 800-207)](https://csrc.nist.gov/publications/detail/sp/800-207/final)
- [Envoy RBAC Filter](https://www.envoyproxy.io/docs/envoy/latest/configuration/http/http_filters/rbac_filter)
