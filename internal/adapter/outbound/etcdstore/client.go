// Package etcdstore provides outbound adapter implementations backed by an
// external etcd cluster. It implements ResourceStore, StateStore, and
// NodeLivenessTracker interfaces.
package etcdstore

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// EtcdClientConfig holds all configuration needed to construct a secure etcd
// client. Zero values are safe: an unconfigured instance connects to
// localhost:2379 without TLS or authentication.
type EtcdClientConfig struct {
	// Endpoints is the list of etcd endpoints to connect to.
	// At least one is required.
	Endpoints []string

	// DialTimeout is how long to wait for the initial dial to succeed.
	// Defaults to 5 s when zero.
	DialTimeout time.Duration

	// --- TLS ---

	// CACertFile is the path to a PEM-encoded CA certificate used to verify
	// the server's certificate. When empty and TLSEnabled is false, plaintext
	// connections are used.
	CACertFile string

	// CertFile and KeyFile are the paths to a PEM-encoded client certificate
	// and private key (mutual TLS). Both must be set together.
	CertFile string
	KeyFile  string

	// InsecureSkipVerify disables server certificate verification.
	// Only use in development/testing — never in production.
	InsecureSkipVerify bool

	// TLSEnabled forces TLS even when no certificate files are provided
	// (e.g. when the server uses a certificate signed by a public CA).
	TLSEnabled bool

	// --- Authentication ---

	// Username and Password are used for etcd role-based access control (RBAC).
	// Leave empty to skip authentication.
	Username string
	Password string

	// --- Keep-alive ---

	// KeepAliveTime is the interval between keep-alive probes sent to etcd.
	// Zero disables keep-alives (not recommended for long-lived connections).
	KeepAliveTime time.Duration

	// KeepAliveTimeout is how long to wait for a keep-alive response before
	// the connection is considered dead. Defaults to KeepAliveTime when zero.
	KeepAliveTimeout time.Duration

	// --- Cluster membership ---

	// AutoSyncInterval is how often the client re-syncs its endpoint list from
	// the etcd cluster. Useful when cluster membership changes. Zero disables.
	AutoSyncInterval time.Duration
}

// NewEtcdClient builds and returns an etcd client from the given config.
// Callers are responsible for calling Close() on the returned client.
func NewEtcdClient(cfg EtcdClientConfig) (*clientv3.Client, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("etcd: at least one endpoint is required")
	}

	dialTimeout := cfg.DialTimeout
	if dialTimeout == 0 {
		dialTimeout = 5 * time.Second
	}

	tlsCfg, err := buildTLSConfig(cfg)
	if err != nil {
		return nil, err
	}

	cc := clientv3.Config{
		Endpoints:        cfg.Endpoints,
		DialTimeout:      dialTimeout,
		TLS:              tlsCfg,
		Username:         cfg.Username,
		Password:         cfg.Password,
		AutoSyncInterval: cfg.AutoSyncInterval,
	}

	if cfg.KeepAliveTime > 0 {
		keepAliveTimeout := cfg.KeepAliveTimeout
		if keepAliveTimeout == 0 {
			keepAliveTimeout = cfg.KeepAliveTime
		}
		cc.DialKeepAliveTime = cfg.KeepAliveTime
		cc.DialKeepAliveTimeout = keepAliveTimeout
	}

	client, err := clientv3.New(cc)
	if err != nil {
		return nil, fmt.Errorf("etcd: failed to create client: %w", err)
	}
	return client, nil
}

// buildTLSConfig returns a *tls.Config appropriate for the configuration, or
// nil when plaintext connections are requested.
func buildTLSConfig(cfg EtcdClientConfig) (*tls.Config, error) {
	hasCACert := cfg.CACertFile != ""
	hasClientCert := cfg.CertFile != "" && cfg.KeyFile != ""
	hasTLS := cfg.TLSEnabled || cfg.InsecureSkipVerify || hasCACert || hasClientCert

	if !hasTLS {
		return nil, nil
	}

	tlsCfg := &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // intentional for dev/testing only
		MinVersion:         tls.VersionTLS12,
	}

	// Load CA certificate for server verification.
	if hasCACert {
		caPEM, err := os.ReadFile(cfg.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("etcd: read CA cert %q: %w", cfg.CACertFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("etcd: CA cert %q contains no valid certificates", cfg.CACertFile)
		}
		tlsCfg.RootCAs = pool
	}

	// Load client certificate for mutual TLS.
	if hasClientCert {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("etcd: load client cert/key (%q, %q): %w", cfg.CertFile, cfg.KeyFile, err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	} else if cfg.CertFile != "" || cfg.KeyFile != "" {
		return nil, fmt.Errorf("etcd: both CertFile and KeyFile must be set together (got %q / %q)", cfg.CertFile, cfg.KeyFile)
	}

	return tlsCfg, nil
}
