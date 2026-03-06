# Omnimesh RFC Review & Design Improvements

This document reviews RFC-0001 through RFC-0005 and highlights concrete design flaws, edge cases, and architectural bottlenecks that should be addressed in the current design.

## 1. State Management & High Availability (RFC-0002)

### Flaw: Agent Heartbeats Overloading etcd
**Issue:** RFC-0002 proposes using etcd leases for agent liveness, where every agent sends a periodic heartbeat (e.g., every 10–30s) to keep its lease alive. In a large-scale deployment (e.g., 5,000 to 10,000 nodes), this results in thousands of lease renewals per second. etcd is highly optimized for read-heavy workloads and consistent state, but it is **not** designed to handle high-frequency, continuous write throughput (lease KeepAlives require consensus). This will severely degrade the control plane's performance.
**Suggestion:** Decouple transient agent liveness from etcd's persistent store.
* Use an in-memory tracking mechanism in the control plane for connected agents.
* Liveness can be maintained via the persistent gRPC stream between the agent and the control plane.
* Only write to etcd when a node registers, gracefully shuts down, or when the control plane marks it as permanently lost after a timeout.

## 2. Data Plane & Traffic Policies (RFC-0003)

### Flaw: On-Demand Route Resolution on the Critical Path
**Issue:** RFC-0003 introduces "On-Demand Queries" for scoped distribution, where an agent pauses an initial request to a destination it doesn't know and synchronously queries the control plane via `ResolveService()`. This puts the control plane on the **critical path of the data plane**. If the control plane is slow or unavailable, the data plane drops traffic. It also introduces a DoS vector where a compromised service could query random IPs, forcing the agent to spam the control plane.
**Suggestion:** Remove synchronous on-demand queries from the data path.
* With RFC-0004, authorization policies dictate dependencies. If there is an ALLOW policy, the route should already be pre-fetched to the agent.
* If a route is not present, the agent should immediately drop the connection (fail-fast) based on the default-deny posture.
* If dynamic discovery is strictly required, the agent should queue a background asynchronous request to the CP and let the client's TCP retransmission handle the delay, rather than holding the connection open in the proxy.

### Flaw: Local-Only Circuit Breaking
**Issue:** Circuit breakers are local to each agent. If a backend endpoint begins to fail, every single agent in the mesh must independently experience `FailureThreshold` failures before tripping its local circuit. For a service with 100 callers, that means 100 * 5 = 500 failed requests before the endpoint is isolated.
**Suggestion:** Implement **Global Outlier Detection**. Agents should report partial failure rates to the control plane (or the control plane derives this from health/observability reports), which then actively marks the endpoint as degraded and removes it from the routing tables of all agents simultaneously.

### Flaw: Lack of Graceful Termination
**Issue:** The RFCs rely on active/passive health checks to detect failure. There is no mechanism described for graceful shutdown. When a service instance scales down or restarts, traffic will be sent to it until the agents' health checks or circuit breakers fail.
**Suggestion:** Introduce a control-plane coordinated draining state. The agent must intercept process termination signals (e.g., SIGTERM) or integrate with the orchestrator (like Kubernetes preStop hooks) to proactively notify the control plane to withdraw routes *before* the service actually shuts down.

## 3. Security & Authorization (RFC-0004)

### Flaw: Dependency Graph Explosion from Wildcard Policies
**Issue:** RFC-0004 smartly ties the dependency graph (for scoped route distribution) to Authorization policies. However, if an operator creates a namespace-wide policy (e.g., "Namespace A can talk to Namespace B") or a wildcard policy, the control plane calculates O(N*M) dependencies and pushes all individual service routes to all agents in the source namespace. This completely defeats the memory-saving goals of scoped distribution.
**Suggestion:** Scoped distribution must support aggregate routing rules or dynamic resolution at the DNS layer. Instead of pushing thousands of individual IPs, the control plane should push a wildcard policy, and the agent can resolve specific mesh IPs via a lightweight local DNS lookup to CoreDNS, caching the IP-to-route mappings on demand.

### Flaw: Local IP Spoofing in Ambient Mode
**Issue:** The ambient agent intercepts traffic at the node level. The RFCs state the agent establishes mTLS using the calling service's SVID. However, it does not detail *how* the agent securely attributes an intercepted TCP connection to a specific local workload. If the agent merely looks at the source IP, a malicious local process or pod could spoof its IP to hijack another service's SPIFFE identity.
**Suggestion:** The architecture must specify cryptographic or kernel-level correlation. On Linux, the agent should use eBPF `bpf_get_current_pid_tgid()` or cgroup ID correlation to map the intercepted socket to the exact process/pod, ensuring the workload is genuinely authorized to use the corresponding SVID.

## 4. Observability (RFC-0005)

### Flaw: False Claim Regarding TCP Tracing Blind Spot
**Issue:** RFC-0001 and RFC-0005 claim that HBONE "eliminates the traditional TCP tracing blind spot entirely" because trace context is passed in the HTTP/2 tunnel headers. This is partially incorrect. While the *network hop* between agents is traced, the target application (if unmodified and speaking raw TCP) still receives no trace context. When that target application makes a subsequent outbound call, the egress agent starts a brand new trace because it has no way to correlate the inbound TCP connection to the outbound one. The distributed trace remains broken.
**Suggestion:** Correct the design assumptions. To correlate inbound and outbound TCP connections for an unmodified application, the system would need complex heuristics (e.g., eBPF tracking of thread socket operations), which is highly error-prone. The RFC should explicitly state that HBONE provides hop-level observability for TCP, but end-to-end tracing still requires L7 protocols (HTTP/gRPC) or application-level instrumentation.

### Flaw: High Cardinality in Request Metrics
**Issue:** `omnimesh.agent.requests.total` includes both `source_service` and `destination_service`. In a mesh with N services that call each other, this creates O(N^2) time series per agent, leading to cardinality explosion in Prometheus.
**Suggestion:** By default, agent metrics should only emit `destination_service` for egress traffic and `source_service` for ingress traffic. If full edge graphs are needed, they should be derived from OpenTelemetry traces or centrally aggregated access logs, rather than raw Prometheus metrics from every agent.
