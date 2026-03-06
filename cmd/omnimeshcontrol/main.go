// Command omnimeshcontrol is the Omnimesh control plane server.
// It starts an embedded etcd, wires up all application services, and
// launches the gRPC server for both CLI and agent communication.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	omnimeshv1 "github.com/esachser/omnimeshcontrol/api/gen/omnimesh/v1"
	"github.com/esachser/omnimeshcontrol/internal/adapter/inbound/grpcadapter"
	"github.com/esachser/omnimeshcontrol/internal/adapter/outbound/etcddns"
	"github.com/esachser/omnimeshcontrol/internal/adapter/outbound/etcdstore"
	"github.com/esachser/omnimeshcontrol/internal/adapter/outbound/routedist"
	"github.com/esachser/omnimeshcontrol/internal/application"
	domainservice "github.com/esachser/omnimeshcontrol/internal/domain/service"
	"google.golang.org/grpc"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// --- Configuration (from env or defaults) ---
	dataDir := envOrDefault("OMNIMESH_ETCD_DATA_DIR", "data/etcd")
	clientURL := envOrDefault("OMNIMESH_ETCD_CLIENT_URL", "http://localhost:2379")
	peerURL := envOrDefault("OMNIMESH_ETCD_PEER_URL", "http://localhost:2380")
	grpcAddr := envOrDefault("OMNIMESH_GRPC_ADDR", ":9090")
	meshCIDR := envOrDefault("OMNIMESH_MESH_CIDR", "100.64.0.0/10")
	dnsZone := envOrDefault("OMNIMESH_DNS_ZONE", "omnimesh.local")
	gracePeriod := 30 * time.Second

	// --- Start embedded etcd ---
	logger.Info("starting embedded etcd", "dataDir", dataDir)
	etcdCfg := etcdstore.EtcdConfig{
		DataDir:         dataDir,
		ListenClientURL: clientURL,
		ListenPeerURL:   peerURL,
		ClusterName:     "omnimesh-default",
	}
	embedded, err := etcdstore.StartEmbeddedEtcd(etcdCfg)
	if err != nil {
		return fmt.Errorf("start etcd: %w", err)
	}
	defer embedded.Close()
	logger.Info("embedded etcd started")

	// --- Wire outbound adapters ---
	resourceStore := etcdstore.NewEtcdResourceStore(embedded.Client())

	stateStore, err := etcdstore.NewEtcdStateStore(embedded.Client())
	if err != nil {
		return fmt.Errorf("create state store: %w", err)
	}
	defer stateStore.Close()

	dnsManager := etcddns.NewEtcdDNSRecordManager(embedded.Client(), dnsZone)
	routeDistributor := routedist.NewGRPCRouteDistributor(logger)

	// --- Wire domain services ---
	cidr, err := netip.ParsePrefix(meshCIDR)
	if err != nil {
		return fmt.Errorf("parse mesh CIDR: %w", err)
	}
	ipAllocator, err := domainservice.NewIPAllocator(cidr)
	if err != nil {
		return fmt.Errorf("create IP allocator: %w", err)
	}
	policyValidator := domainservice.NewPolicyValidator()
	authzEvaluator := domainservice.NewAuthzEvaluator()

	// --- Wire application services ---
	svcApp := application.NewServiceAppService(application.ServiceAppServiceConfig{
		Resources:   resourceStore,
		State:       stateStore,
		DNS:         dnsManager,
		Routes:      routeDistributor,
		Identity:    nil, // SPIRE integration deferred
		IPAllocator: ipAllocator,
		DNSZone:     dnsZone,
		Logger:      logger,
	})

	nsApp := application.NewNamespaceAppService(resourceStore, logger)

	// NodeLivenessTracker with callback to mark nodes as lost.
	nodeTracker := etcdstore.NewNodeLivenessTracker(gracePeriod, func(ctx context.Context, nodeID string) {
		if err := stateStore.MarkNodeLost(ctx, nodeID); err != nil {
			logger.Warn("failed to mark node as lost", "nodeID", nodeID, "error", err)
		}
	}, logger)

	nodeApp := application.NewNodeAppService(stateStore, nodeTracker, logger)

	// Health and scope services — using nil stores for now (Phase 4 MVP).
	// Full wiring will come when the etcd-backed TrafficPolicyStore and
	// EndpointHealthStore adapters are implemented.
	healthApp := application.NewHealthAppService(nil, routeDistributor, logger)
	_ = policyValidator // Will be wired in Phase 5 when full policy flow is connected.
	_ = authzEvaluator  // Will be wired in Phase 5.

	// --- Start gRPC server ---
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", grpcAddr, err)
	}

	grpcServer := grpc.NewServer()
	controlServer := grpcadapter.NewControlPlaneServer(svcApp, nsApp, logger)
	agentServer := grpcadapter.NewAgentServer(nodeApp, healthApp, nil, logger)

	omnimeshv1.RegisterControlPlaneServiceServer(grpcServer, controlServer)
	omnimeshv1.RegisterAgentServiceServer(grpcServer, agentServer)

	logger.Info("gRPC server starting", "addr", grpcAddr)

	// Graceful shutdown.
	go func() {
		<-ctx.Done()
		logger.Info("shutting down gRPC server...")
		grpcServer.GracefulStop()
	}()

	if err := grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("grpc serve: %w", err)
	}

	logger.Info("server stopped")
	return nil
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
