// Command omnimeshcontrol is the Omnimesh control plane server.
// It connects to an external etcd cluster, wires up all application services, and
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
	"strings"
	"syscall"
	"time"

	omnimeshv1 "github.com/esachser/omnimeshcontrol/api/gen/omnimesh/v1"
	"github.com/esachser/omnimeshcontrol/internal/adapter/inbound/grpcadapter"
	"github.com/esachser/omnimeshcontrol/internal/adapter/outbound/etcddns"
	"github.com/esachser/omnimeshcontrol/internal/adapter/outbound/etcdstore"
	"github.com/esachser/omnimeshcontrol/internal/application"
	domainservice "github.com/esachser/omnimeshcontrol/internal/domain/service"
	"google.golang.org/grpc"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
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
	etcdEndpointsRaw := envOrDefault("OMNIMESH_ETCD_ENDPOINTS", "http://localhost:2379")
	etcdEndpoints := strings.Split(etcdEndpointsRaw, ",")
	grpcAddr := envOrDefault("OMNIMESH_GRPC_ADDR", ":9090")
	meshCIDR := envOrDefault("OMNIMESH_MESH_CIDR", "100.64.0.0/10")
	dnsZone := envOrDefault("OMNIMESH_DNS_ZONE", "omnimesh.local")

	// --- Connect to external etcd ---
	logger.Info("connecting to etcd", "endpoints", etcdEndpoints)
	etcdClient, err := etcdstore.NewEtcdClient(etcdEndpoints)
	if err != nil {
		return fmt.Errorf("connect etcd: %w", err)
	}
	defer etcdClient.Close()
	logger.Info("etcd client ready")

	// --- Wire outbound adapters ---
	resourceStore := etcdstore.NewEtcdResourceStore(etcdClient)

	stateStore, err := etcdstore.NewEtcdStateStore(etcdClient)
	if err != nil {
		return fmt.Errorf("create state store: %w", err)
	}
	defer stateStore.Close()

	targetApp := application.NewTargetAppService(resourceStore, logger)

	dnsManager := etcddns.NewEtcdDNSRecordManager(etcdClient, dnsZone)
	routeDistributor := etcdstore.NewEtcdRouteDistributor(stateStore, targetApp, logger.WithGroup("route_dist"))
	policyStore := etcdstore.NewEtcdTrafficPolicyStore(resourceStore)
	healthStore := etcdstore.NewEtcdEndpointHealthStore(etcdClient)
	depStore := etcdstore.NewEtcdDependencyStore(etcdClient)

	// --- Parse default CIDR (used for namespaces that don't specify one) ---
	defaultCIDR, err := netip.ParsePrefix(meshCIDR)
	if err != nil {
		return fmt.Errorf("parse default mesh CIDR: %w", err)
	}

	// --- Wire application services ---
	nsApp := application.NewNamespaceAppService(resourceStore, defaultCIDR, logger)

	svcApp := application.NewServiceAppService(application.ServiceAppServiceConfig{
		Resources:  resourceStore,
		State:      stateStore,
		DNS:        dnsManager,
		Identity:   nil, // SPIRE integration deferred
		Namespaces: nsApp,
		DNSZone:    dnsZone,
		Logger:     logger,
	})

	// NodeLivenessTracker backed by etcd leases — liveness, node state, and route
	// keys are all attached to the same lease so etcd cleans them up atomically
	// when the lease expires (grace period) or is revoked (graceful disconnect).
	nodeTracker, err := etcdstore.NewNodeLivenessTracker(etcdClient, 30, logger)
	if err != nil {
		return fmt.Errorf("create node tracker: %w", err)
	}
	defer nodeTracker.Close()

	routeApp := application.NewRouteAppService(resourceStore, stateStore, targetApp, logger)
	nodeApp := application.NewNodeAppService(stateStore, nodeTracker, routeApp, logger)

	validator := domainservice.NewPolicyValidator()
	policyApp := application.NewTrafficPolicyAppService(policyStore, resourceStore, validator, logger)

	healthApp := application.NewHealthAppService(healthStore, nodeTracker, logger)

	scopeApp := application.NewAgentScopeAppService(
		resourceStore,
		stateStore,
		policyStore,
		healthStore,
		depStore,
		routeDistributor,
		targetApp,
		logger.WithGroup("scope_app"),
	)

	// --- Start gRPC server ---
	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", grpcAddr, err)
	}

	grpcServer := grpc.NewServer()
	controlServer := grpcadapter.NewControlPlaneServer(svcApp, nsApp, targetApp, policyApp, logger)
	agentServer := grpcadapter.NewAgentServer(nodeApp, healthApp, scopeApp, routeApp, logger)

	routeApp.SetScopeDistributor(agentServer)
	routeApp.SetNodeTracker(nodeTracker)
	routeDistributor.SetScopeDistributor(agentServer)

	nodeUpdates := application.NewNodeUpdateService(stateStore, agentServer, logger.WithGroup("node_updates"))
	routeDistributor.SetNodeDrainChecker(nodeUpdates)

	go func() {
		if err := nodeUpdates.Run(ctx); err != nil {
			logger.Warn("node updates stopped", "error", err)
		}
	}()

	policyUpdates := application.NewTrafficPolicyUpdateService(policyStore, agentServer, logger.WithGroup("policy_updates"))
	go func() {
		if err := policyUpdates.Run(ctx); err != nil {
			logger.Warn("traffic policy updates stopped", "error", err)
		}
	}()

	serviceUpdates := application.NewServiceUpdateService(resourceStore, stateStore, agentServer, logger.WithGroup("service_updates"))
	go func() {
		if err := serviceUpdates.Run(ctx); err != nil {
			logger.Warn("service updates stopped", "error", err)
		}
	}()

	healthUpdates := application.NewHealthUpdateService(healthStore, agentServer, logger.WithGroup("health_updates"))
	go func() {
		if err := healthUpdates.Run(ctx); err != nil {
			logger.Warn("health updates stopped", "error", err)
		}
	}()

	go func() {
		if err := routeDistributor.Run(ctx); err != nil {
			logger.Warn("route distributor stopped", "error", err)
		}
	}()

	omnimeshv1.RegisterControlPlaneServiceServer(grpcServer, controlServer)
	omnimeshv1.RegisterAgentServiceServer(grpcServer, agentServer)

	logger.Info("gRPC server starting", "addr", grpcAddr)

	// Graceful shutdown.
	go func() {
		<-ctx.Done()
		logger.Info("shutting down gRPC server...")
		ctxEnd, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		go func() {
			grpcServer.GracefulStop()
		}()
		<-ctxEnd.Done()
		grpcServer.Stop()
		logger.Info("gRPC server stopped")
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
