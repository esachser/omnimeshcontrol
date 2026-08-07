package grpcadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	omnimeshv1 "github.com/esachser/omnimeshcontrol/api/gen/omnimesh/v1"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// AgentServer implements the AgentService gRPC server.
// It handles the persistent bidirectional stream for agent communication.
type AgentServer struct {
	omnimeshv1.UnimplementedAgentServiceServer
	nodeRegistrar  inbound.NodeRegistrar
	healthReporter inbound.ServiceHealthReporter
	scopeManager   inbound.AgentScopeManager
	routeManager   inbound.RouteManager
	logger         *slog.Logger

	mu            sync.RWMutex
	activeStreams map[string]omnimeshv1.AgentService_AgentStreamServer
}

// NewAgentServer creates a new AgentServer.
func NewAgentServer(
	nodeRegistrar inbound.NodeRegistrar,
	healthReporter inbound.ServiceHealthReporter,
	scopeManager inbound.AgentScopeManager,
	routeManager inbound.RouteManager,
	logger *slog.Logger,
) *AgentServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &AgentServer{
		nodeRegistrar:  nodeRegistrar,
		healthReporter: healthReporter,
		scopeManager:   scopeManager,
		routeManager:   routeManager,
		logger:         logger,
		activeStreams:  make(map[string]omnimeshv1.AgentService_AgentStreamServer),
	}
}

// AgentStream handles the persistent bidirectional gRPC stream.
// The first message MUST be a RegisterNodeRequest.
// Stream liveness == agent liveness (no heartbeat RPC).
func (s *AgentServer) AgentStream(stream omnimeshv1.AgentService_AgentStreamServer) error {
	ctx := stream.Context()

	// Read the first message — must be registration.
	firstMsg, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "failed to receive registration: %v", err)
	}

	reg := firstMsg.GetRegisterNode()
	if reg == nil {
		return status.Error(codes.InvalidArgument, "first message must be RegisterNodeRequest")
	}

	// Extract SPIFFE SVID from mTLS peer certificates if present
	var spiffeID string
	if p, ok := peer.FromContext(ctx); ok {
		if tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo); ok {
			for _, cert := range tlsInfo.State.PeerCertificates {
				for _, uri := range cert.URIs {
					if uri.Scheme == "spiffe" {
						spiffeID = uri.String()
						break
					}
				}
			}
		}
	}

	nodeID := reg.NodeId
	if spiffeID != "" {
		s.logger.Info("agent stream opened with SPIFFE SVID", "nodeID", nodeID, "addr", reg.Addr, "spiffeID", spiffeID)
	} else {
		s.logger.Info("agent stream opened", "nodeID", nodeID, "addr", reg.Addr)
	}

	s.mu.Lock()
	s.activeStreams[nodeID] = stream
	s.mu.Unlock()

	// Build node model.
	node := &model.Node{
		ID:       nodeID,
		Platform: platformFromString(reg.Platform),
		Target:   reg.Target,
		Addr:     reg.Addr,
		Labels:   reg.Labels,
	}

	// Register the node.
	if err := s.nodeRegistrar.RegisterNode(ctx, node); err != nil {
		return status.Errorf(codes.Internal, "register node: %v", err)
	}

	// Persist TargetRoutes for the node's initial services.
	for _, svcInfo := range reg.Services {
		s.applyTargetRoute(ctx, svcInfo, nodeID)
	}

	// On stream close (normal or error), handle disconnect.
	defer func() {
		s.mu.Lock()
		delete(s.activeStreams, nodeID)
		s.mu.Unlock()

		s.nodeRegistrar.OnStreamDisconnect(ctx, nodeID)
		s.logger.Info("agent stream closed", "nodeID", nodeID)
	}()

	// Send initial scope snapshot.
	scope, err := s.scopeManager.GetAgentScope(ctx, nodeID)
	if err != nil {
		s.logger.Warn("failed to get agent scope", "nodeID", nodeID, "error", err)
	} else {
		snapshot := scopeToProto(scope)
		if err := stream.Send(&omnimeshv1.AgentStreamResponse{
			Payload: &omnimeshv1.AgentStreamResponse_ScopeSnapshot{ScopeSnapshot: snapshot},
		}); err != nil {
			return status.Errorf(codes.Internal, "send scope snapshot: %v", err)
		}
	}

	// Process incoming messages.
messagereceiver:
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		switch payload := msg.Payload.(type) {
		case *omnimeshv1.AgentStreamRequest_HealthReport:
			s.handleHealthReport(ctx, payload.HealthReport, nodeID)
		case *omnimeshv1.AgentStreamRequest_UpdateNodeServices:
			s.handleUpdateNodeServices(ctx, payload.UpdateNodeServices, nodeID)
		case *omnimeshv1.AgentStreamRequest_DrainNode:
			s.handleDrainNode(ctx, payload.DrainNode, nodeID)
			break messagereceiver
		default:
			s.logger.Warn("unknown agent message type", "nodeID", nodeID)
		}
	}
	return nil
}

func (s *AgentServer) handleHealthReport(ctx context.Context, report *omnimeshv1.HealthReport, nodeID string) {
	health := &model.EndpointHealth{
		ServiceID: model.ServiceID{
			Namespace: report.Namespace,
			Name:      report.ServiceName,
		},
		RouteName: report.RouteName,
		Status:    healthStatusFromProto(report.Status),
		ProbeType: probeTypeFromString(report.ProbeType),
		NodeID:    nodeID,
	}
	if err := s.healthReporter.ReportEndpointHealth(ctx, health); err != nil {
		s.logger.Warn("failed to process health report", "nodeID", nodeID, "error", err)
	}
}

func (s *AgentServer) handleUpdateNodeServices(ctx context.Context, req *omnimeshv1.UpdateNodeServicesRequest, nodeID string) {
	var added, removed []model.ServiceID

	for _, raw := range req.AddedServices {
		svcID := model.ServiceID{
			Namespace: raw.Namespace,
			Name:      raw.Name,
		}
		added = append(added, svcID)
		s.applyTargetRoute(ctx, raw, nodeID)
	}

	for _, raw := range req.RemovedServices {
		svcID := model.ServiceID{
			Namespace: raw.Namespace,
			Name:      raw.Name,
		}
		removed = append(removed, svcID)
		if err := s.routeManager.Delete(ctx, raw.Namespace, raw.Id, raw.Name, nodeID); err != nil {
			s.logger.Warn("failed to delete target route", "nodeID", nodeID, "route", raw.Id, "error", err)
		}
	}

	if err := s.nodeRegistrar.UpdateNodeServices(ctx, nodeID, added, removed); err != nil {
		s.logger.Warn("failed to process update node services", "nodeID", nodeID, "error", err)
	}
}

func (s *AgentServer) handleDrainNode(ctx context.Context, drain *omnimeshv1.DrainNodeRequest, nodeID string) {
	if err := s.nodeRegistrar.DrainNode(ctx, nodeID); err != nil {
		s.logger.Warn("failed to process drain node", "nodeID", nodeID, "error", err)
	}
}

// applyTargetRoute persists a TargetRoute for a service reported by an agent.
func (s *AgentServer) applyTargetRoute(ctx context.Context, svcInfo *omnimeshv1.NodeServiceInfo, nodeID string) {
	route := &model.TargetRoute{
		NodeID:        nodeID,
		Name:          svcInfo.Id,
		Namespace:     svcInfo.Namespace,
		TargetService: svcInfo.Name,
		Labels:        svcInfo.Labels,
	}
	if err := s.routeManager.Apply(ctx, route); err != nil {
		s.logger.Warn("failed to apply target route", "nodeID", nodeID, "route", svcInfo.Id, "error", err)
	}
}

// --- Helper functions ---

func platformFromString(s string) model.NodePlatform {
	switch s {
	case "linux":
		return model.NodePlatformLinux
	case "windows":
		return model.NodePlatformWindows
	case "darwin":
		return model.NodePlatformDarwin
	default:
		return model.NodePlatformLinux
	}
}

func healthStatusFromProto(s omnimeshv1.EndpointHealthStatus) model.EndpointHealthStatus {
	switch s {
	case omnimeshv1.EndpointHealthStatus_ENDPOINT_HEALTH_STATUS_HEALTHY:
		return model.EndpointHealthHealthy
	case omnimeshv1.EndpointHealthStatus_ENDPOINT_HEALTH_STATUS_UNHEALTHY:
		return model.EndpointHealthUnhealthy
	case omnimeshv1.EndpointHealthStatus_ENDPOINT_HEALTH_STATUS_DRAINING:
		return model.EndpointHealthDraining
	default:
		return model.EndpointHealthUnknown
	}
}

func probeTypeFromString(s string) model.HealthProbeType {
	switch s {
	case "HTTP":
		return model.HealthProbeHTTP
	case "gRPC":
		return model.HealthProbeGRPC
	default:
		return model.HealthProbeTCP
	}
}

func scopeToProto(scope *model.AgentScope) *omnimeshv1.ScopeSnapshot {
	snapshot := &omnimeshv1.ScopeSnapshot{}

	for _, svc := range scope.Services {
		info := &omnimeshv1.ServiceInfo{
			Namespace: svc.ID.Namespace,
			Name:      svc.ID.Name,
			Protocol:  svc.Protocol.String(),
			Port:      int32(svc.Port),
		}
		if svc.MeshAddr.IsValid() {
			info.MeshAddr = svc.MeshAddr.String()
		}
		snapshot.Services = append(snapshot.Services, info)
	}

	for _, route := range scope.Routes {
		ri := &omnimeshv1.RouteInfo{
			Namespace:   route.Namespace,
			ServiceName: route.TargetService,
			TargetNodes: []*omnimeshv1.NodeEndpointInfo{
				{
					NodeId: route.NodeID,
					Labels: route.Labels,
					Addr:   route.Address,
				},
			},
		}
		snapshot.Routes = append(snapshot.Routes, ri)
	}

	for _, policy := range scope.TrafficPolicies {
		snapshot.TrafficPolicies = append(snapshot.TrafficPolicies, trafficPolicyToProto(policy))
	}

	for _, health := range scope.EndpointHealth {
		snapshot.EndpointHealth = append(snapshot.EndpointHealth, endpointHealthToProto(health))
	}

	for _, policy := range scope.AuthzPolicies {
		snapshot.AuthzPolicies = append(snapshot.AuthzPolicies, authzPolicyToAgentProto(policy))
	}

	return snapshot
}

// PushUpdate pushes a differential scope update to a specific agent node.
func (s *AgentServer) PushUpdate(ctx context.Context, nodeID string, update model.ScopeUpdate) error {
	s.mu.RLock()
	stream, ok := s.activeStreams[nodeID]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("active stream not found for node %q", nodeID)
	}

	protoUpdate := scopeUpdateToProto(update)

	if err := stream.Send(&omnimeshv1.AgentStreamResponse{
		Payload: &omnimeshv1.AgentStreamResponse_ScopeUpdate{ScopeUpdate: protoUpdate},
	}); err != nil {
		return fmt.Errorf("send scope update: %w", err)
	}

	return nil
}

func scopeUpdateTypeToProto(t model.ScopeUpdateType) omnimeshv1.ScopeUpdateType {
	switch t {
	case model.ScopeUpdateModify:
		return omnimeshv1.ScopeUpdateType_SCOPE_UPDATE_TYPE_MODIFY
	case model.ScopeUpdateRemove:
		return omnimeshv1.ScopeUpdateType_SCOPE_UPDATE_TYPE_REMOVE
	default:
		return omnimeshv1.ScopeUpdateType_SCOPE_UPDATE_TYPE_ADD
	}
}

func scopeUpdateToProto(update model.ScopeUpdate) *omnimeshv1.ScopeUpdate {
	protoUpdate := &omnimeshv1.ScopeUpdate{
		Type: scopeUpdateTypeToProto(update.Type),
	}

	for _, svc := range update.Services {
		info := &omnimeshv1.ServiceInfo{
			Namespace: svc.ID.Namespace,
			Name:      svc.ID.Name,
			Protocol:  svc.Protocol.String(),
			Port:      int32(svc.Port),
		}
		if svc.MeshAddr.IsValid() {
			info.MeshAddr = svc.MeshAddr.String()
		}
		protoUpdate.Services = append(protoUpdate.Services, info)
	}

	for _, route := range update.Routes {
		ri := &omnimeshv1.RouteInfo{
			Namespace:   route.Namespace,
			ServiceName: route.TargetService,
			TargetNodes: []*omnimeshv1.NodeEndpointInfo{
				{
					NodeId:        route.NodeID,
					Labels:        route.Labels,
					Addr:          route.Address,
					RouteName:     route.Name,
					Target:        route.Target,
					TargetAddress: route.TargetAddress,
				},
			},
		}
		protoUpdate.Routes = append(protoUpdate.Routes, ri)
	}

	for _, policy := range update.TrafficPolicies {
		protoUpdate.TrafficPolicies = append(protoUpdate.TrafficPolicies, trafficPolicyToProto(policy))
	}

	for _, health := range update.EndpointHealth {
		protoUpdate.EndpointHealth = append(protoUpdate.EndpointHealth, endpointHealthToProto(health))
	}

	for _, policy := range update.AuthzPolicies {
		protoUpdate.AuthzPolicies = append(protoUpdate.AuthzPolicies, authzPolicyToAgentProto(policy))
	}

	return protoUpdate
}

func trafficPolicyToProto(policy model.TrafficPolicy) *omnimeshv1.TrafficPolicyInfo {
	return &omnimeshv1.TrafficPolicyInfo{
		Name:                   policy.Name,
		Namespace:              policy.Namespace,
		TargetServiceNamespace: policy.TargetService.Namespace,
		TargetServiceName:      policy.TargetService.Name,
		Retry:                  retryPolicyToProto(policy.Retry),
		CircuitBreaker:         circuitBreakerPolicyToProto(policy.CircuitBreaker),
		RateLimiter:            rateLimitPolicyToProto(policy.RateLimiter),
		LoadBalancer:           loadBalancerPolicyToProto(policy.LoadBalancer),
		OutlierDetection:       outlierDetectionPolicyToProto(policy.OutlierDetection),
		Routes:                 trafficRoutesToProto(policy.Routes),
	}
}

func retryPolicyToProto(p *model.RetryPolicy) *omnimeshv1.RetryPolicyInfo {
	if p == nil {
		return nil
	}
	return &omnimeshv1.RetryPolicyInfo{
		MaxRetries:    int32(p.MaxRetries),
		RetryOn:       p.RetryOn,
		BackoffBaseMs: p.BackoffBase.Milliseconds(),
		BackoffMaxMs:  p.BackoffMax.Milliseconds(),
	}
}

func circuitBreakerPolicyToProto(p *model.CircuitBreakerPolicy) *omnimeshv1.CircuitBreakerPolicyInfo {
	if p == nil {
		return nil
	}
	return &omnimeshv1.CircuitBreakerPolicyInfo{
		FailureThreshold: int32(p.FailureThreshold),
		FailureWindowMs:  p.FailureWindow.Milliseconds(),
		HalfOpenAfterMs:  p.HalfOpenAfter.Milliseconds(),
	}
}

func rateLimitPolicyToProto(p *model.RateLimitPolicy) *omnimeshv1.RateLimitPolicyInfo {
	if p == nil {
		return nil
	}
	action := omnimeshv1.RateLimitAction_RATE_LIMIT_ACTION_REJECT
	if p.LimitExceededAction == model.RateLimitActionQueue {
		action = omnimeshv1.RateLimitAction_RATE_LIMIT_ACTION_QUEUE
	}
	return &omnimeshv1.RateLimitPolicyInfo{
		RequestsPerSecond:     int32(p.RequestsPerSecond),
		BurstSize:             int32(p.BurstSize),
		MaxConcurrentRequests: int32(p.MaxConcurrentRequests),
		LimitExceededAction:   action,
	}
}

func loadBalancerPolicyToProto(p *model.LoadBalancerPolicy) *omnimeshv1.LoadBalancerPolicyInfo {
	if p == nil {
		return nil
	}
	var algo omnimeshv1.LoadBalancerAlgorithm
	switch p.Algorithm {
	case model.LoadBalancerLeastConnections:
		algo = omnimeshv1.LoadBalancerAlgorithm_LOAD_BALANCER_ALGORITHM_LEAST_CONNECTIONS
	case model.LoadBalancerRandom:
		algo = omnimeshv1.LoadBalancerAlgorithm_LOAD_BALANCER_ALGORITHM_RANDOM
	default:
		algo = omnimeshv1.LoadBalancerAlgorithm_LOAD_BALANCER_ALGORITHM_WEIGHTED_ROUND_ROBIN
	}
	return &omnimeshv1.LoadBalancerPolicyInfo{Algorithm: algo}
}

func outlierDetectionPolicyToProto(p *model.OutlierDetectionPolicy) *omnimeshv1.OutlierDetectionPolicyInfo {
	if p == nil {
		return nil
	}
	return &omnimeshv1.OutlierDetectionPolicyInfo{
		ErrorRateThreshold: p.ErrorRateThreshold,
		MinReportingAgents: int32(p.MinReportingAgents),
		EjectionDurationMs: p.EjectionDuration.Milliseconds(),
		MaxEjectionPercent: p.MaxEjectionPercent,
	}
}

func trafficRoutesToProto(routes []model.TrafficRoute) []*omnimeshv1.TrafficRouteInfo {
	if len(routes) == 0 {
		return nil
	}
	out := make([]*omnimeshv1.TrafficRouteInfo, 0, len(routes))
	for _, r := range routes {
		out = append(out, &omnimeshv1.TrafficRouteInfo{
			Destination: &omnimeshv1.TrafficRouteDestinationInfo{
				Target: r.Destination.Target,
				Labels: r.Destination.Labels,
			},
			Weight: int32(r.Weight),
		})
	}
	return out
}

// durationFromMs converts a millisecond int64 (as stored in proto) back to time.Duration.
// Kept here for use in any future proto→model conversion.
func durationFromMs(ms int64) time.Duration {
	return time.Duration(ms) * time.Millisecond
}

// endpointHealthToProto converts a domain EndpointHealth to its proto representation.
// Field names are aligned with HealthReport (the agent→CP direction).
func endpointHealthToProto(h model.EndpointHealth) *omnimeshv1.EndpointHealthInfo {
	return &omnimeshv1.EndpointHealthInfo{
		Namespace:   h.ServiceID.Namespace,
		ServiceName: h.ServiceID.Name,
		RouteName:   h.RouteName,
		Status:      healthStatusToProto(h.Status),
		ProbeType:   h.ProbeType.String(),
		NodeId:      h.NodeID,
	}
}

func authzPolicyToAgentProto(policy model.AuthorizationPolicy) *omnimeshv1.AuthzPolicyInfo {
	return &omnimeshv1.AuthzPolicyInfo{
		Namespace: policy.Namespace,
		Name:      policy.Name,
		Action:    policy.Action.String(),
		Source: &omnimeshv1.SelectorInfo{
			Namespace: policy.Source.Namespace,
			Name:      policy.Source.Name,
			Labels:    policy.Source.Labels,
		},
		Destination: &omnimeshv1.SelectorInfo{
			Namespace: policy.Destination.Namespace,
			Name:      policy.Destination.Name,
			Labels:    policy.Destination.Labels,
		},
	}
}

func healthStatusToProto(s model.EndpointHealthStatus) omnimeshv1.EndpointHealthStatus {
	switch s {
	case model.EndpointHealthHealthy:
		return omnimeshv1.EndpointHealthStatus_ENDPOINT_HEALTH_STATUS_HEALTHY
	case model.EndpointHealthUnhealthy:
		return omnimeshv1.EndpointHealthStatus_ENDPOINT_HEALTH_STATUS_UNHEALTHY
	case model.EndpointHealthDraining:
		return omnimeshv1.EndpointHealthStatus_ENDPOINT_HEALTH_STATUS_DRAINING
	default:
		return omnimeshv1.EndpointHealthStatus_ENDPOINT_HEALTH_STATUS_UNSPECIFIED
	}
}

// BroadcastUpdate pushes a differential scope update to all active agents.
func (s *AgentServer) BroadcastUpdate(ctx context.Context, update model.ScopeUpdate) error {
	s.mu.RLock()
	streams := make(map[string]omnimeshv1.AgentService_AgentStreamServer, len(s.activeStreams))
	for nodeID, stream := range s.activeStreams {
		streams[nodeID] = stream
	}
	s.mu.RUnlock()

	protoUpdate := scopeUpdateToProto(update)

	var (
		errs []error
		mu   sync.Mutex
		wg   sync.WaitGroup
	)

	for nodeID, stream := range streams {
		wg.Add(1)
		go func(nID string, st omnimeshv1.AgentService_AgentStreamServer) {
			defer wg.Done()
			if err := st.Send(&omnimeshv1.AgentStreamResponse{
				Payload: &omnimeshv1.AgentStreamResponse_ScopeUpdate{ScopeUpdate: protoUpdate},
			}); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("node %s: %w", nID, err))
				mu.Unlock()
			}
		}(nodeID, stream)
	}

	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("broadcast scope update: %w", errors.Join(errs...))
	}

	return nil
}

// Compile-time interface checks.
var _ outbound.ScopeDistributor = (*AgentServer)(nil)
