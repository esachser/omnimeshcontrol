package spire

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
	entryv1 "github.com/spiffe/spire-api-sdk/proto/spire/api/server/entry/v1"
	spiretypes "github.com/spiffe/spire-api-sdk/proto/spire/api/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Config holds SPIRE integration parameters.
type Config struct {
	// ServerAddr is the SPIRE Server API endpoint connection string.
	// Can be unix://..., npipe://..., \\..., tcp://..., or a raw TCP address.
	ServerAddr string
	// AgentAddr is the SPIRE Agent Workload API address for obtaining workload SVID.
	AgentAddr string
	// TrustDomain is the SPIRE trust domain (e.g. "omnimesh.local").
	TrustDomain string
	// ParentIDs is a list of parent agent SPIFFE IDs that are authorized to fetch workload SVIDs.
	ParentIDs []string
	// Selectors is a list of templated workload selectors (e.g. "windows:process-name:{{.Name}}.exe").
	Selectors []string
	// CACertFile is an optional path to a PEM CA certificate for verifying the SPIRE Server.
	CACertFile string
	// CertFile is an optional path to a PEM client certificate for mTLS authentication to SPIRE Server.
	CertFile string
	// KeyFile is an optional path to a PEM client private key for mTLS authentication to SPIRE Server.
	KeyFile string
}

// SpireIdentityManager implements outbound.IdentityManager using the SPIRE Server Entry API.
type SpireIdentityManager struct {
	cfg    Config
	logger *slog.Logger
	conn   *grpc.ClientConn
	client entryv1.EntryClient
}

// NewIdentityManager creates and initializes a new SpireIdentityManager.
func NewIdentityManager(cfg Config, logger *slog.Logger) (*SpireIdentityManager, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if cfg.ServerAddr == "" {
		return nil, fmt.Errorf("spire config: ServerAddr is required")
	}
	if cfg.TrustDomain == "" {
		return nil, fmt.Errorf("spire config: TrustDomain is required")
	}
	if len(cfg.ParentIDs) == 0 {
		return nil, fmt.Errorf("spire config: at least one ParentID is required")
	}
	if len(cfg.Selectors) == 0 {
		return nil, fmt.Errorf("spire config: at least one workload selector is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := dialSpire(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to dial spire server at %q: %w", cfg.ServerAddr, err)
	}

	client := entryv1.NewEntryClient(conn)

	logger.Info("connected to SPIRE server", "addr", cfg.ServerAddr, "trustDomain", cfg.TrustDomain)

	return &SpireIdentityManager{
		cfg:    cfg,
		logger: logger,
		conn:   conn,
		client: client,
	}, nil
}

// Close closes the underlying gRPC connection to SPIRE Server.
func (m *SpireIdentityManager) Close() error {
	if m.conn != nil {
		m.logger.Info("closing connection to SPIRE server")
		return m.conn.Close()
	}
	return nil
}

// RegisterWorkload registers a SPIFFE identity for a mesh service.
func (m *SpireIdentityManager) RegisterWorkload(ctx context.Context, identity model.ServiceIdentity) error {
	m.logger.Info("registering workload in SPIRE", "service", identity.ServiceID, "spiffeID", identity.SPIFFEID)

	spiffeID, err := parseSPIFFEID(identity.SPIFFEID)
	if err != nil {
		return fmt.Errorf("register workload: invalid spiffe ID %q: %w", identity.SPIFFEID, err)
	}

	selectors, err := m.resolveSelectors(identity.ServiceID.Name, identity.ServiceID.Namespace)
	if err != nil {
		return fmt.Errorf("register workload: resolve selectors: %w", err)
	}

	// Register the workload for each configured parent ID
	for _, parentIDStr := range m.cfg.ParentIDs {
		parentID, err := parseSPIFFEID(parentIDStr)
		if err != nil {
			return fmt.Errorf("register workload: invalid parent ID %q: %w", parentIDStr, err)
		}

		entry := &spiretypes.Entry{
			SpiffeId:  spiffeID,
			ParentId:  parentID,
			Selectors: selectors,
		}

		req := &entryv1.BatchCreateEntryRequest{
			Entries: []*spiretypes.Entry{entry},
		}

		m.logger.Debug("sending batch create entry request", "parentID", parentIDStr, "spiffeID", identity.SPIFFEID)
		resp, err := m.client.BatchCreateEntry(ctx, req)
		if err != nil {
			return fmt.Errorf("batch create entry call failed: %w", err)
		}

		if len(resp.Results) == 0 {
			return fmt.Errorf("batch create entry call returned empty results")
		}

		res := resp.Results[0]
		if res.Status.Code == int32(codes.AlreadyExists) {
			m.logger.Debug("workload entry already exists in SPIRE", "parentID", parentIDStr, "spiffeID", identity.SPIFFEID)
			continue
		}

		if res.Status.Code != int32(codes.OK) {
			return fmt.Errorf("failed to register workload in SPIRE (parent: %s): code=%d message=%s",
				parentIDStr, res.Status.Code, res.Status.Message)
		}

		m.logger.Info("successfully registered workload in SPIRE",
			"entryID", res.Entry.Id, "parentID", parentIDStr, "spiffeID", identity.SPIFFEID)
	}

	return nil
}

// DeregisterWorkload removes the SPIFFE identity for a mesh service.
func (m *SpireIdentityManager) DeregisterWorkload(ctx context.Context, serviceID model.ServiceID) error {
	spiffeIDStr := fmt.Sprintf("spiffe://%s/ns/%s/sa/%s", m.cfg.TrustDomain, serviceID.Namespace, serviceID.Name)
	m.logger.Info("deregistering workload from SPIRE", "serviceID", serviceID, "spiffeID", spiffeIDStr)

	spiffeID, err := parseSPIFFEID(spiffeIDStr)
	if err != nil {
		return fmt.Errorf("deregister workload: invalid spiffe ID: %w", err)
	}

	listReq := &entryv1.ListEntriesRequest{
		Filter: &entryv1.ListEntriesRequest_Filter{
			BySpiffeId: spiffeID,
		},
	}

	listResp, err := m.client.ListEntries(ctx, listReq)
	if err != nil {
		return fmt.Errorf("list entries failed: %w", err)
	}

	if len(listResp.Entries) == 0 {
		m.logger.Info("no matching spire entries found to delete", "spiffeID", spiffeIDStr)
		return nil
	}

	var entryIDs []string
	for _, entry := range listResp.Entries {
		entryIDs = append(entryIDs, entry.Id)
	}

	m.logger.Info("deleting spire entries", "count", len(entryIDs), "spiffeID", spiffeIDStr)
	delReq := &entryv1.BatchDeleteEntryRequest{
		Ids: entryIDs,
	}

	delResp, err := m.client.BatchDeleteEntry(ctx, delReq)
	if err != nil {
		return fmt.Errorf("batch delete entry call failed: %w", err)
	}

	for _, res := range delResp.Results {
		if res.Status.Code != int32(codes.OK) && res.Status.Code != int32(codes.NotFound) {
			return fmt.Errorf("failed to delete spire entry %q: code=%d message=%s",
				res.Id, res.Status.Code, res.Status.Message)
		}
	}

	m.logger.Info("successfully deregistered workload from SPIRE", "spiffeID", spiffeIDStr)
	return nil
}

// dialSpire establishes a gRPC connection to SPIRE Server.
func dialSpire(ctx context.Context, cfg Config) (*grpc.ClientConn, error) {
	var dialer func(context.Context, string) (net.Conn, error)
	var address string

	rawAddress := cfg.ServerAddr
	if after, ok := strings.CutPrefix(rawAddress, "npipe://"); ok {
		rawAddress = after
	}

	// Normalize Windows Named Pipe formats
	if strings.HasPrefix(rawAddress, `\\spire-server\`) {
		address = `\\.\pipe\spire-server\` + strings.TrimPrefix(rawAddress, `\\spire-server\`)
		dialer = func(ctx context.Context, addr string) (net.Conn, error) {
			return winio.DialPipeContext(ctx, addr)
		}
	} else if strings.HasPrefix(rawAddress, `\\spire-agent\`) {
		address = `\\.\pipe\spire-agent\` + strings.TrimPrefix(rawAddress, `\\spire-agent\`)
		dialer = func(ctx context.Context, addr string) (net.Conn, error) {
			return winio.DialPipeContext(ctx, addr)
		}
	} else if strings.HasPrefix(rawAddress, `\\.\pipe\`) {
		address = rawAddress
		dialer = func(ctx context.Context, addr string) (net.Conn, error) {
			return winio.DialPipeContext(ctx, addr)
		}
	} else if strings.HasPrefix(cfg.ServerAddr, "unix://") {
		address = strings.TrimPrefix(cfg.ServerAddr, "unix://")
		dialer = func(ctx context.Context, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", addr)
		}
	} else {
		// TCP fallback
		address = strings.TrimPrefix(cfg.ServerAddr, "tcp://")
	}

	var creds credentials.TransportCredentials
	if cfg.AgentAddr != "" && cfg.CertFile == "" {
		agentTarget := parseAgentAddr(cfg.AgentAddr)
		// agentTarget = "\\\\.\\pipe\\spire-agent\\public\\api"
		slog.Info("connecting to SPIRE Agent Workload API", "agentTarget", agentTarget)
		x509Source, err := workloadapi.NewX509Source(ctx, workloadapi.WithClientOptions(
			workloadapi.WithAddr(agentTarget),
			// workloadapi.WithNamedPipeName(agentTarget),
		))
		if err != nil {
			slog.Error("failed to acquire SPIFFE X509Source from SPIRE Agent", "error", err)
			return nil, fmt.Errorf("SPIRE Workload API X509Source failed: %w", err)
		}
		slog.Info("successfully acquired SPIFFE X509Source from SPIRE Agent", "agentTarget", agentTarget)
		tlsCfg := tlsconfig.MTLSClientConfig(x509Source, x509Source, tlsconfig.AuthorizeAny())
		tlsCfg.MinVersion = tls.VersionTLS12
		creds = credentials.NewTLS(tlsCfg)
	} else if cfg.CACertFile != "" || cfg.CertFile != "" {
		tlsCfg := &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
		if cfg.CACertFile != "" {
			caPem, err := os.ReadFile(cfg.CACertFile)
			if err != nil {
				return nil, fmt.Errorf("read SPIRE CA: %w", err)
			}
			certPool := x509.NewCertPool()
			if !certPool.AppendCertsFromPEM(caPem) {
				return nil, fmt.Errorf("invalid CA certificate PEM")
			}
			tlsCfg.RootCAs = certPool
		}
		if cfg.CertFile != "" && cfg.KeyFile != "" {
			cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("load client credentials: %w", err)
			}
			tlsCfg.Certificates = []tls.Certificate{cert}
		}
		creds = credentials.NewTLS(tlsCfg)
	} else {
		creds = insecure.NewCredentials()
	}

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
	}
	if dialer != nil {
		opts = append(opts, grpc.WithContextDialer(dialer))
	}

	return grpc.NewClient(address, opts...)
}

func parseAgentAddr(raw string) string {
	if strings.HasPrefix(raw, "unix://") {
		return raw
	}
	raw = strings.TrimPrefix(raw, "npipe:")
	if after, ok := strings.CutPrefix(raw, "npipe://"); ok {
		raw = after
	}
	if strings.HasPrefix(raw, `\\.\pipe\`) {
		raw = strings.TrimPrefix(raw, `\\.\pipe\`)
	}
	return "npipe:" + raw
}

// resolveSelectors processes selector templates and produces selectors for workload.
func (m *SpireIdentityManager) resolveSelectors(name, namespace string) ([]*spiretypes.Selector, error) {
	data := struct {
		Name      string
		Namespace string
	}{
		Name:      name,
		Namespace: namespace,
	}

	var selectors []*spiretypes.Selector
	for _, tplStr := range m.cfg.Selectors {
		tpl, err := template.New("selector").Parse(tplStr)
		if err != nil {
			return nil, fmt.Errorf("parse selector template %q: %w", tplStr, err)
		}

		var buf bytes.Buffer
		if err := tpl.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("execute selector template %q: %w", tplStr, err)
		}

		resolved := buf.String()
		parts := strings.SplitN(resolved, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid resolved selector %q: must be type:value format", resolved)
		}

		selectors = append(selectors, &spiretypes.Selector{
			Type:  parts[0],
			Value: parts[1],
		})
	}

	return selectors, nil
}

// parseSPIFFEID parses a SPIFFE ID string into a *spiretypes.SPIFFEID struct.
func parseSPIFFEID(idStr string) (*spiretypes.SPIFFEID, error) {
	if !strings.HasPrefix(idStr, "spiffe://") {
		return nil, fmt.Errorf("must start with spiffe://")
	}

	rest := idStr[len("spiffe://"):]
	idx := strings.Index(rest, "/")
	if idx == -1 {
		return &spiretypes.SPIFFEID{
			TrustDomain: rest,
			Path:        "",
		}, nil
	}

	return &spiretypes.SPIFFEID{
		TrustDomain: rest[:idx],
		Path:        rest[idx:],
	}, nil
}

var _ outbound.IdentityManager = (*SpireIdentityManager)(nil)
