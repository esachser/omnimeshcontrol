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
	"github.com/spiffe/go-spiffe/v2/spiffeid"
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
	// Selectors is a list of templated workload selectors (deprecated: use SelectorSets).
	Selectors []string
	// SelectorSets is a list of independent templated selector sets.
	// Selectors within each set are AND-ed together; each set is registered
	// as a separate SPIRE workload entry (OR-ed across sets).
	SelectorSets [][]string
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
	if len(cfg.SelectorSets) == 0 && len(cfg.Selectors) == 0 {
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
// Each selector set is registered as a separate SPIRE entry per configured parent ID,
// allowing workloads matching any selector set (e.g. Windows or Linux) to attest.
func (m *SpireIdentityManager) RegisterWorkload(ctx context.Context, identity model.ServiceIdentity) error {
	m.logger.Info("registering workload in SPIRE", "service", identity.ServiceID, "spiffeID", identity.SPIFFEID)

	spiffeID, err := parseSPIFFEID(identity.SPIFFEID)
	if err != nil {
		return fmt.Errorf("register workload: invalid spiffe ID %q: %w", identity.SPIFFEID, err)
	}

	selectorSetTemplates := identity.EffectiveSelectorSets()
	if len(selectorSetTemplates) == 0 {
		selectorSetTemplates = m.cfg.effectiveSelectorSets()
	}

	resolvedSets, err := m.resolveSelectorSets(identity.ServiceID.Name, identity.ServiceID.Namespace, selectorSetTemplates)
	if err != nil {
		return fmt.Errorf("register workload: resolve selectors: %w", err)
	}

	desiredEntries, err := m.buildDesiredEntries(spiffeID, resolvedSets)
	if err != nil {
		return err
	}

	listResp, err := m.client.ListEntries(ctx, &entryv1.ListEntriesRequest{
		Filter: &entryv1.ListEntriesRequest_Filter{
			BySpiffeId: spiffeID,
		},
	})
	if err != nil {
		return fmt.Errorf("register workload: list existing entries: %w", err)
	}

	toCreate, toDeleteIDs := diffEntries(desiredEntries, listResp.Entries)

	if len(toCreate) > 0 {
		req := &entryv1.BatchCreateEntryRequest{
			Entries: toCreate,
		}
		m.logger.Debug("sending batch create entry request", "count", len(toCreate), "spiffeID", identity.SPIFFEID)
		resp, err := m.client.BatchCreateEntry(ctx, req)
		if err != nil {
			return fmt.Errorf("batch create entry call failed: %w", err)
		}
		if len(resp.Results) == 0 {
			return fmt.Errorf("batch create entry call returned empty results")
		}

		for i, res := range resp.Results {
			parentStr := formatSPIFFEID(toCreate[i].ParentId)
			if res.Status.Code == int32(codes.AlreadyExists) {
				m.logger.Debug("workload entry already exists in SPIRE", "parentID", parentStr, "spiffeID", identity.SPIFFEID)
				continue
			}
			if res.Status.Code != int32(codes.OK) {
				return fmt.Errorf("failed to register workload in SPIRE (parent: %s): code=%d message=%s",
					parentStr, res.Status.Code, res.Status.Message)
			}
			m.logger.Info("successfully registered workload in SPIRE",
				"entryID", res.Entry.Id, "parentID", parentStr, "spiffeID", identity.SPIFFEID)
		}
	}

	if len(toDeleteIDs) > 0 {
		m.logger.Info("removing stale spire entries", "count", len(toDeleteIDs), "spiffeID", identity.SPIFFEID)
		if err := m.deleteEntriesByID(ctx, toDeleteIDs); err != nil {
			return fmt.Errorf("register workload: %w", err)
		}
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
	if err := m.deleteEntriesByID(ctx, entryIDs); err != nil {
		return err
	}

	m.logger.Info("successfully deregistered workload from SPIRE", "spiffeID", spiffeIDStr)
	return nil
}

func (m *SpireIdentityManager) deleteEntriesByID(ctx context.Context, entryIDs []string) error {
	delResp, err := m.client.BatchDeleteEntry(ctx, &entryv1.BatchDeleteEntryRequest{Ids: entryIDs})
	if err != nil {
		return fmt.Errorf("batch delete entry call failed: %w", err)
	}

	for _, res := range delResp.Results {
		if res.Status.Code != int32(codes.OK) && res.Status.Code != int32(codes.NotFound) {
			return fmt.Errorf("failed to delete spire entry %q: code=%d message=%s",
				res.Id, res.Status.Code, res.Status.Message)
		}
	}
	return nil
}

func (m *SpireIdentityManager) buildDesiredEntries(spiffeID *spiretypes.SPIFFEID, resolvedSets [][]*spiretypes.Selector) ([]*spiretypes.Entry, error) {
	var desired []*spiretypes.Entry
	for _, parentIDStr := range m.cfg.ParentIDs {
		parentID, err := parseSPIFFEID(parentIDStr)
		if err != nil {
			return nil, fmt.Errorf("register workload: invalid parent ID %q: %w", parentIDStr, err)
		}
		for _, selectors := range resolvedSets {
			candidate := &spiretypes.Entry{
				SpiffeId:  spiffeID,
				ParentId:  parentID,
				Selectors: selectors,
			}
			duplicate := false
			for _, existing := range desired {
				if entriesMatch(existing, candidate) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				desired = append(desired, candidate)
			}
		}
	}
	return desired, nil
}

func diffEntries(desired, existing []*spiretypes.Entry) (toCreate []*spiretypes.Entry, toDeleteIDs []string) {
	for _, d := range desired {
		found := false
		for _, e := range existing {
			if entriesMatch(d, e) {
				found = true
				break
			}
		}
		if !found {
			toCreate = append(toCreate, d)
		}
	}

	for _, e := range existing {
		keep := false
		for _, d := range desired {
			if entriesMatch(d, e) {
				keep = true
				break
			}
		}
		if !keep {
			toDeleteIDs = append(toDeleteIDs, e.Id)
		}
	}

	return toCreate, toDeleteIDs
}

func entriesMatch(a, b *spiretypes.Entry) bool {
	return spiffeIDsEqual(a.SpiffeId, b.SpiffeId) &&
		spiffeIDsEqual(a.ParentId, b.ParentId) &&
		selectorsEqual(a.Selectors, b.Selectors)
}

func spiffeIDsEqual(a, b *spiretypes.SPIFFEID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.TrustDomain == b.TrustDomain && a.Path == b.Path
}

func selectorsEqual(a, b []*spiretypes.Selector) bool {
	if len(a) != len(b) {
		return false
	}
	used := make([]bool, len(b))
	for _, sa := range a {
		matched := false
		for j, sb := range b {
			if !used[j] && sa.Type == sb.Type && sa.Value == sb.Value {
				used[j] = true
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func formatSPIFFEID(id *spiretypes.SPIFFEID) string {
	if id == nil {
		return ""
	}
	return "spiffe://" + id.TrustDomain + id.Path
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
		slog.Info("connecting to SPIRE Agent Workload API", "agentTarget", agentTarget)
		x509Source, err := workloadapi.NewX509Source(ctx, workloadapi.WithClientOptions(
			workloadapi.WithAddr(agentTarget),
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
	if after, ok := strings.CutPrefix(raw, `\\.\pipe\`); ok {
		raw = after
	}
	return "npipe:" + raw
}

// NewX509Source creates a SPIFFE X509Source connected to the SPIRE Agent Workload API.
func NewX509Source(ctx context.Context, agentAddr string) (*workloadapi.X509Source, error) {
	agentTarget := parseAgentAddr(agentAddr)
	source, err := workloadapi.NewX509Source(ctx, workloadapi.WithClientOptions(
		workloadapi.WithAddr(agentTarget),
	))
	if err != nil {
		return nil, fmt.Errorf("spire x509source: %w", err)
	}
	return source, nil
}

// NewServerCredentials creates gRPC server credentials configured for mTLS using SPIRE SVIDs.
// It presents a server X.509 SVID fetched from SPIRE Agent and requires a valid client X.509 SVID
// within the specified trust domain.
func NewServerCredentials(ctx context.Context, agentAddr, trustDomain string) (credentials.TransportCredentials, *workloadapi.X509Source, error) {
	source, err := NewX509Source(ctx, agentAddr)
	if err != nil {
		return nil, nil, err
	}

	td, err := spiffeid.TrustDomainFromString(trustDomain)
	if err != nil {
		source.Close()
		return nil, nil, fmt.Errorf("spire server creds: invalid trust domain %q: %w", trustDomain, err)
	}

	tlsCfg := tlsconfig.MTLSServerConfig(source, source, tlsconfig.AuthorizeMemberOf(td))
	tlsCfg.MinVersion = tls.VersionTLS12
	creds := credentials.NewTLS(tlsCfg)

	return creds, source, nil
}

func (c Config) effectiveSelectorSets() [][]string {
	if len(c.SelectorSets) > 0 {
		return c.SelectorSets
	}
	if len(c.Selectors) > 0 {
		return [][]string{c.Selectors}
	}
	return nil
}

// ParseSelectorSets parses a semicolon-delimited list of comma-delimited selector
// templates (e.g. "windows:user:app,windows:sha256:abc;unix:user:app,unix:pid:1").
func ParseSelectorSets(raw string) [][]string {
	var sets [][]string
	for _, rawSet := range strings.Split(raw, ";") {
		var set []string
		for _, sel := range strings.Split(rawSet, ",") {
			trimmed := strings.TrimSpace(sel)
			if trimmed != "" {
				set = append(set, trimmed)
			}
		}
		if len(set) > 0 {
			sets = append(sets, set)
		}
	}
	return sets
}

func (m *SpireIdentityManager) resolveSelectorSets(name, namespace string, selectorSetTemplates [][]string) ([][]*spiretypes.Selector, error) {
	if len(selectorSetTemplates) == 0 {
		return nil, fmt.Errorf("at least one selector set is required")
	}

	resolved := make([][]*spiretypes.Selector, 0, len(selectorSetTemplates))
	for i, tplSet := range selectorSetTemplates {
		selectors, err := m.resolveSelectors(name, namespace, tplSet)
		if err != nil {
			return nil, fmt.Errorf("selector set %d: %w", i, err)
		}
		if len(selectors) == 0 {
			return nil, fmt.Errorf("selector set %d resolved to no selectors", i)
		}
		resolved = append(resolved, selectors)
	}
	return resolved, nil
}

// resolveSelectors processes selector templates and produces selectors for workload.
func (m *SpireIdentityManager) resolveSelectors(name, namespace string, selectorTemplates []string) ([]*spiretypes.Selector, error) {
	data := struct {
		Name      string
		Namespace string
	}{
		Name:      name,
		Namespace: namespace,
	}

	var selectors []*spiretypes.Selector
	for _, tplStr := range selectorTemplates {
		tplStr = strings.TrimSpace(tplStr)
		if tplStr == "" {
			continue
		}
		tpl, err := template.New("selector").Parse(tplStr)
		if err != nil {
			return nil, fmt.Errorf("parse selector template %q: %w", tplStr, err)
		}

		var buf bytes.Buffer
		if err := tpl.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("execute selector template %q: %w", tplStr, err)
		}

		resolved := strings.TrimSpace(buf.String())
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
