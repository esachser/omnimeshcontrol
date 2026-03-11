package etcdstore

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const dependencyPrefix = "/omnimesh/state/dependencies/"

// EtcdDependencyStore implements the DependencyStore outbound port.
type EtcdDependencyStore struct {
	client *clientv3.Client
}

// NewEtcdDependencyStore creates a new EtcdDependencyStore.
func NewEtcdDependencyStore(client *clientv3.Client) *EtcdDependencyStore {
	return &EtcdDependencyStore{client: client}
}

// PutDependencies persists service dependencies.
func (s *EtcdDependencyStore) PutDependencies(ctx context.Context, deps *model.ServiceDependencies) error {
	if deps == nil {
		return fmt.Errorf("dependency store: deps is nil")
	}
	data, err := json.Marshal(deps)
	if err != nil {
		return fmt.Errorf("dependency store: marshal: %w", err)
	}
	key := dependencyKey(deps.ServiceID)
	if _, err := s.client.Put(ctx, key, string(data)); err != nil {
		return fmt.Errorf("dependency store: put %q: %w", key, err)
	}
	return nil
}

// GetDependencies retrieves dependencies for a service.
func (s *EtcdDependencyStore) GetDependencies(ctx context.Context, serviceID model.ServiceID) (*model.ServiceDependencies, error) {
	key := dependencyKey(serviceID)
	resp, err := s.client.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("dependency store: get %q: %w", key, err)
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("dependency store: get %q: not found", key)
	}

	var deps model.ServiceDependencies
	if err := json.Unmarshal(resp.Kvs[0].Value, &deps); err != nil {
		return nil, fmt.Errorf("dependency store: unmarshal %q: %w", key, err)
	}
	return &deps, nil
}

// WatchDependencies watches for dependency updates.
func (s *EtcdDependencyStore) WatchDependencies(ctx context.Context) (<-chan outbound.DependencyEvent, error) {
	watchCh := s.client.Watch(ctx, dependencyPrefix, clientv3.WithPrefix(), clientv3.WithPrevKV())

	eventCh := make(chan outbound.DependencyEvent, 64)
	go func() {
		defer close(eventCh)
		for resp := range watchCh {
			for _, ev := range resp.Events {
				event := outbound.DependencyEvent{}
				switch ev.Type {
				case clientv3.EventTypePut:
					event.Type = outbound.DependencyEventPut
				case clientv3.EventTypeDelete:
					event.Type = outbound.DependencyEventDelete
				}

				kv := ev.Kv
				if ev.Type == clientv3.EventTypeDelete && ev.PrevKv != nil {
					kv = ev.PrevKv
				}
				if kv != nil && len(kv.Value) > 0 {
					var deps model.ServiceDependencies
					if err := json.Unmarshal(kv.Value, &deps); err == nil {
						event.Dependencies = &deps
					}
				}

				select {
				case eventCh <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return eventCh, nil
}

func dependencyKey(serviceID model.ServiceID) string {
	return dependencyPrefix + serviceID.Namespace + "/" + serviceID.Name
}

// Compile-time interface check.
var _ outbound.DependencyStore = (*EtcdDependencyStore)(nil)
