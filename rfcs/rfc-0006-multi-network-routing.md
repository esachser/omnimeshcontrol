# RFC-0006: Multi-Network Routing and Locality

Status: **Draft**

## Problem Statement

RFC-0001 establishes a flat network model where every agent in the mesh assumes it can reach the internal `grpcAddr` of any other agent. While this works perfectly within a single uniform environment (e.g., a single Kubernetes cluster or a single flat VNet), enterprise environments are rarely this simple. 

A multi-environment architecture introduces **NAT boundaries and non-routable IPs**. For example:
- An Omnimesh agent running on a Windows bare-metal server in a corporate data center cannot route traffic to `10.244.1.5` (a Kubernetes Pod IP) on a cluster in AWS.
- Agents in distinct Kubernetes clusters cannot communicate directly via pod IPs.

To enable **Cross-Network Routing**, the mesh must understand the network topology. It needs to know:
1. Which logical "Target" or "Network" each agent belongs to.
2. How agents *outside* a Target can reach the agents *inside* that Target (via a Gateway/Ingress).
3. How traffic should be distributed or failed-over across multiple Targets that host the same service.

## Proposed Solution: The Target Topology Model

This RFC introduces a decoupled architecture for multi-network routing consisting of three elements:

1. **`NetworkTarget` Resource**: Defines a logical network boundary and its entrypoint (Gateway).
2. **`Node` Extensions**: Agents declare the `NetworkTarget` they belong to upon registration.
3. **`TargetRoute` Resource**: Defines how traffic for a specific service is distributed across different targets.

### 1. `NetworkTarget` (Cluster-Scoped)

A `NetworkTarget` represents a distinct routing domain — an isolated network where internal IPs are routable between nodes within the target, but not from the outside.

```yaml
apiVersion: core.omnimesh.io/v1
kind: NetworkTarget
metadata:
  name: k8s-cluster-us-east
spec:
  # The address (host:port) that external agents must use to establish
  # the HBONE tunnel to reach agents inside this target.
  # This is typically an Ingress controller, LoadBalancer API, or Gateway.
  gatewayAddress: "gateway.us-east.corp.local:8443"
```

* If an agent in `k8s-cluster-us-east` wants to reach another agent in `k8s-cluster-us-east`, it uses the destination agent's direct internal `grpcAddr`.
* If an agent in `bare-metal-dc1` wants to reach an agent in `k8s-cluster-us-east`, the control plane instructs it to connect via `gateway.us-east.corp.local:8443`.

### 2. Node Registration Extension (`core.omnimesh.io/v1`)

The existing `Node` resource is extended so each agent can declare its locality. Any service endpoints discovered by this agent automatically inherit its `NetworkTarget`.

```yaml
apiVersion: core.omnimesh.io/v1
kind: Node
metadata:
  name: web-node-1
spec:
  platform: kubernetes
  target: k8s-cluster-us-east   # NEW: Binds the node to a NetworkTarget
  grpcAddr: "10.0.1.5:8443"     # Internal unroutable IP
```

### 3. `TargetRoute` Resource (`traffic.omnimesh.io/v1`)

To prevent lock points and monolithic configurations, traffic distribution is NOT defined as a single list of targets. Instead, a `TargetRoute` resource is declared **per target** for a given service. 

This means targets can be added, updated, or removed dynamically without editing a central routing resource. The control plane aggregates all `TargetRoute` resources for a specific service to compute the final weight distribution.

```yaml
apiVersion: traffic.omnimesh.io/v1
kind: TargetRoute
metadata:
  # The name canonically includes both the service and the target it routes to
  name: payment-service-route-to-useast
  namespace: production
spec:
  # Which service this route applies to
  targetService: payment-service
  
  # The destination target being configured
  destinationTarget: k8s-cluster-us-east
  
  # The relative weight of this target compared to others
  weight: 80
```

And for a secondary target:

```yaml
apiVersion: traffic.omnimesh.io/v1
kind: TargetRoute
metadata:
  name: payment-service-route-to-baremetal
  namespace: production
spec:
  targetService: payment-service
  destinationTarget: bare-metal-dc1
  weight: 20
```

#### How the Control Plane Aggregates Routes

When formulating routes for `payment-service` in the `/ns/production` namespace, the control plane queries all `TargetRoute` resources matching `spec.targetService == payment-service`.

1. It observes:
   - `k8s-cluster-us-east`: Weight 80
   - `bare-metal-dc1`: Weight 20
2. Total weight = 100.
3. It pushes the computed routing table to the agents.

If a new cluster (`k8s-cluster-eu-west`) comes online, an administrator simply applies a *new* `TargetRoute` resource with a weight. The control plane recalculates the total weight (e.g., 80 + 20 + 20 = 120) and redistributes the updated probabilities seamlessly without locking or modifying existing configuration files.

#### Default Behavior

If a `Service` has endpoints registered in a Target, but **no** `TargetRoute` exists for that target, the control plane assigns a default weight (e.g., `100`).

#### Failover Policy

Failover rules are also declared per-target. A `TargetRoute` can specify that it is only active if another target is unavailable:

```yaml
spec:
  targetService: payment-service
  destinationTarget: cloud-failover-cluster
  weight: 100
  failover:
    activeWhenTargetIsDown: k8s-cluster-us-east
```

## Control Plane Route Distribution

When an agent requests routes for `payment-service`, the control plane computes the routes **relative to the requesting agent's target**.

For an agent in `bare-metal-dc1`:
- **For endpoints in `bare-metal-dc1`**: The CP provides the direct `grpcAddr` of the destination agents. It applies the aggregated weight for this target.
- **For endpoints in `k8s-cluster-us-east`**: The CP sees the target mismatch. It overrides the destination address with the `gatewayAddress` of the `k8s-cluster-us-east` NetworkTarget. It applies the aggregated weight for this target.

The requesting agent applies its standard Weighted Round-Robin load balancing over these routes. 

## Updates to Health Detection (RFC-0003)

Multi-target routing introduces two new failure domains:
1. **Target-Level Failure (Gateway Down):** The gateway for a `NetworkTarget` might go down, making the entire target unreachable from the outside, even if the internal nodes are healthy.
2. **Partial Partition:** An agent inside `Target A` can reach `Target B`, but an agent inside `Target C` cannot reach `Target B`.

### Impact on Global Outlier Detection

RFC-0003 introduces "Global Outlier Detection" where the control plane aggregates failure reports. 

**Modification for Multi-Network:**
The control plane must track failure rates **per reporting target**, not just globally. 

If agents in `Target A` report 100% failure rates when connecting to `Target B` (likely a gateway issue or network partition), but agents in `Target C` report 0% failures to `Target B`, the control plane must:
1. Stop sending routes for `Target B` to agents in `Target A` (triggering failover to secondary `TargetRoutes` if configured, or removing the target entirely from A's routing probability).
2. CONTINUE sending routes for `Target B` to agents in `Target C`.

The Outlier Detection state model changes from:
`Endpoint -> HealthState`
To:
`SourceTarget -> DestinationTarget -> Endpoint -> HealthState`

### Cross-Target Health Probing

Agents only actively probe *local* services on their own node. Cross-target health is determined entirely by **Passive Health Observation (Traffic-Based)**. 

When an agent in `Target A` attempts to tunnel traffic to `Target B`'s Gateway and receives TCP connection failures, it trips its local circuit breaker and reports the failure to the control plane. The control plane then aggregates these reports per the updated Global Outlier Detection model above.

## etcd Key Space Extension

Following the key space design from RFC-0002, new resources for multi-network routing are stored under the `/omnimesh/resources/` prefix:

```text
/omnimesh/
└── resources/
    ├── core.omnimesh.io/v1/
    │   └── networktargets/
    │       ├── k8s-cluster-us-east                    → NetworkTarget resource JSON
    │       └── bare-metal-dc1                         → NetworkTarget resource JSON
    │
    └── traffic.omnimesh.io/v1/
        └── targetroutes/
            └── production/
                ├── payment-service-route-to-useast    → TargetRoute resource JSON
                └── payment-service-route-to-baremetal → TargetRoute resource JSON
```

## Trade-offs and Drawbacks

| Concern | Discussion |
| ------- | ---------- |
| **Double Hop Latency** | Cross-target traffic must pass through the destination target's Gateway. This is unavoidable when bridging unroutable networks but adds a network hop and latency. |
| **Gateway Bottleneck** | The Gateway for a NetworkTarget handles *all* inbound cross-target mesh traffic. It must be highly available and scaled appropriately. |
| **SNAT/Source IP Loss** | If the Gateway uses Source NAT, the receiving agent might lose the original caller's IP. However, because we use HBONE tunnels, the original source identity and IP are carried securely in the HTTP/2 CONNECT headers, mitigating this issue completely. |
| **Resource Sprawl** | Defining a `TargetRoute` per target per service creates more YAML files than a single monolithic list. However, this trade-off is widely accepted in GitOps and distributed systems because it prevents configuration locks and collision when multiple teams deploy targets independently. |

## Dependencies
- **RFC-0001**: Relies on the core declarative resource model and the Node self-registration process.
- **RFC-0003**: Relies on the traffic policy engine, load balancer algorithm, and the Outlier Detection mechanisms (which are amended here).
