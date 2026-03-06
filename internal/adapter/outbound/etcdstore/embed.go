// Package etcdstore provides outbound adapter implementations backed by an
// embedded etcd server. It implements ResourceStore, StateStore, and
// NodeLivenessTracker interfaces.
package etcdstore

import (
	"fmt"
	"net/url"
	"os"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
)

// EtcdConfig holds configuration for the embedded etcd instance.
type EtcdConfig struct {
	// DataDir is the directory for etcd data storage.
	DataDir string
	// ListenClientURL is the URL for client connections (e.g., "http://localhost:2379").
	ListenClientURL string
	// ListenPeerURL is the URL for peer connections (e.g., "http://localhost:2380").
	ListenPeerURL string
	// ClusterName is the human-readable name of this etcd node.
	ClusterName string
	// InitialCluster is the initial cluster configuration (e.g., "node1=http://localhost:2380").
	// Leave empty for single-node.
	InitialCluster string
}

// DefaultEtcdConfig returns a default configuration for single-node development.
func DefaultEtcdConfig(dataDir string) EtcdConfig {
	return EtcdConfig{
		DataDir:         dataDir,
		ListenClientURL: "http://localhost:2379",
		ListenPeerURL:   "http://localhost:2380",
		ClusterName:     "omnimesh-default",
	}
}

// EmbeddedEtcd wraps an embedded etcd server and exposes a client for use by adapters.
type EmbeddedEtcd struct {
	server *embed.Etcd
	client *clientv3.Client
}

// StartEmbeddedEtcd starts an embedded etcd server with the given configuration.
func StartEmbeddedEtcd(cfg EtcdConfig) (*EmbeddedEtcd, error) {
	etcdCfg := embed.NewConfig()
	etcdCfg.Dir = cfg.DataDir
	etcdCfg.Name = cfg.ClusterName

	clientURL, err := url.Parse(cfg.ListenClientURL)
	if err != nil {
		return nil, fmt.Errorf("etcd: invalid client URL %q: %w", cfg.ListenClientURL, err)
	}
	etcdCfg.ListenClientUrls = []url.URL{*clientURL}
	etcdCfg.AdvertiseClientUrls = []url.URL{*clientURL}

	peerURL, err := url.Parse(cfg.ListenPeerURL)
	if err != nil {
		return nil, fmt.Errorf("etcd: invalid peer URL %q: %w", cfg.ListenPeerURL, err)
	}
	etcdCfg.ListenPeerUrls = []url.URL{*peerURL}
	etcdCfg.AdvertisePeerUrls = []url.URL{*peerURL}

	if cfg.InitialCluster != "" {
		etcdCfg.InitialCluster = cfg.InitialCluster
	} else {
		etcdCfg.InitialCluster = cfg.ClusterName + "=" + cfg.ListenPeerURL
	}
	etcdCfg.ClusterState = embed.ClusterStateFlagNew

	// Suppress excessive etcd logging in embedded mode.
	etcdCfg.LogLevel = "warn"
	etcdCfg.LogOutputs = []string{"stderr"}

	server, err := embed.StartEtcd(etcdCfg)
	if err != nil {
		return nil, fmt.Errorf("etcd: failed to start embedded server: %w", err)
	}

	// Wait for server to be ready.
	select {
	case <-server.Server.ReadyNotify():
	case <-time.After(30 * time.Second):
		server.Close()
		return nil, fmt.Errorf("etcd: server took too long to start")
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{cfg.ListenClientURL},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		server.Close()
		return nil, fmt.Errorf("etcd: failed to create client: %w", err)
	}

	return &EmbeddedEtcd{
		server: server,
		client: client,
	}, nil
}

// Client returns the etcd client for use by adapter implementations.
func (e *EmbeddedEtcd) Client() *clientv3.Client {
	return e.client
}

// Close shuts down the embedded etcd server and client gracefully.
func (e *EmbeddedEtcd) Close() error {
	if e.client != nil {
		if err := e.client.Close(); err != nil {
			return fmt.Errorf("etcd: failed to close client: %w", err)
		}
	}
	if e.server != nil {
		e.server.Close()
	}
	return nil
}

// CleanupDataDir removes the etcd data directory. Only for testing.
func CleanupDataDir(dataDir string) error {
	return os.RemoveAll(dataDir)
}
