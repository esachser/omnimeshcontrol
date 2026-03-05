# RFC-0003: Traffic Policies — Retries, Circuit Breaking, Rate Limiting, Health Detection & Load Balancing

Status: **Draft**

## Problem Statement

RFC-0001 defines the service mesh infrastructure: service registration, IP allocation, mTLS identity, DNS, and route distribution. RFC-0002 adds high availability with embedded etcd. However, the mesh currently lacks traffic resilience: if a service instance or an agent node goes down, the mesh has no mechanism to detect the failure, stop sending traffic to it, retry failed requests, limit abusive traffic, or distribute traffic intelligently across healthy instances.

Additionally, the ambient agent model intercepts traffic transparently, but different services use different protocols. The agent needs to know **what protocol a service speaks** (raw TCP, HTTP/1.1, HTTP/2, gRPC) to determine what level of inspection and control it can apply. Without this knowledge, the agent cannot distinguish between L4-only services (where it can only observe connection-level events) and L7 services (where it can inspect HTTP status codes, retry on 5xx, inspect headers, etc.).

Finally, in a large mesh, agents should **not** receive the entire mesh state. Each agent only needs to know about the services it hosts locally and the services those local services communicate with. Distributing all policies and routes to every agent wastes memory, bandwidth, and exposes unnecessary information.

**This RFC addresses:**

1. **Service Protocol Model** — services declare their protocol, which determines what level of traffic control the agent can apply.
2. **Health Detection** — how agents detect service/agent unavailability and actively notify the control plane.
3. **Retries** — automatic retry of failed requests with configurable policies.
4. **Circuit Breaking** — preventing cascading failures by stopping traffic to unhealthy endpoints.
5. **Rate Limiting** — protecting services from overload with token bucket and concurrent request limits.
6. **Load Balancing** — distributing traffic across service endpoints with weight-based strategies.
7. **Scoped Agent Distribution** — agents receive only the state they need, not the full mesh.

---

## Design Principles

1. **Agent-centric execution** — traffic policies are defined in the control plane but enforced by the ambient agent on each node. The control plane distributes policy; agents execute it.
2. **Protocol-aware** — the agent's capabilities depend on the declared service protocol. L4 services get connection-level resilience; L7 services get full request-level control.
3. **Active + passive health detection** — agents both actively probe local services AND passively observe traffic failures.
4. **Agent self-reporting** — agents are responsible for notifying the control plane when they detect a local service is down.
5. **Weights everywhere** — all load balancing algorithms consider endpoint weights as a first-class concept.
6. **Scoped distribution** — each agent receives only the policies, routes, and health data relevant to its local services and their dependencies.

---

## Architecture

### 1. Service Protocol Model

The agent intercepts traffic at the network level (L4), but to provide features like HTTP retries, gRPC circuit breaking, or rate limiting by request count, it needs to understand the application protocol. Every service in the mesh must declare its **protocol**.

#### 1.1 Protocol Declaration

```go
// Domain model — internal/domain/model/

type ServiceProtocol int

const (
    // ProtocolTCP is a raw TCP service. The agent can only observe
    // connection-level events (connect, disconnect, errors).
    // Retries apply to connection attempts only. No request-level
    // inspection is possible.
    ProtocolTCP ServiceProtocol = iota

    // ProtocolHTTP is an HTTP/1.1 or HTTP/2 service. The agent
    // terminates mTLS then inspects HTTP request/response headers
    // and status codes. Full L7 retry, circuit breaking, and
    // rate limiting by request are available.
    ProtocolHTTP

    // ProtocolGRPC is a gRPC service (HTTP/2 + protobuf). The agent
    // can inspect gRPC status codes and metadata. Full L7 features
    // are available, plus gRPC-specific health checking.
    ProtocolGRPC
)
```

#### 1.2 Impact on Agent Capabilities

The protocol determines what the agent can do:

| Capability | TCP (L4) | HTTP (L7) | gRPC (L7) |
| --- | --- | --- | --- |
| **Connection-level health check** | ✅ TCP connect | ✅ TCP connect | ✅ TCP connect |
| **Application health check** | ❌ | ✅ `GET /healthz` | ✅ `grpc.health.v1` |
| **Retry on connection failure** | ✅ | ✅ | ✅ |
| **Retry on response error** | ❌ | ✅ (5xx status codes) | ✅ (gRPC status codes) |
| **Circuit breaking on failure rate** | ✅ (connection errors) | ✅ (connection + HTTP errors) | ✅ (connection + gRPC errors) |
| **Rate limiting (requests/sec)** | ❌ (connection rate only) | ✅ | ✅ |
| **Rate limiting (concurrent)** | ✅ (concurrent connections) | ✅ (concurrent requests) | ✅ (concurrent requests) |
| **Passive health observation** | ✅ (connect/disconnect) | ✅ (HTTP status codes) | ✅ (gRPC status codes) |

**L7 inspection is possible because the agent already terminates mTLS** (as described in RFC-0001). After mTLS termination, the agent sees plaintext HTTP/gRPC traffic and can parse headers, status codes, and bodies.

For TCP services, the agent operates as an L4 proxy: it can only observe connection establishment, connection errors, and bytes transferred. It cannot inspect the application protocol.

#### 1.3 Service Model Extension

The `Service` domain model from RFC-0001 is extended with `Protocol`:

```go
type Service struct {
    Name        string
    Namespace   string
    Protocol    ServiceProtocol    // NEW: TCP, HTTP, or GRPC
    MeshAddr    netip.Addr
    Endpoints   []ServiceEndpoint
    HealthCheck *HealthCheck
    // ... existing fields
}
```

The protocol is set at service registration time and can be updated later. The agent uses it to determine its interception mode for traffic destined to that service.

---

### 2. Health Detection

Health detection operates at two levels: **agent-level** (is the agent itself alive?) and **service-level** (are the individual services on an agent healthy?).

#### 2.1 Agent Liveness

Agent liveness is already covered by RFC-0002's etcd lease mechanism:

- Agent calls `RegisterNode` → control plane creates a 30s etcd lease.
- Agent calls `Heartbeat` periodically → control plane renews the lease.
- If the agent stops heartbeating, the lease expires → control plane marks all services on that node as unhealthy and withdraws their routes.

**No changes needed** for agent-level liveness.

#### 2.2 Service Health Detection (Agent-Side)

Each agent monitors the health of services running locally on its node. The agent uses two complementary mechanisms:

##### Active Health Checks

The agent periodically probes local service endpoints. The probe type is constrained by the service's declared protocol:

```
Agent Node
┌──────────────────────────────────────────────────────────┐
│                                                          │
│  ┌──────────┐   health probe    ┌──────────────────┐    │
│  │ Omnimesh │ ────────────────► │ Service Instance │    │
│  │ Agent    │   (TCP/HTTP/gRPC) │ :8080            │    │
│  │          │ ◄──────────────── │                  │    │
│  │          │   response/error  └──────────────────┘    │
│  │          │                                            │
│  │          │   health probe    ┌──────────────────┐    │
│  │          │ ────────────────► │ Service Instance │    │
│  │          │   (TCP/HTTP/gRPC) │ :9090            │    │
│  │          │ ◄──────────────── │                  │    │
│  └────┬─────┘   response/error  └──────────────────┘    │
│       │                                                  │
└───────┼──────────────────────────────────────────────────┘
        │ gRPC: ReportServiceHealth
        ▼
   Control Plane
```

| Service Protocol | Available Probes | Notes |
| --- | --- | --- |
| **TCP** | TCP connect only | Success = connection established within timeout |
| **HTTP** | TCP connect, HTTP GET | HTTP probe sends `GET` to configured path, expects 2xx |
| **gRPC** | TCP connect, gRPC Health | Calls `grpc.health.v1.Health/Check`, expects `SERVING` |

Health check configuration per service:

```go
type HealthCheck struct {
    Protocol       HealthCheckProtocol // TCP, HTTP, GRPC — must be compatible with service protocol
    Path           string              // HTTP path (e.g., "/healthz"), empty for TCP/gRPC
    IntervalSec    int                 // Probe interval (default: 10s)
    TimeoutSec     int                 // Probe timeout (default: 3s)
    HealthyAfter   int                 // Consecutive successes to mark healthy (default: 2)
    UnhealthyAfter int                 // Consecutive failures to mark unhealthy (default: 3)
}
```

> **Validation rule**: The health check protocol must be compatible with the service protocol. A TCP service cannot use an HTTP health check (the agent wouldn't know how to parse the response). An HTTP service can use either TCP or HTTP checks. A gRPC service can use TCP or gRPC checks.

##### Passive Health Observation (Traffic-Based)

The agent passively monitors real traffic flowing through it. What the agent can observe depends on the service protocol:

- **TCP services**: connection refused, connection timeout, connection reset.
- **HTTP services**: all TCP observations + HTTP 5xx responses.
- **gRPC services**: all TCP observations + gRPC status codes `UNAVAILABLE`, `INTERNAL`, `DEADLINE_EXCEEDED`.

These observations feed into the same health state machine as active probes.

##### Health State Machine

Each service endpoint on an agent has a health state:

```
                  ┌────────────────┐
       start ──►  │    UNKNOWN     │
                  └───────┬────────┘
                          │ first probe result
                 ┌────────┴────────┐
                 ▼                 ▼
          ┌──────────┐      ┌────────────┐
          │ HEALTHY  │      │ UNHEALTHY  │
          └────┬─────┘      └─────┬──────┘
               │                  │
               │ UnhealthyAfter  │ HealthyAfter
               │ consecutive     │ consecutive
               │ failures        │ successes
               ▼                 ▼
          ┌────────────┐   ┌──────────┐
          │ UNHEALTHY  │   │ HEALTHY  │
          └────────────┘   └──────────┘
```

#### 2.3 Agent → Control Plane Health Reporting

When an agent detects a health state change for a local service, it **actively reports** it to the control plane via gRPC:

```
Agent                     Control Plane                      etcd
  │                             │                               │
  │ ReportServiceHealth(        │                               │
  │   serviceID,                │                               │
  │   endpointAddr,             │                               │
  │   status: UNHEALTHY,        │                               │
  │   reason: "3 consecutive    │                               │
  │           TCP failures")    │                               │
  │────────────────────────────►│                               │
  │                             │  Update service health        │
  │                             │  in state store               │
  │                             │──────────────────────────────►│
  │                             │                               │
  │                             │  etcd Watch fires             │
  │                             │◄──────────────────────────────│
  │                             │                               │
  │                             │  Recalculate routes           │
  │                             │  (remove unhealthy endpoint)  │
  │                             │                               │
  │                             │  Push updated routes to       │
  │                             │  AFFECTED agents only         │
  │                             │  (scoped distribution)        │
  │                             │──────── (gRPC stream) ───────►│
  │  ok                         │                               │
  │◄────────────────────────────│                               │
```

The control plane **does not** poll agents for health — agents push health changes proactively. This ensures fast detection and scalability.

#### 2.4 etcd Key Space Extension

New keys under the `/omnimesh/` prefix:

Following the key space design from RFC-0002, new keys are added:

- **Resources** (user-defined, under `/omnimesh/resources/`):
  - `/omnimesh/resources/traffic.omnimesh.io/v1/trafficpolicies/{namespace}/{name}` → TrafficPolicy resource JSON

- **State** (system-derived, under `/omnimesh/state/`):
  - `/omnimesh/state/health/{namespace}/{service-name}/{endpoint-addr}` → EndpointHealth JSON
  - `/omnimesh/state/dependencies/{namespace}/{service-name}` → Computed ServiceDependencies JSON

---

### 3. Traffic Policy Model

A **TrafficPolicy** is the top-level domain object that binds retry, circuit breaking, rate limiting, and load balancing configuration to a target service:

```go
// Domain model — internal/domain/model/

type TrafficPolicy struct {
    Name            string
    Namespace       string
    TargetService   ServiceID             // Which service this policy applies to
    Retry           *RetryPolicy          // nil = no retries
    CircuitBreaker  *CircuitBreakerPolicy // nil = no circuit breaking
    RateLimiter     *RateLimitPolicy      // nil = no rate limiting
    LoadBalancer    *LoadBalancerPolicy   // nil = default (weighted round-robin)
    CreatedAt       time.Time
    UpdatedAt       time.Time
}
```

**YAML Resource Definition** (for `omnimeshctl apply`):

```yaml
apiVersion: traffic.omnimesh.io/v1
kind: TrafficPolicy
metadata:
  name: order-service-policy
  namespace: production
  labels:
    tier: backend
spec:
  targetService: order-service
  retry:
    maxRetries: 3
    retryOn: [connection-failure, 5xx]
    backoffBase: 100ms
    backoffMax: 5s
  circuitBreaker:
    failureThreshold: 5
    failureWindow: 30s
    halfOpenAfter: 15s
    halfOpenMaxRequests: 1
    maxConcurrentRequests: 100
  rateLimiter:
    requestsPerSecond: 100.0
    burstSize: 150
    maxConcurrentRequests: 50
    limitExceededAction: reject
  loadBalancer:
    algorithm: weighted-round-robin
```

Each service can have **at most one** TrafficPolicy. If no policy is defined, the agent applies sensible defaults (no retries, no circuit breaking, no rate limiting, weighted round-robin load balancing).

> **Protocol interaction**: The agent validates the policy against the service protocol. For example, if a TCP service has `RetryOn5xx` configured, the control plane rejects the policy at creation time — a TCP service cannot inspect HTTP status codes.

---

### 4. Retries

The agent retries failed requests based on the retry policy configured for the target service.

#### 4.1 Retry Policy

```go
type RetryPolicy struct {
    MaxRetries             int              // Maximum retry attempts (e.g., 3). 0 = no retries.
    RetryOn                []RetryCondition // When to retry
    BackoffBase            time.Duration    // Base backoff duration (e.g., 100ms)
    BackoffMax             time.Duration    // Maximum backoff duration (e.g., 5s)
    RetryableStatusCodes   []int            // HTTP status codes that trigger retry (e.g., 502, 503, 504)
}

type RetryCondition int

const (
    // L4 conditions — available for all protocols
    RetryOnConnectionFailure RetryCondition = iota  // TCP connection refused/timeout
    RetryOnReset                                     // Connection reset mid-request

    // L7 conditions — available for HTTP and gRPC protocols only
    RetryOn5xx                                       // HTTP 5xx responses
    RetryOnGRPCUnavailable                           // gRPC UNAVAILABLE
    RetryOnGRPCDeadlineExceeded                      // gRPC DEADLINE_EXCEEDED
)
```

#### 4.2 Protocol-Specific Retry Behavior

| Service Protocol | Retryable Events | Retry Unit |
| --- | --- | --- |
| **TCP** | Connection failure, connection reset | Connection attempt |
| **HTTP** | All TCP events + HTTP 5xx, configurable status codes | HTTP request |
| **gRPC** | All TCP events + gRPC status codes | gRPC call |

For TCP services, retries only apply to **connection establishment**. Once a connection is established, the agent cannot retry application-level operations because it does not understand the wire protocol.

#### 4.3 Retry Flow

```
Client Request ──► Agent
                    │
                    ├──► Endpoint A ── fails (502) ──┐
                    │                                  │ matches RetryOn5xx
                    │    wait: backoff(attempt=1)      │
                    │◄─────────────────────────────────┘
                    │
                    ├──► Endpoint B ── fails (503) ──┐
                    │                                  │ matches RetryOn5xx
                    │    wait: backoff(attempt=2)      │
                    │◄─────────────────────────────────┘
                    │
                    ├──► Endpoint C ── succeeds (200)
                    │
                    └──► Response to Client (200)
```

Retry rules:

1. Retries are attempted against **different endpoints** when possible (retry on a different instance to avoid hitting the same failing instance).
2. Backoff uses **exponential backoff with jitter**: `min(backoffBase * 2^attempt + jitter, backoffMax)`.
3. The total retry time is bounded: if the original request timeout is reached, retries stop.
4. Only **idempotent** requests should be retried by default. The policy can override this for specific services that are known to be idempotent.

---

### 5. Circuit Breaking

Circuit breaking prevents cascading failures by stopping traffic to an endpoint that is consistently failing.

#### 5.1 Circuit Breaker Policy

```go
type CircuitBreakerPolicy struct {
    // Thresholds for opening the circuit
    FailureThreshold    int           // Number of failures to open circuit (e.g., 5)
    FailureWindow       time.Duration // Rolling window for counting failures (e.g., 30s)

    // Recovery
    HalfOpenAfter       time.Duration // Time to wait before probing (e.g., 15s)
    HalfOpenMaxRequests int           // Requests allowed in half-open state (e.g., 1)

    // Scope
    MaxConcurrentRequests int         // Max in-flight requests per endpoint (0 = unlimited)
}
```

What counts as a "failure" depends on the service protocol:

| Service Protocol | Failure Events |
| --- | --- |
| **TCP** | Connection refused, connection timeout, connection reset |
| **HTTP** | All TCP failures + HTTP 5xx responses |
| **gRPC** | All TCP failures + gRPC `UNAVAILABLE`, `INTERNAL`, `DEADLINE_EXCEEDED` |

#### 5.2 Circuit Breaker State Machine

The circuit breaker operates **per-endpoint** at the agent level:

```
                    ┌──────────┐
       start ──►    │  CLOSED  │  (traffic flows normally)
                    └────┬─────┘
                         │
                         │ FailureThreshold reached
                         │ within FailureWindow
                         ▼
                    ┌──────────┐
                    │   OPEN   │  (all requests fail-fast with 503 / connection refused)
                    └────┬─────┘
                         │
                         │ HalfOpenAfter elapsed
                         ▼
                    ┌──────────────┐
                    │  HALF-OPEN   │  (allow HalfOpenMaxRequests probe requests)
                    └──────┬───────┘
                           │
                  ┌────────┴────────┐
                  │                 │
              probe succeeds   probe fails
                  │                 │
                  ▼                 ▼
             ┌──────────┐    ┌──────────┐
             │  CLOSED  │    │   OPEN   │  (reset HalfOpenAfter timer)
             └──────────┘    └──────────┘
```

#### 5.3 Interaction with Retries

When an endpoint's circuit is **OPEN**, the retry logic skips that endpoint entirely. If all endpoints for a service have open circuits, the agent returns a `503 Service Unavailable` (HTTP/gRPC) or refuses the connection (TCP) immediately without retrying.

#### 5.4 Interaction with Health Detection

Circuit breaking (agent-local, per-endpoint, based on traffic failure rates) and health detection (active probes + passive observation → reported to control plane) are complementary:

| Mechanism | Scope | Speed | Effect |
| --- | --- | --- | --- |
| **Circuit Breaker** | Per-agent, per-endpoint | Immediate (on failure) | Agent locally stops sending to that endpoint |
| **Health Detection** | Cluster-wide (via control plane) | Seconds (probe interval + reporting) | Control plane removes endpoint from all agents' routes |

A single agent's circuit breaker protects **that agent's traffic** instantly. Once the agent also reports the health change, the control plane propagates the information cluster-wide.

---

### 6. Rate Limiting

Rate limiting protects services from being overwhelmed — whether by misbehaving clients, traffic spikes, or cascading retry storms. Rate limits are enforced **at the agent** on inbound traffic to a service.

#### 6.1 Rate Limit Policy

```go
type RateLimitPolicy struct {
    // Request rate limiting (token bucket)
    // Available for HTTP and gRPC services only — TCP services cannot count "requests".
    RequestsPerSecond    float64       // Sustained rate limit (e.g., 100.0 req/s). 0 = unlimited.
    BurstSize            int           // Maximum burst above sustained rate (e.g., 150). Defaults to RequestsPerSecond if unset.

    // Concurrent request/connection limiting
    // Available for all protocols.
    MaxConcurrentRequests int          // Max in-flight requests/connections to the service (0 = unlimited)

    // Behavior when limit is exceeded
    LimitExceededAction   RateLimitAction
}

type RateLimitAction int

const (
    // RateLimitReject rejects excess requests immediately.
    // HTTP: 429 Too Many Requests. gRPC: RESOURCE_EXHAUSTED. TCP: connection refused.
    RateLimitReject RateLimitAction = iota

    // RateLimitQueue queues excess requests up to QueueTimeout, then rejects.
    // Only available for HTTP/gRPC (L7). TCP services always reject.
    RateLimitQueue
)
```

#### 6.2 Token Bucket Algorithm

Request rate limiting uses a **token bucket** algorithm:

```
Token Bucket (per service, per agent)
┌────────────────────────────────────────┐
│                                        │
│  Capacity: BurstSize (e.g., 150)       │
│  Refill rate: RequestsPerSecond        │
│                                        │
│  ┌──────────────────────────────────┐  │
│  │ ○ ○ ○ ○ ○ ○ ○ ○ ○ ○ ○ ○ ○ ○ ○  │  │   ← tokens
│  └──────────────────────────────────┘  │
│                                        │
│  Request arrives:                      │
│    tokens > 0 → consume 1, allow       │
│    tokens = 0 → reject or queue        │
│                                        │
└────────────────────────────────────────┘
```

Properties:
- **Sustained rate**: `RequestsPerSecond` tokens refill per second.
- **Burst tolerance**: `BurstSize` tokens can accumulate, allowing short bursts above the sustained rate.
- Bucket starts full at agent startup.

#### 6.3 Protocol-Specific Behavior

| Service Protocol | Request Rate Limit | Concurrent Limit | Exceeded Response |
| --- | --- | --- | --- |
| **TCP** | ❌ Not available (no request boundary) | ✅ Max concurrent connections | Connection refused |
| **HTTP** | ✅ Requests/sec (token bucket) | ✅ Max concurrent requests | `429 Too Many Requests` or queue |
| **gRPC** | ✅ Requests/sec (token bucket) | ✅ Max concurrent requests | `RESOURCE_EXHAUSTED` or queue |

#### 6.4 Scope: Local vs. Global Rate Limiting

This RFC implements **local (per-agent) rate limiting** only. Each agent enforces rate limits independently based on the traffic it sees. This means:

- A service with a 100 req/s limit across 5 agents can receive up to 500 req/s total.
- This is acceptable for the initial implementation: it protects each agent and each service instance from overload.

> **Future work**: Global (distributed) rate limiting requires a shared counter (e.g., via the control plane or a distributed counter in etcd). This is deferred — local rate limiting covers the majority of use cases.

#### 6.5 Rate Limiting vs. Circuit Breaking

These are complementary mechanisms:

| Mechanism | Purpose | Trigger | Scope |
| --- | --- | --- | --- |
| **Rate Limiting** | Prevent overload *before* failures occur | Request volume exceeds threshold | Per-service, per-agent |
| **Circuit Breaking** | Stop traffic *after* failures are detected | Error rate exceeds threshold | Per-endpoint, per-agent |

Rate limiting is **proactive** (prevent overload). Circuit breaking is **reactive** (respond to failures).

#### 6.6 Interaction with Retries

Retried requests **count against rate limits**. A request that is retried 3 times consumes 3 tokens. This is intentional — it prevents retry storms from overwhelming the target service. If a service's rate limit is reached due to retries, further retries are rejected immediately.

---

### 7. Load Balancing

When a service has multiple endpoints, the agent selects an endpoint using a load balancing algorithm. **All algorithms consider endpoint weights.**

#### 7.1 Endpoint Weights

Each service endpoint has a **weight** (default: 100). Weights are relative: an endpoint with weight 200 receives roughly twice the traffic of an endpoint with weight 100.

```go
// Extension to existing domain model
type ServiceEndpoint struct {
    Address     string               // host:port
    Weight      int                  // Relative weight (default: 100, range: 1-1000)
    Health      EndpointHealthStatus
}

type EndpointHealthStatus int

const (
    EndpointHealthUnknown   EndpointHealthStatus = iota
    EndpointHealthHealthy
    EndpointHealthUnhealthy
)
```

Weights can be set:
- At registration time (via CLI or agent self-registration).
- Dynamically via `omnimeshctl` or the gRPC API.
- The control plane distributes updated weights to agents via route updates.

#### 7.2 Load Balancing Algorithms

```go
type LoadBalancerPolicy struct {
    Algorithm   LoadBalancerAlgorithm
}

type LoadBalancerAlgorithm int

const (
    // LBWeightedRoundRobin distributes requests in round-robin order,
    // proportional to endpoint weights. This is the default.
    LBWeightedRoundRobin LoadBalancerAlgorithm = iota

    // LBWeightedRandom selects an endpoint randomly, with probability
    // proportional to endpoint weights.
    LBWeightedRandom

    // LBWeightedLeastConnections selects the endpoint with the fewest
    // active connections, weighted by endpoint weight.
    // Effective weight = weight / (active_connections + 1)
    LBWeightedLeastConnections
)
```

##### Weighted Round-Robin

Uses a smooth weighted round-robin (SWRR) algorithm to distribute traffic proportionally while avoiding bursts. Given endpoints A(w=5), B(w=3), C(w=2):

```
Request 1 → A    (AABBBCC... is NOT the pattern)
Request 2 → A    (ABACABACBA... smooth distribution)
Request 3 → B
Request 4 → A
Request 5 → C
Request 6 → B
...
```

##### Weighted Random

Each endpoint's probability of selection = `weight / total_weight`. Simple and effective for large request volumes.

##### Weighted Least Connections

The agent tracks active (in-flight) connections per endpoint. Selection formula:

```
effective_weight(endpoint) = endpoint.weight / (active_connections + 1)
```

The endpoint with the **highest effective weight** is selected.

#### 7.3 Unhealthy Endpoint Exclusion

Load balancing only considers endpoints with `Health == EndpointHealthHealthy`. Unhealthy endpoints are excluded from the selection pool. If an endpoint's circuit breaker is OPEN, it is also excluded.

If **all endpoints are unhealthy**, the agent falls back to trying all endpoints (to handle cases where health checks are overly aggressive), but with the circuit breaker still respected.

---

### 8. Scoped Agent Distribution

In a large mesh, agents should not hold the entire mesh state in memory. The control plane distributes **only the information each agent needs**.

#### 8.1 What an Agent Needs

An agent needs two categories of information:

1. **Local services** — services that run on its node. The agent needs their configuration, health check settings, and traffic policies so it can probe and manage them.
2. **Upstream services** — services that the agent's local services communicate with (dependencies). The agent needs their endpoints, health status, traffic policies, and load balancing config to route outbound traffic correctly.

Everything else (services on other nodes that have no relationship to the agent's local services) is irrelevant and should **not** be sent to the agent.

#### 8.2 Service Dependency Graph

The control plane maintains a **dependency graph** that tracks which services call which other services. This graph is populated through:

1. **Explicit declaration** — at service registration time, the service (or the administrator) declares its dependencies:

    ```bash
    omnimeshctl service register \
      --name order-service \
      --namespace production \
      --protocol http \
      --endpoints 10.0.1.5:8080 \
      --depends-on payment-service,inventory-service
    ```

2. **Agent self-registration** — agents can report dependencies for locally discovered services via the gRPC API.

```go
// Domain model
type ServiceDependencies struct {
    ServiceID    ServiceID    // The service that has dependencies
    DependsOn    []ServiceID  // Services it calls (upstreams)
}
```

The dependency graph is stored in the system-derived state area:

```
/omnimesh/state/dependencies/{namespace}/{service-name} → ServiceDependencies JSON
```

#### 8.3 Agent Scope Calculation

When an agent connects or when the dependency graph changes, the control plane calculates the agent's **scope** — the set of services and policies that agent needs:

```
Agent's Scope = Local Services ∪ Upstreams(Local Services)
```

```
Control Plane                          Agent (node-1)
     │                                      │
     │   Agent registers with:              │
     │   - node: node-1                     │
     │   - local services:                  │
     │     [order-service, frontend]        │
     │                                      │
     │   Control plane calculates scope:    │
     │   - order-service (local)            │
     │     └─ depends on: payment-service,  │
     │                    inventory-service  │
     │   - frontend (local)                 │
     │     └─ depends on: order-service,    │
     │                    user-service       │
     │                                      │
     │   Agent scope:                       │
     │   {order-service, frontend,          │
     │    payment-service, inventory-service,│
     │    user-service}                     │
     │                                      │
     │   Push ONLY these services' routes,  │
     │   policies, and health to agent      │
     │─────────────────────────────────────►│
     │                                      │
```

#### 8.4 Scoped Distribution Protocol

The existing gRPC bidirectional stream between agent and control plane is extended with a scope-aware protocol:

```
Agent                              Control Plane
  │                                      │
  │  RegisterNode(localServices=[...])   │
  │─────────────────────────────────────►│
  │                                      │
  │        ScopedStateSnapshot(          │
  │          services=[...],             │  ← Initial full sync of scoped state
  │          routes=[...],               │
  │          policies=[...],             │
  │          health=[...])               │
  │◄─────────────────────────────────────│
  │                                      │
  │                           (time passes, things change)
  │                                      │
  │        ScopedStateUpdate(            │
  │          type=ROUTE_CHANGE,          │  ← Incremental updates (only for
  │          service=payment-service,    │     services in agent's scope)
  │          ...)                        │
  │◄─────────────────────────────────────│
  │                                      │
  │  UpdateLocalServices(               │  ← Agent adds/removes a local service
  │    added=[new-service],             │
  │    removed=[old-service])           │
  │─────────────────────────────────────►│
  │                                      │
  │        ScopedStateUpdate(            │  ← Control plane recalculates scope
  │          type=SCOPE_CHANGE,          │     and sends delta
  │          added=[...],               │
  │          removed=[...])             │
  │◄─────────────────────────────────────│
```

#### 8.5 On-Demand Queries

For edge cases where an agent encounters traffic for a service **not in its scope** (e.g., a misconfigured client, or a dependency that wasn't declared), the agent can make an **on-demand query** to the control plane:

```go
// Agent → Control Plane (gRPC unary call, not on the stream)
ResolveService(ctx context.Context, serviceID ServiceID) (*ServiceRouteInfo, error)
```

On-demand queries are:
- **Cacheable** — the agent caches the result for a configurable TTL (e.g., 60s).
- **Async-updatable** — after the on-demand query, the control plane may add that service to the agent's scope for future updates.
- **Rate-limited** — to prevent abuse, on-demand queries are rate-limited (e.g., 10 queries/sec per agent).

This ensures the agent can always function even with incomplete dependency declarations, while keeping the steady-state memory footprint minimal.

#### 8.6 Memory Impact

With scoped distribution, an agent hosting 5 services, each with 3 dependencies, needs state for at most ~20 services (5 local + 15 upstreams, minus overlaps). In a 10,000-service mesh, this is **0.2%** of the total mesh state — a dramatic reduction.

---

### 9. Hexagonal Architecture Mapping

#### Domain Layer (`internal/domain/model/`)

New domain types:

- `ServiceProtocol` — enum for TCP, HTTP, gRPC.
- `TrafficPolicy` — aggregate root for traffic configuration (retry + CB + rate limit + LB).
- `RetryPolicy` — retry parameters.
- `CircuitBreakerPolicy` — circuit breaker parameters.
- `RateLimitPolicy` — rate limiting parameters (token bucket + concurrency).
- `LoadBalancerPolicy` — load balancing algorithm selection.
- `HealthCheck` — active health check configuration per service.
- `ServiceEndpoint` — extended with `Weight` and `Health` fields.
- `EndpointHealth` — health report from an agent for a specific endpoint.
- `ServiceDependencies` — dependency list for scoped distribution.

New domain errors:

- `ErrPolicyNotFound` — traffic policy does not exist.
- `ErrPolicyConflict` — a policy already exists for the target service.
- `ErrInvalidRetryConfig` — invalid retry configuration (e.g., negative max retries).
- `ErrInvalidCBConfig` — invalid circuit breaker configuration.
- `ErrInvalidRateLimitConfig` — invalid rate limit configuration.
- `ErrProtocolMismatch` — policy feature incompatible with service protocol (e.g., HTTP retry on TCP service).

New domain service:

- `PolicyValidator` — validates that a traffic policy is compatible with the target service's protocol. Rejects L7-only features on TCP services.

#### Ports (`internal/port/`)

**Inbound (use cases):**

```go
// TrafficPolicyManager defines inbound use cases for traffic policy management.
type TrafficPolicyManager interface {
    CreateTrafficPolicy(ctx context.Context, policy *domain.TrafficPolicy) error
    UpdateTrafficPolicy(ctx context.Context, policy *domain.TrafficPolicy) error
    GetTrafficPolicy(ctx context.Context, namespace, name string) (*domain.TrafficPolicy, error)
    DeleteTrafficPolicy(ctx context.Context, namespace, name string) error
    ListTrafficPolicies(ctx context.Context, namespace string) ([]*domain.TrafficPolicy, error)
    GetPolicyForService(ctx context.Context, serviceID domain.ServiceID) (*domain.TrafficPolicy, error)
}

// ServiceHealthReporter defines the inbound use case for agents reporting health.
type ServiceHealthReporter interface {
    ReportEndpointHealth(ctx context.Context, report *domain.EndpointHealth) error
}

// AgentScopeManager defines the inbound use case for managing agent scopes.
type AgentScopeManager interface {
    // GetAgentScope returns the full set of services, routes, policies, and health
    // data that the given agent needs.
    GetAgentScope(ctx context.Context, nodeID string) (*domain.AgentScope, error)
    // ResolveService handles on-demand queries from agents for services not in scope.
    ResolveService(ctx context.Context, serviceID domain.ServiceID) (*domain.ServiceRouteInfo, error)
}
```

**Outbound (infrastructure contracts):**

```go
// TrafficPolicyStore persists and queries traffic policies.
type TrafficPolicyStore interface {
    PutTrafficPolicy(ctx context.Context, policy *domain.TrafficPolicy) error
    GetTrafficPolicy(ctx context.Context, namespace, name string) (*domain.TrafficPolicy, error)
    GetPolicyByService(ctx context.Context, serviceID domain.ServiceID) (*domain.TrafficPolicy, error)
    ListTrafficPolicies(ctx context.Context, namespace string) ([]*domain.TrafficPolicy, error)
    DeleteTrafficPolicy(ctx context.Context, namespace, name string) error
    WatchTrafficPolicies(ctx context.Context) (<-chan TrafficPolicyEvent, error)
}

// EndpointHealthStore persists and queries endpoint health status.
type EndpointHealthStore interface {
    PutEndpointHealth(ctx context.Context, health *domain.EndpointHealth) error
    GetEndpointHealth(ctx context.Context, serviceID domain.ServiceID, addr string) (*domain.EndpointHealth, error)
    ListEndpointHealth(ctx context.Context, serviceID domain.ServiceID) ([]*domain.EndpointHealth, error)
    WatchEndpointHealth(ctx context.Context) (<-chan EndpointHealthEvent, error)
}

// DependencyStore persists and queries service dependencies.
type DependencyStore interface {
    PutDependencies(ctx context.Context, deps *domain.ServiceDependencies) error
    GetDependencies(ctx context.Context, serviceID domain.ServiceID) (*domain.ServiceDependencies, error)
    WatchDependencies(ctx context.Context) (<-chan DependencyEvent, error)
}

// ScopedDistributor pushes scoped state to agents.
type ScopedDistributor interface {
    PushScopeSnapshot(ctx context.Context, nodeID string, scope *domain.AgentScope) error
    PushScopeUpdate(ctx context.Context, nodeID string, update *domain.ScopeUpdate) error
}
```

> **Note**: `TrafficPolicyStore`, `EndpointHealthStore`, and `DependencyStore` can be implemented as part of the existing `EtcdStateStore` adapter (extending the `StateStore` interface from RFC-0002) or as separate adapters. The port interfaces remain independent.

#### Application Layer (`internal/application/`)

- `TrafficPolicyService` — orchestrates policy CRUD: validates policy → validates protocol compatibility → checks target service exists → persists → triggers scoped distribution.
- `HealthService` — processes health reports from agents: validates → persists → triggers route recalculation → triggers scoped updates to affected agents.
- `AgentScopeService` — manages agent scopes: calculates scope from dependency graph → maintains scope per agent → pushes deltas on changes.

#### Adapters

**Inbound:**

- `grpc` — extends the existing gRPC server with:
  - `CreateTrafficPolicy` / `UpdateTrafficPolicy` / `DeleteTrafficPolicy` / `ListTrafficPolicies` RPCs.
  - `ReportServiceHealth` RPC (called by agents).
  - `ResolveService` RPC (on-demand agent queries).
  - Scoped state streaming on the bidirectional agent stream.

**Outbound:**

- `etcdstore` — extends the existing etcd adapter with:
  - Traffic policy storage under `/omnimesh/resources/traffic.omnimesh.io/v1/trafficpolicies/` (via generic `ResourceStore`).
  - Endpoint health storage under `/omnimesh/state/health/`.
  - Dependency storage under `/omnimesh/state/dependencies/`.
  - Watch on policy, health, and dependency keys.

---

### 10. CLI Extensions

```bash
# Declarative — apply a TrafficPolicy resource (preferred for production/GitOps)
omnimeshctl apply -f traffic-policy.yaml

# Apply from stdin
omnimeshctl apply -f - <<EOF
apiVersion: traffic.omnimesh.io/v1
kind: TrafficPolicy
metadata:
  name: my-api-policy
  namespace: production
spec:
  targetService: my-api
  retry:
    maxRetries: 3
    retryOn: [connection-failure, 5xx]
    backoffBase: 100ms
    backoffMax: 5s
  circuitBreaker:
    failureThreshold: 5
    failureWindow: 30s
    halfOpenAfter: 15s
  rateLimiter:
    requestsPerSecond: 100
    burstSize: 150
    maxConcurrentRequests: 50
    limitExceededAction: reject
  loadBalancer:
    algorithm: weighted-round-robin
EOF

# Imperative convenience wrappers (construct a resource internally, call apply)
omnimeshctl policy create \
  --name my-api-policy \
  --namespace production \
  --target-service my-api \
  --retry-max 3 \
  --retry-on connection-failure,5xx \
  --cb-failure-threshold 5 \
  --rl-requests-per-second 100 \
  --lb-algorithm weighted-round-robin

# Get / delete / list policies
omnimeshctl get trafficpolicy my-api-policy -n production
omnimeshctl get trafficpolicy my-api-policy -n production -o yaml
omnimeshctl delete trafficpolicy my-api-policy -n production
omnimeshctl get trafficpolicies -n production

# View service health
omnimeshctl service health --name my-api --namespace production
```

---

### 11. Configuration Defaults

```yaml
# Default traffic policy applied when no explicit policy exists
traffic_policy:
  defaults:
    retry:
      max_retries: 0              # No retries by default
    circuit_breaker:
      enabled: false               # No circuit breaking by default
    rate_limiter:
      enabled: false               # No rate limiting by default
    load_balancer:
      algorithm: "weighted-round-robin"

  # Agent-side health check defaults
  health_check:
    default_protocol: "tcp"        # Lowest common denominator
    interval_sec: 10
    timeout_sec: 3
    healthy_after: 2
    unhealthy_after: 3

  # Scoped distribution
  agent_distribution:
    on_demand_query_ttl_sec: 60    # Cache TTL for on-demand service queries
    on_demand_query_rate: 10       # Max on-demand queries per second per agent
```

---

## Trade-offs and Drawbacks

| Concern | Discussion |
| --- | --- |
| **Agent complexity** | Agents must implement retry loops, circuit breaker state machines, token buckets, LB algorithms, and health probing. Mitigated by clear domain model and shared libraries. |
| **Protocol declaration required** | Services must declare their protocol. This is a small burden, but is necessary so the agent knows what level of control is possible. Default is TCP (safest/lowest common denominator). |
| **Local rate limiting only** | Per-agent rate limits don't enforce a global rate. A service behind 5 agents with a 100 req/s limit can receive up to 500 req/s total. Global rate limiting is deferred. |
| **Dependency graph maintenance** | Dependencies must be declared explicitly. Undeclared dependencies mean on-demand queries (slower, cached). Future work could infer dependencies from observed traffic. |
| **Circuit breaker per-agent state** | Circuit breaker state is local to each agent. One agent may have an open circuit for an endpoint while another does not. This is intentional — it avoids distributed state overhead and still provides per-agent protection. Cluster-wide protection comes from health reporting. |
| **On-demand query overhead** | On-demand queries to the control plane add latency for the first request to an unknown service. Mitigated by caching and automatic scope expansion. |
| **Endpoint weight propagation** | Weight changes require route redistribution. In a large mesh, frequent weight changes could cause update storms. Mitigated by batching updates, debouncing, and scoped distribution (only affected agents receive updates). |

---

## Future Work

- **RFC-0004**: Authorization policies — service-to-service access control (can also feed the dependency graph).
- **RFC-0005**: Observability — metrics for retry rates, circuit breaker state, rate limit rejections, LB distribution.
- **RFC-0007**: Waypoint proxies — L7 traffic management (header routing, request manipulation).
- **Global rate limiting** — distributed rate counters via etcd or a dedicated rate limit service.
- **Traffic-inferred dependencies** — automatically detect dependencies by observing what services an agent's local services actually call, and update the dependency graph.
- **Outlier detection** — automatic weight adjustment based on endpoint performance (latency P99, error rate).
- **Zone-aware load balancing** — prefer endpoints in the same zone/region.
- **Canary deployments** — gradual traffic shifting via weight manipulation.

---

## References

- [RFC-0001: Omnimesh Control Plane — Initial Architecture](./rfc-0001-initial-design.md)
- [RFC-0002: High Availability & State Storage — Embedded etcd](./rfc-0002-high-availability.md)
- [Circuit Breaker Pattern — Microsoft](https://learn.microsoft.com/en-us/azure/architecture/patterns/circuit-breaker)
- [Token Bucket Algorithm — Wikipedia](https://en.wikipedia.org/wiki/Token_bucket)
- [Envoy Retry Policy](https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/http/http_routing#retry-semantics)
- [Envoy Circuit Breaking](https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/upstream/circuit_breaking)
- [Envoy Rate Limiting](https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/other_features/global_rate_limiting)
- [Smooth Weighted Round-Robin Algorithm (NGINX)](https://github.com/phusion/nginx/commit/27e94984486058d73157038f7950a0a36ecc6e35)
- [gRPC Health Checking Protocol](https://github.com/grpc/grpc/blob/master/doc/health-checking.md)
