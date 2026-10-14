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
	"github.com/esachser/omnimeshcontrol/internal/adapter/outbound/spire"
	"github.com/esachser/omnimeshcontrol/internal/application/authz"
	"github.com/esachser/omnimeshcontrol/internal/application/health"
	"github.com/esachser/omnimeshcontrol/internal/application/namespace"
	"github.com/esachser/omnimeshcontrol/internal/application/node"
	"github.com/esachser/omnimeshcontrol/internal/application/policy"
	"github.com/esachser/omnimeshcontrol/internal/application/route"
	"github.com/esachser/omnimeshcontrol/internal/application/scope"
	"github.com/esachser/omnimeshcontrol/internal/application/service"
	domainservice "github.com/esachser/omnimeshcontrol/internal/domain/service"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
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
	grpcAddr := envOrDefault("OMNIMESH_GRPC_ADDR", ":9090")
	agentGrpcAddr := envOrDefault("OMNIMESH_AGENT_GRPC_ADDR", ":9091")
	meshCIDR := envOrDefault("OMNIMESH_MESH_CIDR", "100.64.0.0/10")
	dnsZone := envOrDefault("OMNIMESH_DNS_ZONE", "omnimesh.local")

	// --- SPIRE Configuration ---
	spireEnabled := envOrDefault("OMNIMESH_SPIRE_ENABLED", "false") == "true"
	defaultSpireServerAddr := "127.0.0.1:8081"
	defaultSpireAgentAddr := "\\spire-agent\\public\\api"
	if os.PathSeparator == '/' {
		defaultSpireAgentAddr = "unix:///tmp/spire-agent/public/api.sock"
	}
	spireServerAddr := envOrDefault("OMNIMESH_SPIRE_SERVER_ADDR", defaultSpireServerAddr)
	spireAgentAddr := envOrDefault("OMNIMESH_SPIRE_AGENT_ADDR", defaultSpireAgentAddr)
	spireTrustDomain := envOrDefault("OMNIMESH_SPIRE_TRUST_DOMAIN", dnsZone)
	spireParentIDs := strings.Split(envOrDefault("OMNIMESH_SPIRE_PARENT_IDS", "spiffe://omnimesh.local/agents/dev-agent"), ",")
	spireSelectors := strings.Split(envOrDefault("OMNIMESH_SPIRE_SELECTORS", "windows:process-name:{{.Name}}.exe,unix:user:{{.Name}}"), ",")
	spireCACert := os.Getenv("OMNIMESH_SPIRE_CA_CERT")
	spireClientCert := os.Getenv("OMNIMESH_SPIRE_CERT")
	spireClientKey := os.Getenv("OMNIMESH_SPIRE_KEY")

	// --- Build etcd client config from environment ---
	etcdCfg := etcdstore.EtcdClientConfig{
		Endpoints:          strings.Split(envOrDefault("OMNIMESH_ETCD_ENDPOINTS", "http://localhost:2379"), ","),
		DialTimeout:        mustParseDuration("OMNIMESH_ETCD_DIAL_TIMEOUT", 5*time.Second),
		CACertFile:         os.Getenv("OMNIMESH_ETCD_CA_CERT"),
		CertFile:           os.Getenv("OMNIMESH_ETCD_CERT"),
		KeyFile:            os.Getenv("OMNIMESH_ETCD_KEY"),
		TLSEnabled:         os.Getenv("OMNIMESH_ETCD_TLS") == "true",
		InsecureSkipVerify: os.Getenv("OMNIMESH_ETCD_INSECURE") == "true",
		Username:           os.Getenv("OMNIMESH_ETCD_USERNAME"),
		Password:           os.Getenv("OMNIMESH_ETCD_PASSWORD"),
		KeepAliveTime:      mustParseDuration("OMNIMESH_ETCD_KEEPALIVE", 0),
		KeepAliveTimeout:   mustParseDuration("OMNIMESH_ETCD_KEEPALIVE_TO", 0),
		AutoSyncInterval:   mustParseDuration("OMNIMESH_ETCD_AUTOSYNC", 0),
	}

	// --- Connect to external etcd ---
	logger.Info("connecting to etcd",
		"endpoints", etcdCfg.Endpoints,
		"tls", etcdCfg.TLSEnabled || etcdCfg.CACertFile != "",
		"mtls", etcdCfg.CertFile != "",
		"auth", etcdCfg.Username != "",
	)
	etcdClient, err := etcdstore.NewEtcdClient(etcdCfg)
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

	targetApp := route.NewTargetAppService(resourceStore, logger)

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
	nsApp := namespace.NewNamespaceAppService(resourceStore, defaultCIDR, logger)

	// --- Initialize SPIRE Identity Manager if enabled ---
	var identityManager outbound.IdentityManager
	if spireEnabled {
		logger.Info("initializing SPIRE identity manager...")
		spireMgr, err := spire.NewIdentityManager(spire.Config{
			ServerAddr:  spireServerAddr,
			AgentAddr:   spireAgentAddr,
			TrustDomain: spireTrustDomain,
			ParentIDs:   spireParentIDs,
			Selectors:   spireSelectors,
			CACertFile:  spireCACert,
			CertFile:    spireClientCert,
			KeyFile:     spireClientKey,
		}, logger.WithGroup("spire"))
		if err != nil {
			return fmt.Errorf("failed to initialize SPIRE identity manager: %w", err)
		}
		defer spireMgr.Close()
		identityManager = spireMgr
	} else {
		logger.Info("SPIRE identity manager is disabled")
	}

	svcApp := service.NewServiceAppService(service.ServiceAppServiceConfig{
		Resources:  resourceStore,
		State:      stateStore,
		DNS:        dnsManager,
		Identity:   identityManager,
		Namespaces: nsApp,
		DNSZone:    dnsZone,
		Logger:     logger,
	})

	// NodeLivenessTracker backed by etcd leases
	nodeTracker, err := etcdstore.NewNodeLivenessTracker(etcdClient, 30, logger)
	if err != nil {
		return fmt.Errorf("create node tracker: %w", err)
	}
	defer nodeTracker.Close()

	routeApp := route.NewRouteAppService(resourceStore, stateStore, targetApp, logger)
	nodeApp := node.NewNodeAppService(stateStore, nodeTracker, routeApp, logger)

	validator := domainservice.NewPolicyValidator()
	policyApp := policy.NewTrafficPolicyAppService(policyStore, resourceStore, validator, logger)

	healthApp := health.NewHealthAppService(healthStore, nodeTracker, logger)

	authzEvaluator := domainservice.NewAuthzEvaluator()
	authzApp := authz.NewAuthzAppService(resourceStore, depStore, authzEvaluator, logger)

	scopeApp := scope.NewAgentScopeAppService(
		resourceStore,
		stateStore,
		policyStore,
		healthStore,
		depStore,
		routeDistributor,
		targetApp,
		authzApp,
		nsApp,
		logger.WithGroup("scope_app"),
	)

	// --- gRPC Servers Setup ---
	controlServer := grpcadapter.NewControlPlaneServer(svcApp, nsApp, targetApp, policyApp, authzApp, logger)
	agentServer := grpcadapter.NewAgentServer(nodeApp, healthApp, scopeApp, routeApp, logger)

	routeApp.SetScopeDistributor(agentServer)
	routeApp.SetNodeTracker(nodeTracker)
	routeDistributor.SetScopeDistributor(agentServer)

	nodeUpdates := node.NewNodeUpdateService(stateStore, agentServer, logger.WithGroup("node_updates"))
	routeDistributor.SetNodeDrainChecker(nodeUpdates)

	go func() {
		if err := nodeUpdates.Run(ctx); err != nil {
			logger.Warn("node updates stopped", "error", err)
		}
	}()

	namespaceUpdates := namespace.NewNamespaceUpdateService(resourceStore, agentServer, logger.WithGroup("namespace_updates"))
	go func() {
		if err := namespaceUpdates.Run(ctx); err != nil {
			logger.Warn("namespace updates stopped", "error", err)
		}
	}()

	policyUpdates := policy.NewTrafficPolicyUpdateService(policyStore, agentServer, logger.WithGroup("policy_updates"))
	go func() {
		if err := policyUpdates.Run(ctx); err != nil {
			logger.Warn("traffic policy updates stopped", "error", err)
		}
	}()

	serviceUpdates := service.NewServiceUpdateService(resourceStore, stateStore, agentServer, logger.WithGroup("service_updates"))
	go func() {
		if err := serviceUpdates.Run(ctx); err != nil {
			logger.Warn("service updates stopped", "error", err)
		}
	}()

	healthUpdates := health.NewHealthUpdateService(healthStore, agentServer, logger.WithGroup("health_updates"))
	go func() {
		if err := healthUpdates.Run(ctx); err != nil {
			logger.Warn("health updates stopped", "error", err)
		}
	}()

	authzUpdates := authz.NewAuthzPolicyUpdateService(resourceStore, agentServer, logger.WithGroup("authz_updates"))
	go func() {
		if err := authzUpdates.Run(ctx); err != nil {
			logger.Warn("authz policy updates stopped", "error", err)
		}
	}()

	go func() {
		if err := routeDistributor.Run(ctx); err != nil {
			logger.Warn("route distributor stopped", "error", err)
		}
	}()

	// 1. Control Plane Server (CLI / REST Gateway)
	controlLis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return fmt.Errorf("listen control plane %s: %w", grpcAddr, err)
	}
	controlGrpcServer := grpc.NewServer()
	omnimeshv1.RegisterControlPlaneServiceServer(controlGrpcServer, controlServer)

	// 2. Agent Server (Sidecars / Proxies)
	var agentGrpcServer *grpc.Server
	if agentGrpcAddr == grpcAddr {
		// Single listener mode: register AgentService on controlGrpcServer
		omnimeshv1.RegisterAgentServiceServer(controlGrpcServer, agentServer)
	} else {
		var agentServerOpts []grpc.ServerOption
		if spireEnabled {
			logger.Info("configuring SPIRE mTLS for Agent gRPC server", "agentAddr", spireAgentAddr, "trustDomain", spireTrustDomain)
			agentCreds, agentSource, err := spire.NewServerCredentials(ctx, spireAgentAddr, spireTrustDomain)
			if err != nil {
				return fmt.Errorf("failed to create SPIRE server credentials for agent server: %w", err)
			}
			defer agentSource.Close()
			agentServerOpts = append(agentServerOpts, grpc.Creds(agentCreds))
			logger.Info("SPIRE mTLS enabled for Agent gRPC server (requires server & client SVIDs)")
		} else {
			logger.Info("Agent gRPC server running without mTLS (SPIRE disabled)")
		}

		agentLis, err := net.Listen("tcp", agentGrpcAddr)
		if err != nil {
			return fmt.Errorf("listen agent server %s: %w", agentGrpcAddr, err)
		}

		agentGrpcServer = grpc.NewServer(agentServerOpts...)
		omnimeshv1.RegisterAgentServiceServer(agentGrpcServer, agentServer)

		go func() {
			logger.Info("Agent gRPC server starting", "addr", agentGrpcAddr, "spireMTLS", spireEnabled)
			if err := agentGrpcServer.Serve(agentLis); err != nil {
				logger.Error("Agent gRPC server failed", "error", err)
			}
		}()
	}

	logger.Info("ControlPlane gRPC server starting", "addr", grpcAddr)

	// Graceful shutdown.
	go func() {
		<-ctx.Done()
		logger.Info("shutting down gRPC servers...")
		ctxEnd, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		done := make(chan struct{})
		go func() {
			controlGrpcServer.GracefulStop()
			if agentGrpcServer != nil {
				agentGrpcServer.GracefulStop()
			}
			close(done)
		}()

		select {
		case <-done:
		case <-ctxEnd.Done():
			controlGrpcServer.Stop()
			if agentGrpcServer != nil {
				agentGrpcServer.Stop()
			}
		}
		logger.Info("gRPC servers stopped")
	}()

	if err := controlGrpcServer.Serve(controlLis); err != nil {
		return fmt.Errorf("control plane grpc serve: %w", err)
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

// mustParseDuration reads a duration from an env var. If the variable is unset
// or empty the provided default is returned. If the value cannot be parsed,
// the program logs a warning and falls back to the default.
func mustParseDuration(key string, defaultVal time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		slog.Warn("invalid duration env var, using default",
			"key", key, "value", v, "default", defaultVal, "error", err)
		return defaultVal
	}
	return d
}
