# RFC-0005: Observability — OpenTelemetry Metrics, Traces & Logs

Status: **Draft**

## Problem Statement

RFCs 0001–0004 establish the control plane, state storage, traffic policies, and authorization — but operators have **no visibility** into what the mesh is doing. Without observability:

- Traffic failures silently retry and eventually circuit-break, with no one alerted.
- Authorization denials are invisible unless someone checks agent logs.
- Rate limiting rejections are unreported — callers see errors but operators don't know why.
- Capacity planning is guesswork — there is no data on request volume, latency distribution, or resource usage.
- Debugging cross-service issues requires manually correlating logs across multiple agents.

**This RFC addresses:**

1. **Metrics** — real-time counters, gauges, and histograms for both the control plane and ambient agents.
2. **Distributed tracing** — request-level traces that follow traffic across mesh services, with context propagation through agents.
3. **Structured logging** — consistent, machine-parseable log format across all components.
4. **Telemetry export** — OpenTelemetry-native pipeline for metrics, traces, and logs.

---

## Design Principles

1. **OpenTelemetry native** — all telemetry uses the OpenTelemetry SDK. No proprietary instrumentation. This gives operators free choice of backends (Prometheus, Grafana, Jaeger, Datadog, Elastic, etc.).
2. **Low overhead by default** — instrumentation must not noticeably impact request latency or agent memory. High-cardinality dimensions are avoided; sampling controls trace volume.
3. **Agent-centric** — the ambient agent is the primary telemetry source. It sees every request and can emit per-request metrics and spans. The control plane emits operational metrics.
4. **Scoped exposure** — each component exposes a Prometheus-compatible `/metrics` endpoint. Agents expose agent-level metrics; the control plane exposes control-plane metrics.
5. **Correlation** — traces, metrics, and logs share common attributes (service name, namespace, node ID) so they can be correlated in backends.

---

## Architecture

### 1. Telemetry Pipeline

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          Omnimesh Components                                │
│                                                                             │
│  ┌──────────────┐     ┌──────────────┐     ┌──────────────┐                │
│  │   Agent       │     │   Agent       │     │  Control     │                │
│  │  (node-1)     │     │  (node-2)     │     │  Plane       │                │
│  │               │     │               │     │              │                │
│  │ OTel SDK      │     │ OTel SDK      │     │ OTel SDK     │                │
│  │ ┌─────────┐  │     │ ┌─────────┐  │     │ ┌─────────┐ │                │
│  │ │Metrics  │  │     │ │Metrics  │  │     │ │Metrics  │ │                │
│  │ │Traces   │  │     │ │Traces   │  │     │ │Traces   │ │                │
│  │ │Logs     │  │     │ │Logs     │  │     │ │Logs     │ │                │
│  │ └────┬────┘  │     │ └────┬────┘  │     │ └────┬────┘ │                │
│  └──────┼───────┘     └──────┼───────┘     └──────┼──────┘                │
│         │                    │                    │                         │
└─────────┼────────────────────┼────────────────────┼─────────────────────────┘
          │                    │                    │
          │   OTLP / Prometheus scrape              │
          ▼                    ▼                    ▼
   ┌──────────────────────────────────────────────────────┐
   │              OpenTelemetry Collector                  │
   │              (optional — can export directly)        │
   └───────┬──────────────┬──────────────┬────────────────┘
           │              │              │
      ┌────▼────┐   ┌─────▼────┐   ┌────▼─────┐
      │Prometheus│   │  Jaeger  │   │  Loki /  │
      │/ Grafana │   │/ Tempo   │   │  Elastic │
      └─────────┘   └──────────┘   └──────────┘
       Metrics        Traces          Logs
```

### 2. Export Modes

| Mode | Description | Use Case |
| --- | --- | --- |
| **Pull (Prometheus)** | Each component exposes `/metrics` in Prometheus exposition format | Standard Prometheus + Grafana stack |
| **Push (OTLP gRPC)** | Components push telemetry to an OTel Collector via OTLP | Centralized collection, complex pipelines |
| **Push (OTLP HTTP)** | Same as OTLP gRPC but over HTTP/JSON | Environments where gRPC is not available |

Multiple exporters can be active simultaneously (e.g., Prometheus `/metrics` for scraping **and** OTLP push for traces).

### 3. Configuration

```yaml
observability:
  metrics:
    enabled: true
    endpoint: ":9090"                    # Prometheus /metrics endpoint
    exporters:
      - type: prometheus                 # Default — exposes /metrics
      - type: otlp-grpc                  # Optional — push to OTel Collector
        target: "otel-collector:4317"

  traces:
    enabled: true
    sampler: "parentbased_traceidratio"
    sample_rate: 0.01                    # 1% of requests sampled (adjustable)
    exporters:
      - type: otlp-grpc
        target: "otel-collector:4317"

  logs:
    enabled: true
    level: "info"                        # debug, info, warn, error
    format: "json"                       # json or text
    exporters:
      - type: stdout                     # Default
      - type: otlp-grpc                  # Optional — structured log export
        target: "otel-collector:4317"

  # Common resource attributes added to all telemetry
  resource:
    service.name: "omnimesh-control-plane"  # or "omnimesh-agent"
    service.namespace: "omnimesh"
    deployment.environment: "production"
```

---

## Metrics

All metrics use the `omnimesh.` prefix. Dimensions (labels/attributes) follow OpenTelemetry semantic conventions where applicable.

### 4. Control Plane Metrics

#### 4.1 Resource Management

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.cp.resources.total` | Gauge | Current count of resources by kind | `kind`, `namespace`, `api_group` |
| `omnimesh.cp.resources.apply.total` | Counter | Total Apply operations processed | `kind`, `namespace`, `result` (success/error) |
| `omnimesh.cp.resources.apply.duration_ms` | Histogram | Apply operation latency | `kind` |
| `omnimesh.cp.resources.delete.total` | Counter | Total Delete operations | `kind`, `namespace`, `result` |
| `omnimesh.cp.resources.validation.errors` | Counter | Validation failures by kind and error type | `kind`, `error_type` (structural/semantic) |

#### 4.2 IPAM

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.cp.ipam.allocated` | Gauge | Number of currently allocated IPs | — |
| `omnimesh.cp.ipam.capacity` | Gauge | Total IPs available in the configured CIDR | — |
| `omnimesh.cp.ipam.utilization_ratio` | Gauge | `allocated / capacity` (0.0–1.0) | — |
| `omnimesh.cp.ipam.allocation.errors` | Counter | IP allocation failures (exhaustion, conflict) | `error_type` |

#### 4.3 Node Management

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.cp.nodes.connected` | Gauge | Currently connected agent nodes | `platform` (linux/windows/macos/k8s) |
| `omnimesh.cp.nodes.heartbeat.total` | Counter | Total heartbeats received | `node_id` |
| `omnimesh.cp.nodes.heartbeat.missed` | Counter | Heartbeats not received within expected window | `node_id` |
| `omnimesh.cp.nodes.disconnections` | Counter | Node disconnections (lease expired) | `node_id`, `platform` |

#### 4.4 etcd

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.cp.etcd.operations.total` | Counter | etcd operations by type | `operation` (put/get/delete/txn/watch) |
| `omnimesh.cp.etcd.operations.duration_ms` | Histogram | etcd operation latency | `operation` |
| `omnimesh.cp.etcd.operations.errors` | Counter | etcd error count | `operation`, `error_type` |
| `omnimesh.cp.etcd.leader` | Gauge | Whether this instance is the etcd leader (1/0) | — |
| `omnimesh.cp.etcd.db_size_bytes` | Gauge | etcd database size | — |
| `omnimesh.cp.etcd.peers.connected` | Gauge | Number of connected etcd peers | — |

#### 4.5 Identity (SPIRE)

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.cp.identity.registrations.total` | Counter | SPIRE workload registrations | `namespace`, `result` |
| `omnimesh.cp.identity.registrations.duration_ms` | Histogram | Registration latency | — |
| `omnimesh.cp.identity.deregistrations.total` | Counter | SPIRE workload deregistrations | `namespace`, `result` |

#### 4.6 DNS

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.cp.dns.upserts.total` | Counter | DNS record upserts to CoreDNS | `result` |
| `omnimesh.cp.dns.deletes.total` | Counter | DNS record deletions | `result` |
| `omnimesh.cp.dns.upserts.duration_ms` | Histogram | DNS upsert latency | — |

#### 4.7 Route Distribution

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.cp.routes.announcements.total` | Counter | Routes announced via distributors | `transport` (bgp/grpc/nats) |
| `omnimesh.cp.routes.withdrawals.total` | Counter | Routes withdrawn | `transport` |
| `omnimesh.cp.routes.distribution.duration_ms` | Histogram | Time to distribute a route update to all agents | — |

#### 4.8 Scoped Distribution

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.cp.scope.snapshots.total` | Counter | Full scope snapshots sent to agents | `node_id` |
| `omnimesh.cp.scope.updates.total` | Counter | Incremental scope updates | `node_id`, `update_type` |
| `omnimesh.cp.scope.ondemand.total` | Counter | On-demand service resolution queries | `node_id`, `result` |
| `omnimesh.cp.scope.services_per_agent` | Histogram | Number of services in each agent's scope | — |

#### 4.9 gRPC Server

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.cp.grpc.requests.total` | Counter | Incoming gRPC requests | `method`, `status_code` |
| `omnimesh.cp.grpc.requests.duration_ms` | Histogram | gRPC request latency | `method` |
| `omnimesh.cp.grpc.streams.active` | Gauge | Currently active gRPC streams (agent connections) | — |

---

### 5. Agent Metrics (Data Plane)

These are the most critical metrics — they represent the actual traffic flowing through the mesh.

#### 5.1 Request Traffic (Core)

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.agent.requests.total` | Counter | Total requests proxied | `source_service`, `source_namespace`, `destination_service`, `destination_namespace`, `protocol`, `response_code` |
| `omnimesh.agent.requests.duration_ms` | Histogram | End-to-end request latency (agent perspective) | `source_service`, `destination_service`, `protocol` |
| `omnimesh.agent.requests.active` | Gauge | Currently in-flight requests | `destination_service`, `destination_namespace` |
| `omnimesh.agent.requests.bytes_sent` | Counter | Total bytes sent (request direction) | `source_service`, `destination_service` |
| `omnimesh.agent.requests.bytes_received` | Counter | Total bytes received (response direction) | `source_service`, `destination_service` |

> **Cardinality note**: The `source_service` × `destination_service` × `response_code` combination can be high-cardinality. In large meshes, agents can be configured to drop `source_service` from request metrics and only keep it on traces.

#### 5.2 Connection & HBONE Tunnel Metrics

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.agent.connections.active` | Gauge | Active TCP connections | `destination_service`, `destination_namespace` |
| `omnimesh.agent.connections.total` | Counter | Total connections opened | `destination_service`, `result` (success/refused/timeout) |
| `omnimesh.agent.connections.duration_ms` | Histogram | Connection lifetime | `destination_service` |
| `omnimesh.agent.hbone.tunnels.active` | Gauge | Active HBONE HTTP/2 tunnels to peer agents | `peer_node` |
| `omnimesh.agent.hbone.streams.active` | Gauge | Active HTTP/2 streams (multiplexed connections) across all tunnels | `peer_node` |
| `omnimesh.agent.hbone.streams.total` | Counter | Total HBONE streams opened | `peer_node`, `destination_service`, `result` |
| `omnimesh.agent.hbone.bytes_sent` | Counter | Bytes sent through HBONE tunnels | `peer_node` |
| `omnimesh.agent.hbone.bytes_received` | Counter | Bytes received through HBONE tunnels | `peer_node` |

#### 5.3 mTLS

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.agent.mtls.handshakes.total` | Counter | mTLS handshakes | `peer_service`, `result` (success/failure) |
| `omnimesh.agent.mtls.handshakes.duration_ms` | Histogram | Handshake latency | — |
| `omnimesh.agent.mtls.svid.expiry_seconds` | Gauge | Seconds until the agent's SVID expires | `spiffe_id` |
| `omnimesh.agent.mtls.svid.rotations.total` | Counter | SVID rotations (renewals) | — |

#### 5.4 Retries

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.agent.retries.total` | Counter | Total retry attempts | `destination_service`, `retry_reason` (connection_failure/5xx/reset) |
| `omnimesh.agent.retries.exhausted` | Counter | Requests where all retries were exhausted | `destination_service` |
| `omnimesh.agent.retries.success` | Counter | Requests that succeeded on a retry (not the first attempt) | `destination_service` |

#### 5.5 Circuit Breaker

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.agent.circuit_breaker.state` | Gauge | Current CB state per endpoint (0=closed, 1=half-open, 2=open) | `destination_service`, `endpoint` |
| `omnimesh.agent.circuit_breaker.transitions` | Counter | State transitions | `destination_service`, `endpoint`, `from_state`, `to_state` |
| `omnimesh.agent.circuit_breaker.rejected` | Counter | Requests rejected by an open circuit | `destination_service`, `endpoint` |
| `omnimesh.agent.circuit_breaker.half_open.success` | Counter | Half-open probe requests that succeeded | `destination_service`, `endpoint` |
| `omnimesh.agent.circuit_breaker.half_open.failure` | Counter | Half-open probe requests that failed | `destination_service`, `endpoint` |

#### 5.6 Rate Limiting

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.agent.rate_limiter.requests.allowed` | Counter | Requests allowed by the rate limiter | `destination_service` |
| `omnimesh.agent.rate_limiter.requests.rejected` | Counter | Requests rejected (rate exceeded) | `destination_service`, `limit_type` (token_bucket/concurrency) |
| `omnimesh.agent.rate_limiter.requests.queued` | Counter | Requests queued (when `limitExceededAction: queue`) | `destination_service` |
| `omnimesh.agent.rate_limiter.tokens.available` | Gauge | Current available tokens in the bucket | `destination_service` |
| `omnimesh.agent.rate_limiter.concurrent.active` | Gauge | Current concurrent requests (for concurrency limiter) | `destination_service` |

#### 5.7 Load Balancing

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.agent.lb.selections.total` | Counter | Endpoint selections by algorithm | `destination_service`, `endpoint`, `algorithm` |
| `omnimesh.agent.lb.endpoints.healthy` | Gauge | Number of healthy endpoints | `destination_service` |
| `omnimesh.agent.lb.endpoints.total` | Gauge | Total endpoints (healthy + unhealthy) | `destination_service` |
| `omnimesh.agent.lb.no_healthy_endpoints` | Counter | Times all endpoints were unhealthy (fallback triggered) | `destination_service` |

#### 5.8 Health Checks

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.agent.health.probes.total` | Counter | Active health check probes sent | `service`, `endpoint`, `protocol`, `result` (success/failure) |
| `omnimesh.agent.health.probes.duration_ms` | Histogram | Health probe latency | `service`, `protocol` |
| `omnimesh.agent.health.transitions` | Counter | Endpoint health state transitions | `service`, `endpoint`, `from_state`, `to_state` |

#### 5.9 Authorization

| Metric | Type | Description | Key Attributes |
| --- | --- | --- | --- |
| `omnimesh.agent.authz.decisions.total` | Counter | Authorization decisions | `source_service`, `destination_service`, `decision` (allow/deny/dry_run) |
| `omnimesh.agent.authz.denials.total` | Counter | Denials by reason | `source_service`, `destination_service`, `reason` (no_policy/explicit_deny/l7_mismatch) |
| `omnimesh.agent.authz.evaluation.duration_us` | Histogram | Policy evaluation time (microseconds) | — |
| `omnimesh.agent.authz.policies.cached` | Gauge | Number of policies in the agent's cache | — |

---

### 6. Distributed Tracing

The ambient agent injects and propagates trace context for every proxied request. This provides end-to-end visibility into request flows across multiple services.

#### 6.1 Trace Context Propagation

Since all agent-to-agent traffic uses **HBONE HTTP/2 tunnels** (RFC-0001 §3.1), trace context is **always propagated via HTTP/2 headers on the tunnel**, regardless of the inner service protocol. This means:

- **HTTP/gRPC services**: trace context is propagated both in the HBONE tunnel headers and in the application-level headers (if the application uses OTel SDK).
- **TCP services**: trace context rides in the HBONE tunnel’s HTTP/2 headers. The agent creates spans with full source/destination context. The inner TCP payload is opaque, but the mesh-level trace is complete.

This eliminates the traditional TCP tracing blind spot found in other service meshes.

```
Service A (TCP)    Agent (node-1)                              Agent (node-2)          Service B (TCP)
    │                    │                                          │                      │
    │  raw TCP ────────►│                                          │                      │
    │                    │ 1. Intercept outbound                    │                      │
    │                    │ 2. Generate trace ID, start span          │                      │
    │                    │ 3. Open HBONE tunnel to node-2            │                      │
    │                    │    (HTTP/2 CONNECT with mTLS)             │                      │
    │                    │    Headers:                               │                      │
    │                    │      traceparent: 00-abc123-def456-01     │                      │
    │                    │      x-omnimesh-source-identity: ...      │                      │
    │                    │      x-omnimesh-destination-service: ...  │                      │
    │                    │────────── HBONE tunnel ────────────────►│                      │
    │                    │                                          │ 4. Extract trace ctx │
    │                    │                                          │    from tunnel headers│
    │                    │                                          │ 5. Start ingress span│
    │                    │                                          │ 6. Run authz check   │
    │                    │                                          │ 7. Forward TCP to B  │
    │                    │                                          │─── raw TCP ──────────►│
    │                    │                                          │◄── response ─────────│
    │                    │◄────────── HBONE response ─────────────│ 8. Finish span       │
    │◄─── response ───│ 9. Finish span                            │                      │
```

#### 6.2 Span Structure

Each proxied request in the mesh generates the following spans:

```
Trace: abc123
│
├── [Agent node-1] omnimesh.egress
│   ├── service: order-service
│   ├── destination: payment-service
│   ├── protocol: http
│   ├── retry.attempt: 0
│   │
│   └── [Agent node-2] omnimesh.ingress
│       ├── service: payment-service
│       ├── source: order-service
│       ├── authz.decision: allow
│       ├── authz.policy: order-to-payment
│       ├── protocol: http
│       │
│       └── [Agent node-2] omnimesh.egress  (if payment-service calls another service)
│           └── ...
```

#### 6.3 Span Attributes

| Attribute | Description | Example |
| --- | --- | --- |
| `omnimesh.source.service` | Calling service name | `order-service` |
| `omnimesh.source.namespace` | Calling service namespace | `production` |
| `omnimesh.destination.service` | Target service name | `payment-service` |
| `omnimesh.destination.namespace` | Target service namespace | `production` |
| `omnimesh.destination.endpoint` | Target endpoint address | `10.0.1.5:8080` |
| `omnimesh.protocol` | Service protocol | `http`, `grpc`, `tcp` |
| `omnimesh.direction` | Agent role | `ingress`, `egress` |
| `omnimesh.authz.decision` | Authorization result | `allow`, `deny` |
| `omnimesh.authz.policy` | Policy that matched | `order-to-payment` |
| `omnimesh.retry.attempt` | Retry attempt number (0 = first try) | `0`, `1`, `2` |
| `omnimesh.circuit_breaker.state` | CB state at time of request | `closed`, `open` |
| `omnimesh.rate_limiter.remaining` | Tokens remaining after this request | `42` |
| `omnimesh.node` | Agent node ID | `node-1` |
| `http.method` | HTTP method (HTTP protocol) | `POST` |
| `http.url` | HTTP URL (HTTP protocol) | `/api/v1/charge` |
| `http.status_code` | HTTP response code | `200` |
| `rpc.system` | RPC system (gRPC protocol) | `grpc` |
| `rpc.service` | gRPC service name | `payment.v1.PaymentService` |
| `rpc.method` | gRPC method name | `Charge` |
| `rpc.grpc.status_code` | gRPC status code | `0` (OK) |

#### 6.4 Sampling

Full trace capture on every request is prohibitive at scale. The default sampler is **parent-based with trace ID ratio**:

- If an incoming request already has a trace context with a sampling decision, respect it.
- If not, sample at the configured rate (default: 1%).
- **Always-on sampling** for errors: if a request results in an error (5xx, gRPC non-OK, connection failure), the trace is always captured regardless of the sampling rate.

```yaml
traces:
  sampler: "parentbased_traceidratio"
  sample_rate: 0.01
  always_sample_errors: true
```

---

### 7. Structured Logging

All Omnimesh components emit structured JSON logs using the OpenTelemetry Logs SDK, ensuring correlation with metrics and traces.

#### 7.1 Log Format

```json
{
  "timestamp": "2026-03-04T22:45:12.345Z",
  "severity": "INFO",
  "body": "Authorization policy applied",
  "resource": {
    "service.name": "omnimesh-agent",
    "service.namespace": "omnimesh",
    "omnimesh.node": "node-1"
  },
  "attributes": {
    "omnimesh.event": "authz.policy.applied",
    "omnimesh.policy.name": "order-to-payment",
    "omnimesh.policy.namespace": "production",
    "omnimesh.policy.action": "allow"
  },
  "traceId": "abc123...",
  "spanId": "def456..."
}
```

#### 7.2 Key Log Events

| Event | Severity | When |
| --- | --- | --- |
| `resource.applied` | INFO | Resource created or updated via apply |
| `resource.deleted` | INFO | Resource deleted |
| `resource.validation.failed` | WARN | Resource failed validation |
| `node.connected` | INFO | Agent node connected |
| `node.disconnected` | WARN | Agent node disconnected (lease expired) |
| `authz.denied` | WARN | Authorization request denied |
| `authz.dry_run.would_deny` | INFO | Dry-run mode: request would be denied |
| `circuit_breaker.opened` | WARN | Circuit breaker transitioned to OPEN |
| `circuit_breaker.closed` | INFO | Circuit breaker recovered to CLOSED |
| `rate_limiter.rejected` | WARN | Request rejected by rate limiter |
| `health.endpoint.unhealthy` | WARN | Endpoint became unhealthy |
| `health.endpoint.healthy` | INFO | Endpoint recovered to healthy |
| `ipam.exhausted` | ERROR | IP address pool exhausted |
| `etcd.leader.changed` | WARN | etcd leadership changed |
| `mtls.svid.expiring_soon` | WARN | SVID expiring within 10% of its TTL |
| `scope.recalculated` | DEBUG | Agent scope recalculated after dependency change |

#### 7.3 Trace-Log Correlation

Every log entry emitted during a traced request includes `traceId` and `spanId`. This allows operators to jump from a log entry to its full trace in the tracing backend, and vice versa.

---

### 8. Hexagonal Architecture Mapping

#### Domain Layer (`internal/domain/model/`)

New domain types:

- `TelemetryConfig` — configuration for metrics, traces, and log export.
- `SpanContext` — trace-id/span-id/flags for propagation through domain logic.

> **Note**: The domain layer itself does not depend on OpenTelemetry. Span propagation uses a domain `SpanContext` type. The OTel SDK conversion happens in the adapter layer.

#### Ports (`internal/port/`)

**Outbound:**

```go
// MetricsRecorder defines the outbound port for recording metrics.
// Domain and application layers call this interface — they never touch the OTel SDK directly.
type MetricsRecorder interface {
    IncrCounter(name string, labels map[string]string)
    SetGauge(name string, value float64, labels map[string]string)
    RecordHistogram(name string, value float64, labels map[string]string)
}

// TraceExporter defines the outbound port for exporting trace spans.
type TraceExporter interface {
    StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, SpanHandle)
    InjectContext(ctx context.Context, carrier PropagationCarrier) error
    ExtractContext(ctx context.Context, carrier PropagationCarrier) context.Context
}

// SpanHandle allows the caller to finish a span and set attributes.
type SpanHandle interface {
    SetAttribute(key, value string)
    SetError(err error)
    End()
}

// LogExporter defines the outbound port for structured log emission.
type LogExporter interface {
    Log(ctx context.Context, severity Severity, event string, attrs map[string]string)
}
```

#### Application Layer (`internal/application/`)

- `TelemetryService` — initializes the OTel SDK, configures exporters, manages shutdown. Wires the `MetricsRecorder`, `TraceExporter`, and `LogExporter` ports.
- Existing application services (`ResourceService`, `TrafficPolicyService`, `AuthorizationService`, `HealthService`) are instrumented by calling `MetricsRecorder` for counters/histograms and `TraceExporter` for spans.

#### Adapters (`internal/adapter/outbound/`)

| Adapter | Port | Description |
| --- | --- | --- |
| `otelmetrics` | `MetricsRecorder` | OpenTelemetry Metrics SDK — Prometheus exporter + optional OTLP pusher |
| `oteltracing` | `TraceExporter` | OpenTelemetry Tracing SDK — OTLP exporter, W3C propagation |
| `otellogs` | `LogExporter` | OpenTelemetry Logs SDK — stdout + optional OTLP exporter |

**Inbound adapter:**

The gRPC inbound adapter and the agent's traffic proxy are the **primary instrumentation points**. They wrap each request in a span, record request metrics, and propagate trace context.

---

### 9. Agent Metrics Endpoint

Each agent exposes an HTTP endpoint for Prometheus scraping:

```
GET http://<agent-ip>:9091/metrics
```

The control plane exposes its own:

```
GET http://<cp-ip>:9090/metrics
```

Both return **Prometheus exposition format** (text-based) by default, with **OpenMetrics** content negotiation supported.

#### 9.1 Service Discovery for Scraping

The control plane's gRPC API provides a **targets endpoint** compatible with Prometheus service discovery:

```
GET http://<cp-ip>:9090/api/v1/targets
```

Returns a JSON list of all agent `/metrics` endpoints, suitable for Prometheus `http_sd_configs`. This ensures Prometheus automatically discovers all agents as they join or leave the mesh.

---

### 10. Cardinality Management

High cardinality (too many unique label combinations) can overwhelm metrics backends. Omnimesh provides controls:

| Control | Description |
| --- | --- |
| **Label dropping** | Configurable per-metric label allow/deny list. E.g., drop `source_service` from `omnimesh.agent.requests.total` in large meshes. |
| **Aggregation** | Agent can aggregate per-endpoint metrics to per-service granularity. |
| **Histogram bucket tuning** | Configurable histogram buckets for latency metrics (default: 1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000 ms). |

```yaml
observability:
  metrics:
    cardinality:
      max_label_cardinality: 1000         # Warn if a metric exceeds this unique label count
      drop_labels:
        omnimesh.agent.requests.total:
          - source_service                 # Drop source_service in high-cardinality environments
      histogram_buckets:
        duration_ms: [1, 5, 10, 25, 50, 100, 250, 500, 1000, 5000]
```

---

## Trade-offs and Drawbacks

| Concern | Discussion |
| --- | --- |
| **Agent memory for per-request metrics** | Each agent maintains counters and histograms for its scoped services. With scoped distribution (~20 services per agent), this is manageable. Label cardinality controls mitigate explosion. |
| **Trace overhead** | Even with sampling, the agent must check trace context on every request. The check is fast (ns-level), and 1% default sampling minimizes export overhead. |
| **OpenTelemetry SDK dependency** | Adds the OTel Go SDK as a dependency. This is a well-maintained, vendor-neutral library with minimal transitive dependencies. |
| **TCP tracing is tunnel-level only** | TCP service traces capture mesh-level spans (egress/ingress) with full metadata via HBONE headers, but cannot inject trace context into the TCP payload itself. For end-to-end in-app tracing, TCP services should use the OTel SDK directly. The mesh-level trace is still complete. |
| **Multiple export formats** | Supporting both Prometheus pull and OTLP push adds configuration complexity. Mitigated by sensible defaults (Prometheus only) and clear documentation. |
| **Error-biased sampling** | Always-sampling errors can cause trace storage spikes during outages. Mitigated by configurable rate caps on error sampling. |

---

## Future Work

- **RFC-0006**: Kubernetes integration — Prometheus `ServiceMonitor` CRDs, Grafana dashboard provisioning, Kubernetes events integration.
- **RFC-0007**: Waypoint proxies — L7-specific tracing (request/response body sampling, header capture).
- **Access logging** — per-request access logs (like Envoy access logs) with configurable fields.
- **Alerting rules** — pre-built Prometheus alerting rules for common failure modes (high error rate, circuit breaker storms, rate limiter saturation, SVID expiry).
- **Grafana dashboards** — pre-built dashboards for the control plane and per-service agent views.
- **eBPF-based metrics** — kernel-level metrics (TCP retransmits, socket buffer utilization) on Linux agents for deeper network visibility.
- **Trace-based testing** — automated canary analysis using trace data to compare latency distributions between versions.

---

## References

- [OpenTelemetry Specification](https://opentelemetry.io/docs/specs/otel/)
- [OpenTelemetry Go SDK](https://github.com/open-telemetry/opentelemetry-go)
- [W3C Trace Context](https://www.w3.org/TR/trace-context/)
- [Prometheus Exposition Format](https://prometheus.io/docs/instrumenting/exposition_formats/)
- [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/)
- [RFC-0001: Omnimesh Control Plane — Initial Architecture](./rfc-0001-initial-design.md)
- [RFC-0002: High Availability & State Storage — Embedded etcd](./rfc-0002-high-availability.md)
- [RFC-0003: Traffic Policies — Retries, Circuit Breaking, Rate Limiting, Health Detection & Load Balancing](./rfc-0003-traffic-policies.md)
- [RFC-0004: Authorization Policies — SPIFFE Identity-Based Access Control](./rfc-0004-authorization.md)
