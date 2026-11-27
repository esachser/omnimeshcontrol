package spire

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
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

const defaultSpireBatchSize = 100

// SpireIdentityManager implements outbound.IdentityManager using the SPIRE Server Entry API.
type SpireIdentityManager struct {
	cfg       Config
	logger    *slog.Logger
	conn      *grpc.ClientConn
	client    entryv1.EntryClient
	batchSize int
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
	cfg.ParentIDs = normalizeNonEmptyTrimmed(cfg.ParentIDs)
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
	spiffeIDStr := strings.TrimSpace(identity.SPIFFEID)
	if spiffeIDStr == "" {
		spiffeIDStr = fmt.Sprintf("spiffe://%s/ns/%s/sa/%s", m.cfg.TrustDomain, identity.ServiceID.Namespace, identity.ServiceID.Name)
	}
	m.logger.Info("registering workload in SPIRE", "service", identity.ServiceID, "spiffeID", spiffeIDStr)

	spiffeID, err := parseSPIFFEID(spiffeIDStr)
	if err != nil {
		return fmt.Errorf("register workload: invalid spiffe ID %q: %w", spiffeIDStr, err)
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

	existingEntries, err := m.listEntriesBySPIFFEID(ctx, spiffeID)
	if err != nil {
		return fmt.Errorf("register workload: list existing entries: %w", err)
	}

	toCreate, toDeleteIDs := diffEntries(desiredEntries, existingEntries)

	var createdCount int
	if len(toCreate) > 0 {
		createdIDs, alreadyExistingIDs, err := m.createEntries(ctx, spiffeIDStr, toCreate)
		if err != nil {
			return fmt.Errorf("register workload: %w", err)
		}
		createdCount = len(createdIDs)
		if len(alreadyExistingIDs) > 0 {
			toDeleteIDs = filterOutIDs(toDeleteIDs, alreadyExistingIDs)
		}
	}

	if len(toDeleteIDs) > 0 {
		m.logger.Info("removing stale spire entries",
			"count", len(toDeleteIDs),
			"entryIDs", toDeleteIDs,
			"spiffeID", spiffeIDStr,
		)
		if err := m.deleteEntriesByID(ctx, toDeleteIDs); err != nil {
			return fmt.Errorf("register workload: %w", err)
		}
	}

	m.logger.Info("reconciled spire entries",
		"spiffeID", spiffeIDStr,
		"existing", len(existingEntries),
		"created", createdCount,
		"deleted", len(toDeleteIDs),
	)

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

	existingEntries, err := m.listEntriesBySPIFFEID(ctx, spiffeID)
	if err != nil {
		return fmt.Errorf("list entries failed: %w", err)
	}

	if len(existingEntries) == 0 {
		m.logger.Info("no matching spire entries found to delete", "spiffeID", spiffeIDStr)
		return nil
	}

	entryIDs := make([]string, 0, len(existingEntries))
	for _, entry := range existingEntries {
		if entry != nil && entry.Id != "" {
			entryIDs = append(entryIDs, entry.Id)
		}
	}

	m.logger.Info("deleting spire entries", "count", len(entryIDs), "spiffeID", spiffeIDStr)
	if err := m.deleteEntriesByID(ctx, entryIDs); err != nil {
		return err
	}

	m.logger.Info("successfully deregistered workload from SPIRE", "spiffeID", spiffeIDStr)
	return nil
}

func (m *SpireIdentityManager) effectiveBatchSize() int {
	if m.batchSize > 0 {
		return m.batchSize
	}
	return defaultSpireBatchSize
}

func (m *SpireIdentityManager) listEntriesBySPIFFEID(ctx context.Context, spiffeID *spiretypes.SPIFFEID) ([]*spiretypes.Entry, error) {
	var (
		all        []*spiretypes.Entry
		pageToken  string
		seenTokens = make(map[string]struct{})
	)

	for {
		resp, err := m.client.ListEntries(ctx, &entryv1.ListEntriesRequest{
			Filter: &entryv1.ListEntriesRequest_Filter{
				BySpiffeId: spiffeID,
			},
			OutputMask: &spiretypes.EntryMask{
				SpiffeId:  true,
				ParentId:  true,
				Selectors: true,
			},
			PageToken: pageToken,
		})
		if err != nil {
			return nil, err
		}

		all = append(all, resp.GetEntries()...)

		nextToken := resp.GetNextPageToken()
		if nextToken == "" {
			break
		}
		if nextToken == pageToken {
			return nil, fmt.Errorf("list entries: pagination loop detected for token %q", nextToken)
		}
		if _, seen := seenTokens[nextToken]; seen {
			return nil, fmt.Errorf("list entries: repeated page token %q", nextToken)
		}
		seenTokens[nextToken] = struct{}{}
		pageToken = nextToken
	}

	return all, nil
}

func (m *SpireIdentityManager) createEntries(ctx context.Context, spiffeIDStr string, entries []*spiretypes.Entry) ([]string, map[string]struct{}, error) {
	batchSize := m.effectiveBatchSize()
	var (
		createdIDs         []string
		alreadyExistingIDs = make(map[string]struct{})
		errs               []error
	)

	for start := 0; start < len(entries); start += batchSize {
		end := min(start+batchSize, len(entries))
		chunk := entries[start:end]

		m.logger.Debug("sending batch create entry request", "count", len(chunk), "spiffeID", spiffeIDStr)
		resp, err := m.client.BatchCreateEntry(ctx, &entryv1.BatchCreateEntryRequest{
			Entries: chunk,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("batch create entry call failed: %w", err))
			break
		}
		if len(resp.GetResults()) != len(chunk) {
			errs = append(errs, fmt.Errorf("batch create entry returned %d results for %d entries", len(resp.GetResults()), len(chunk)))
			break
		}

		for i, res := range resp.GetResults() {
			parentStr := formatSPIFFEID(chunk[i].ParentId)
			if res == nil || res.Status == nil {
				errs = append(errs, fmt.Errorf("batch create entry result %d (parent: %s) missing status", i, parentStr))
				continue
			}

			switch codes.Code(res.Status.Code) {
			case codes.AlreadyExists:
				m.logger.Debug("workload entry already exists in SPIRE", "parentID", parentStr, "spiffeID", spiffeIDStr)
				if res.Entry != nil && res.Entry.Id != "" {
					alreadyExistingIDs[res.Entry.Id] = struct{}{}
				}
			case codes.OK:
				var entryID string
				if res.Entry != nil && res.Entry.Id != "" {
					entryID = res.Entry.Id
					createdIDs = append(createdIDs, entryID)
				}
				m.logger.Info("successfully registered workload in SPIRE",
					"entryID", entryID, "parentID", parentStr, "spiffeID", spiffeIDStr)
			default:
				errs = append(errs, fmt.Errorf("failed to register workload in SPIRE (parent: %s): code=%d message=%s",
					parentStr, res.Status.Code, res.Status.Message))
			}
		}

		if len(errs) > 0 {
			break
		}
	}

	if len(errs) > 0 {
		if len(createdIDs) > 0 {
			if rbErr := m.deleteEntriesByID(ctx, createdIDs); rbErr != nil {
				m.logger.Warn("failed to rollback newly created spire entries",
					"count", len(createdIDs), "spiffeID", spiffeIDStr, "error", rbErr)
			}
		}
		return nil, nil, errors.Join(errs...)
	}

	return createdIDs, alreadyExistingIDs, nil
}

func (m *SpireIdentityManager) deleteEntriesByID(ctx context.Context, entryIDs []string) error {
	uniqueIDs := normalizeNonEmptyTrimmed(entryIDs)
	if len(uniqueIDs) == 0 {
		return nil
	}

	batchSize := m.effectiveBatchSize()
	var errs []error

	for start := 0; start < len(uniqueIDs); start += batchSize {
		end := min(start+batchSize, len(uniqueIDs))
		chunk := uniqueIDs[start:end]

		delResp, err := m.client.BatchDeleteEntry(ctx, &entryv1.BatchDeleteEntryRequest{Ids: chunk})
		if err != nil {
			errs = append(errs, fmt.Errorf("batch delete entry call failed: %w", err))
			continue
		}
		if len(delResp.GetResults()) != len(chunk) {
			errs = append(errs, fmt.Errorf("batch delete entry returned %d results for %d entries", len(delResp.GetResults()), len(chunk)))
			continue
		}

		for i, res := range delResp.GetResults() {
			if res == nil || res.Status == nil {
				errs = append(errs, fmt.Errorf("batch delete entry result %d (id: %q) missing status", i, chunk[i]))
				continue
			}
			code := codes.Code(res.Status.Code)
			if code != codes.OK && code != codes.NotFound {
				entryID := res.Id
				if entryID == "" {
					entryID = chunk[i]
				}
				errs = append(errs, fmt.Errorf("failed to delete spire entry %q: code=%d message=%s",
					entryID, res.Status.Code, res.Status.Message))
			}
		}
	}

	return errors.Join(errs...)
}

func filterOutIDs(ids []string, exclude map[string]struct{}) []string {
	if len(ids) == 0 || len(exclude) == 0 {
		return ids
	}
	filtered := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, skip := exclude[id]; !skip {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

func normalizeNonEmptyTrimmed(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
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
	matchedDesired := make([]bool, len(desired))
	for _, e := range existing {
		if e == nil {
			continue
		}
		matched := false
		for i, d := range desired {
			if !matchedDesired[i] && entriesMatch(d, e) {
				matchedDesired[i] = true
				matched = true
				break
			}
		}
		if !matched && e.Id != "" {
			toDeleteIDs = append(toDeleteIDs, e.Id)
		}
	}

	for i, d := range desired {
		if !matchedDesired[i] {
			toCreate = append(toCreate, d)
		}
	}

	return toCreate, toDeleteIDs
}

func entriesMatch(a, b *spiretypes.Entry) bool {
	if a == nil || b == nil {
		return a == b
	}
	return spiffeIDsEqual(a.SpiffeId, b.SpiffeId) &&
		spiffeIDsEqual(a.ParentId, b.ParentId) &&
		selectorsEqual(a.Selectors, b.Selectors)
}

func spiffeIDsEqual(a, b *spiretypes.SPIFFEID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return strings.EqualFold(a.TrustDomain, b.TrustDomain) &&
		normalizeSPIFFEPath(a.Path) == normalizeSPIFFEPath(b.Path)
}

func normalizeSPIFFEPath(path string) string {
	if path == "" {
		return ""
	}
	trimmed := strings.TrimRight(path, "/")
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "/" + trimmed
	}
	return trimmed
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

// ParseParentIDs parses a comma-delimited list of parent agent SPIFFE IDs,
// trimming whitespace and dropping empty entries.
func ParseParentIDs(raw string) []string {
	return normalizeNonEmptyTrimmed(strings.Split(raw, ","))
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

	var (
		selectors []*spiretypes.Selector
		seen      = make(map[string]struct{}, len(selectorTemplates))
	)
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
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid resolved selector %q: must be type:value format", resolved)
		}

		key := parts[0] + "\x00" + parts[1]
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		selectors = append(selectors, &spiretypes.Selector{
			Type:  parts[0],
			Value: parts[1],
		})
	}

	return selectors, nil
}

// parseSPIFFEID parses a SPIFFE ID string into a *spiretypes.SPIFFEID struct.
func parseSPIFFEID(idStr string) (*spiretypes.SPIFFEID, error) {
	trimmed := strings.TrimSpace(idStr)
	if !strings.HasPrefix(trimmed, "spiffe://") {
		return nil, fmt.Errorf("must start with spiffe://")
	}

	rest := trimmed[len("spiffe://"):]
	if rest == "" {
		return nil, fmt.Errorf("trust domain is required")
	}

	idx := strings.Index(rest, "/")
	if idx == 0 {
		return nil, fmt.Errorf("trust domain is required")
	}
	if idx == -1 {
		return &spiretypes.SPIFFEID{
			TrustDomain: strings.ToLower(rest),
			Path:        "",
		}, nil
	}

	return &spiretypes.SPIFFEID{
		TrustDomain: strings.ToLower(rest[:idx]),
		Path:        normalizeSPIFFEPath(rest[idx:]),
	}, nil
}

var _ outbound.IdentityManager = (*SpireIdentityManager)(nil)
