// Package etcdstore provides outbound adapter implementations backed by an
// external etcd cluster. It implements ResourceStore, StateStore, and
// NodeLivenessTracker interfaces.
package etcdstore

import (
	"fmt"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// NewEtcdClient creates an etcd client connected to the given endpoints.
// Callers are responsible for closing the returned client when done.
func NewEtcdClient(endpoints []string) (*clientv3.Client, error) {
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("etcd: at least one endpoint is required")
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("etcd: failed to create client: %w", err)
	}
	return client, nil
}
