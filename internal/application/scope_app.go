package application

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

// AgentScopeAppService implements the AgentScopeManager inbound port.
// It calculates the full scope of services, routes, policies, and health
// data that a given agent needs based on its registered services and the
// dependency graph.
type AgentScopeAppService struct {
	resources   outbound.ResourceStore
	state       outbound.StateStore
	policyStore outbound.TrafficPolicyStore
	healthStore outbound.EndpointHealthStore
	depStore    outbound.DependencyStore
	routes      outbound.RouteDistributor
	targets     inbound.TargetManager
	logger      *slog.Logger
}

// NewAgentScopeAppService creates a new AgentScopeAppService.
func NewAgentScopeAppService(
	resources outbound.ResourceStore,
	state outbound.StateStore,
	policyStore outbound.TrafficPolicyStore,
	healthStore outbound.EndpointHealthStore,
	depStore outbound.DependencyStore,
	routes outbound.RouteDistributor,
	targets inbound.TargetManager,
	logger *slog.Logger,
) *AgentScopeAppService {
	if logger == nil {
		logger = slog.Default()
	}
	return &AgentScopeAppService{
		resources:   resources,
		state:       state,
		policyStore: policyStore,
		healthStore: healthStore,
		depStore:    depStore,
		routes:      routes,
		targets:     targets,
		logger:      logger,
	}
}

// GetAgentScope builds the full scope for an agent.
func (s *AgentScopeAppService) GetAgentScope(ctx context.Context, nodeID string) (*model.AgentScope, error) {
	scope := &model.AgentScope{NodeID: nodeID}

	// Get all services across all namespaces to find which ones this agent needs.
	// In a production system, this would be filtered by the node's registered services
	// and their dependency graph. For now, we include all services.
	allResources, err := s.resources.List(ctx, "core.omnimesh.io/v1/Service/")
	if err != nil {
		return nil, fmt.Errorf("scope: list services: %w", err)
	}

	for _, r := range allResources {
		svc, mapErr := resourceToService(r)
		if mapErr != nil {
			continue
		}
		addr, addrErr := s.state.GetIPAllocation(ctx, svc.ID)
		if addrErr == nil {
			svc.MeshAddr = addr
		}
		scope.Services = append(scope.Services, *svc)
	}

	// Get traffic policies for scoped services.
	for _, svc := range scope.Services {
		policy, pErr := s.policyStore.GetPolicyByService(ctx, svc.ID)
		if pErr == nil && policy != nil {
			scope.TrafficPolicies = append(scope.TrafficPolicies, *policy)
		}
	}

	// Get health data for scoped services.
	for _, svc := range scope.Services {
		healthRecords, hErr := s.healthStore.ListEndpointHealth(ctx, svc.ID)
		if hErr == nil {
			for _, h := range healthRecords {
				scope.EndpointHealth = append(scope.EndpointHealth, *h)
			}
		}
	}

	// Fetch requesting node to determine its Target.
	reqNode, err := s.state.GetNode(ctx, nodeID)
	if err != nil {
		s.logger.Warn("failed to get requesting node locality", "nodeID", nodeID, "error", err)
	}
	reqTarget := ""
	if reqNode != nil {
		reqTarget = reqNode.Target
	}

	// Route Distribution with Gateway Address Substitution (RFC-0006).
	// TargetRoutes hold per-node registrations. We copy each one and, for nodes
	// in a different NetworkTarget than the requesting agent, substitute the
	// HBONE gateway address in the NodeID field is not applicable here — we
	// instead annotate the TargetRoute by storing gateway-addr in Labels so the
	// agent can do address resolution. The simpler path: send all TargetRoutes
	// and let the agent look up addresses — future refactor.
	// For now: filter/rewrite using the same gateway substitution as before,
	// but we embed the resolved addr as a label "__gateway__" on the TargetRoute copy.
	if s.routes != nil {
		allRoutes := s.routes.AllRoutes()
		for _, tr := range allRoutes {
			// Copy to avoid mutating shared state.
			routeCopy := model.TargetRoute{
				Name:          tr.Name,
				Namespace:     tr.Namespace,
				TargetService: tr.TargetService,
				NodeID:        tr.NodeID,
				Labels:        tr.Labels,
			}

			// If the node is in a different target, resolve the gateway address
			// and embed it as a special label so agents can find the tunnel endpoint.
			destNode, nErr := s.state.GetNode(ctx, tr.NodeID)
			if nErr == nil && destNode != nil {
				routeCopy.Address = destNode.Addr
				if destNode.Target != reqTarget && destNode.Target != "" {
					netTarget, tErr := s.targets.Get(ctx, destNode.Target)
					if tErr == nil && netTarget != nil && netTarget.GatewayAddress != "" {
						if ipAddr, aErr := netip.ParseAddrPort(netTarget.GatewayAddress); aErr == nil {
							routeCopy.Address = ipAddr
						}
					}
				}
			}

			scope.Routes = append(scope.Routes, routeCopy)
		}
	}

	s.logger.Info("agent scope computed",
		"nodeID", nodeID,
		"services", len(scope.Services),
		"policies", len(scope.TrafficPolicies),
		"healthRecords", len(scope.EndpointHealth),
		"routes", len(scope.Routes),
	)

	return scope, nil
}

// Compile-time interface check.
var _ inbound.AgentScopeManager = (*AgentScopeAppService)(nil)
