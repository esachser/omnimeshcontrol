# RFC Implementation Status Relatory

Based on the analysis of the RFCs and the current codebase (`internal/domain/model`, `internal/application`, `internal/adapter`, etc.), here is a relatory of what is implemented, what is missing, and what differs.

## RFC-0001: Omnimesh Control Plane — Initial Architecture
*   **Status:** Partially Implemented
*   **Implemented:**
    *   Declarative resource model (`Resource`, `ResourceMetadata`, `Service`, `Node`, `Namespace` in `internal/domain/model`).
    *   Core structs for `MeshRoute`, `NodeEndpoint`.
    *   Resource mappers (`internal/application/resource_mapper.go`).
*   **Missing/Different:**
    *   The Ambient Agent implementation (data plane) is entirely missing from the control plane repository (which is expected, as this repo seems to be just the control plane, but worth noting).
    *   `omnimeshctl` and `omnimeshcontrol` are present as directories but their completeness is unclear from the domain/app layer alone.

## RFC-0002: High Availability & State Storage — Embedded etcd
*   **Status:** Mostly Implemented
*   **Implemented:**
    *   Embedded etcd usage (`EtcdStateStore`, `EtcdResourceStore` in `internal/adapter/outbound/etcdstore`).
    *   Atomic IP Allocation (`AllocateIP`, `ReleaseIP`, `popFreeIP`).
    *   Node Liveness Tracker (`NodeLivenessTracker` in `node_tracker.go`).
    *   CoreDNS integration (`EtcdDNSRecordManager` in `internal/adapter/outbound/etcddns`).
*   **Missing/Different:**
    *   The implementation matches the RFC closely.

## RFC-0003: Traffic Policies — Retries, Circuit Breaking, Rate Limiting, Health Detection & Load Balancing
*   **Status:** Partially Implemented
*   **Implemented:**
    *   Domain models (`TrafficPolicy`, `RetryPolicy`, `CircuitBreakerPolicy`, `RateLimitPolicy`, `LoadBalancerPolicy`, `OutlierDetectionPolicy`).
    *   `ServiceProtocol` enum (`TCP`, `HTTP`, `GRPC`).
    *   Endpoint health models (`EndpointHealthStatus`).
    *   `TrafficPolicyStore` and `EndpointHealthStore` (etcd adapters).
*   **Missing/Different:**
    *   **Health Detection:** While the models and store exist, the actual active health probing logic is an agent concern, so it's not here. The control plane's aggregation of health reports for Global Outlier Detection does not seem fully implemented in the application layer (`HealthAppService` might just store the status, missing the aggregation logic described in the RFC).
    *   **Graceful Termination (Draining):** `DrainService` is defined in `HealthAppService` but it's unclear if it fully handles route withdrawal.

## RFC-0004: Authorization Policies — SPIFFE Identity-Based Access Control
*   **Status:** Partially Implemented
*   **Implemented:**
    *   Domain models (`AuthorizationPolicy`, `PolicyAction`, `PolicySelector`, `ServiceDependency`, `DependencyOrigin`).
    *   Authorization mappers and application service (`authz_app.go`, `authz_mapper.go`).
    *   `DependencyStore` (etcd adapter).
*   **Missing/Different:**
    *   **Temporal Restrictions:** The `AuthorizationPolicy` model in `internal/domain/model/authz.go` is **missing** the `ValidFrom` and `ValidUntil` fields defined in the RFC. The garbage collection/expiration logic is therefore also missing.
    *   **L7 Rules (`AuthzRule`):** The `AuthorizationPolicy` model is **missing** the `Rules` field containing `HTTPAuthzRule` and `GRPCAuthzRule` defined in the RFC. It currently only has `Action`, `Source`, and `Destination`.
    *   **Peer/Target structs differ:** The RFC specifies `AuthzPeer` and `AuthzTarget`. The implementation uses a simplified `PolicySelector`.
    *   **AuthzEvaluator:** The pure domain service for evaluating requests (`AuthzEvaluator`) is missing.
    *   **IdentityManager (SPIRE):** The actual SPIRE adapter seems missing (only a mock `mockIdentityManager` exists in tests).

## RFC-0005: Observability — OpenTelemetry Metrics, Traces & Logs
*   **Status:** Not Implemented
*   **Implemented:**
    *   None.
*   **Missing/Different:**
    *   The entire OpenTelemetry pipeline (`TelemetryConfig`, `SpanContext`, `MetricsRecorder`, `TraceExporter`, `LogExporter`, `TelemetryService`, and the adapters `otelmetrics`, `oteltracing`, `otellogs`) is missing.
    *   There is no telemetry instrumentation in the domain or application layers.

## RFC-0006: Multi-Network Routing and Locality
*   **Status:** Partially Implemented
*   **Implemented:**
    *   Domain models (`NetworkTarget`, `TargetRoute`, `TrafficRoute`, `TrafficRouteDestination`).
    *   `Node` model extended with `Target`.
    *   Application services (`target_app.go`, `route_app.go`).
*   **Missing/Different:**
    *   **Failover Policy:** The `TargetRoute` model is **missing** the `failover` configuration (`activeWhenTargetIsDown`) defined in the RFC.
    *   **Weight Aggregation:** While `TargetRoute` has a `Weight` field, the control plane logic to aggregate these weights and push probabilities relative to the requesting agent's target (as described in "How the Control Plane Aggregates Routes") needs verification, as it seems to just store the routes currently.
    *   **Target-Level Failure in Outlier Detection:** The `OutlierDetectionPolicy` does not seem to account for `SourceTarget -> DestinationTarget -> Endpoint -> HealthState` yet, it still seems global.
