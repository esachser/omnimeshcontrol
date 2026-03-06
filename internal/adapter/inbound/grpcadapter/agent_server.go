package grpcadapter

import (
	"context"
	"io"
	"log/slog"
	"net/netip"

	omnimeshv1 "github.com/esachser/omnimeshcontrol/api/gen/omnimesh/v1"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/inbound"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AgentServer implements the AgentService gRPC server.
// It handles the persistent bidirectional stream for agent communication.
type AgentServer struct {
	omnimeshv1.UnimplementedAgentServiceServer
	nodeRegistrar  inbound.NodeRegistrar
	healthReporter inbound.ServiceHealthReporter
	scopeManager   inbound.AgentScopeManager
	logger         *slog.Logger
}

// NewAgentServer creates a new AgentServer.
func NewAgentServer(
	nodeRegistrar inbound.NodeRegistrar,
	healthReporter inbound.ServiceHealthReporter,
	scopeManager inbound.AgentScopeManager,
	logger *slog.Logger,
) *AgentServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &AgentServer{
		nodeRegistrar:  nodeRegistrar,
		healthReporter: healthReporter,
		scopeManager:   scopeManager,
		logger:         logger,
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

	nodeID := reg.NodeId
	s.logger.Info("agent stream opened", "nodeID", nodeID, "addr", reg.Addr)

	// Parse address.
	addr, _ := netip.ParseAddrPort(reg.Addr)

	// Build node model.
	node := &model.Node{
		ID:       nodeID,
		Platform: platformFromString(reg.Platform),
		Addr:     addr,
		Labels:   reg.Labels,
	}

	// Register the node.
	if err := s.nodeRegistrar.RegisterNode(ctx, node); err != nil {
		return status.Errorf(codes.Internal, "register node: %v", err)
	}

	// On stream close (normal or error), handle disconnect.
	defer func() {
		s.nodeRegistrar.OnStreamDisconnect(ctx, nodeID)
		s.logger.Info("agent stream closed", "nodeID", nodeID)
	}()

	// Send initial scope snapshot.
	scope, err := s.scopeManager.GetAgentScope(ctx, nodeID)
	if err != nil {
		s.logger.Warn("failed to get agent scope", "nodeID", nodeID, "error", err)
	} else {
		snapshot := scopeToProto(scope)
		if err := stream.Send(&omnimeshv1.ControlMessage{
			Payload: &omnimeshv1.ControlMessage_ScopeSnapshot{ScopeSnapshot: snapshot},
		}); err != nil {
			return status.Errorf(codes.Internal, "send scope snapshot: %v", err)
		}
	}

	// Process incoming messages.
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		switch payload := msg.Payload.(type) {
		case *omnimeshv1.AgentMessage_HealthReport:
			s.handleHealthReport(ctx, payload.HealthReport, nodeID)
		case *omnimeshv1.AgentMessage_DrainRequest:
			s.handleDrainRequest(ctx, payload.DrainRequest)
		default:
			s.logger.Warn("unknown agent message type", "nodeID", nodeID)
		}
	}
}

func (s *AgentServer) handleHealthReport(ctx context.Context, report *omnimeshv1.HealthReport, nodeID string) {
	health := &model.EndpointHealth{
		ServiceID: model.ServiceID{
			Namespace: report.ServiceNamespace,
			Name:      report.ServiceName,
		},
		Address:   report.EndpointAddress,
		Status:    healthStatusFromProto(report.Status),
		ProbeType: probeTypeFromString(report.ProbeType),
		NodeID:    nodeID,
	}
	if err := s.healthReporter.ReportEndpointHealth(ctx, health); err != nil {
		s.logger.Warn("failed to process health report", "nodeID", nodeID, "error", err)
	}
}

func (s *AgentServer) handleDrainRequest(ctx context.Context, drain *omnimeshv1.DrainRequest) {
	svcID := model.ServiceID{Namespace: drain.ServiceNamespace, Name: drain.ServiceName}
	if err := s.healthReporter.DrainService(ctx, svcID, drain.EndpointAddress); err != nil {
		s.logger.Warn("failed to process drain request", "service", svcID, "error", err)
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
			ServiceNamespace: route.ServiceID.Namespace,
			ServiceName:      route.ServiceID.Name,
		}
		if route.MeshAddr.IsValid() {
			ri.MeshAddr = route.MeshAddr.String()
		}
		for _, node := range route.TargetNodes {
			ri.TargetNodes = append(ri.TargetNodes, &omnimeshv1.NodeEndpointInfo{
				NodeId: node.NodeID,
				Addr:   node.Addr.String(),
			})
		}
		snapshot.Routes = append(snapshot.Routes, ri)
	}

	return snapshot
}
