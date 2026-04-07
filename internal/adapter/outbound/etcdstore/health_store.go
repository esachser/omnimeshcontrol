package etcdstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const endpointHealthPrefix = "/omnimesh/state/health/"

// EtcdEndpointHealthStore implements the EndpointHealthStore outbound port.
type EtcdEndpointHealthStore struct {
	client *clientv3.Client
}

// NewEtcdEndpointHealthStore creates a new EtcdEndpointHealthStore.
func NewEtcdEndpointHealthStore(client *clientv3.Client) *EtcdEndpointHealthStore {
	return &EtcdEndpointHealthStore{client: client}
}

// PutEndpointHealth persists an endpoint health report.
// If leaseID is non-zero, the key is attached to the given etcd lease so it is
// automatically removed when the lease expires (node disconnect cleanup).
func (s *EtcdEndpointHealthStore) PutEndpointHealth(ctx context.Context, health *model.EndpointHealth, leaseID int64) error {
	if health == nil {
		return fmt.Errorf("endpoint health store: health is nil")
	}
	if health.LastCheck.IsZero() {
		health.LastCheck = time.Now().UTC()
	}
	data, err := json.Marshal(health)
	if err != nil {
		return fmt.Errorf("endpoint health store: marshal: %w", err)
	}
	key := endpointHealthKey(health.ServiceID, health.Address)
	opts := []clientv3.OpOption{}
	if leaseID != 0 {
		opts = append(opts, clientv3.WithLease(clientv3.LeaseID(leaseID)))
	}
	if _, err := s.client.Put(ctx, key, string(data), opts...); err != nil {
		return fmt.Errorf("endpoint health store: put %q: %w", key, err)
	}
	return nil
}

// GetEndpointHealth retrieves a single endpoint health record.
func (s *EtcdEndpointHealthStore) GetEndpointHealth(ctx context.Context, serviceID model.ServiceID, addr string) (*model.EndpointHealth, error) {
	key := endpointHealthKey(serviceID, addr)
	resp, err := s.client.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("endpoint health store: get %q: %w", key, err)
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("endpoint health store: get %q: not found", key)
	}

	var health model.EndpointHealth
	if err := json.Unmarshal(resp.Kvs[0].Value, &health); err != nil {
		return nil, fmt.Errorf("endpoint health store: unmarshal %q: %w", key, err)
	}
	return &health, nil
}

// ListEndpointHealth lists all endpoint health records for a service.
func (s *EtcdEndpointHealthStore) ListEndpointHealth(ctx context.Context, serviceID model.ServiceID) ([]*model.EndpointHealth, error) {
	prefix := endpointHealthPrefix + serviceID.Namespace + "/" + serviceID.Name + "/"
	resp, err := s.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("endpoint health store: list %q: %w", prefix, err)
	}

	records := make([]*model.EndpointHealth, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var health model.EndpointHealth
		if err := json.Unmarshal(kv.Value, &health); err != nil {
			return nil, fmt.Errorf("endpoint health store: unmarshal: %w", err)
		}
		records = append(records, &health)
	}
	return records, nil
}

// WatchEndpointHealth watches health updates under the health prefix.
func (s *EtcdEndpointHealthStore) WatchEndpointHealth(ctx context.Context) (<-chan outbound.EndpointHealthEvent, error) {
	watchCh := s.client.Watch(ctx, endpointHealthPrefix, clientv3.WithPrefix(), clientv3.WithPrevKV())

	eventCh := make(chan outbound.EndpointHealthEvent, 64)
	go func() {
		defer close(eventCh)
		for resp := range watchCh {
			for _, ev := range resp.Events {
				event := outbound.EndpointHealthEvent{}
				switch ev.Type {
				case clientv3.EventTypePut:
					event.Type = outbound.EndpointHealthEventPut
				case clientv3.EventTypeDelete:
					event.Type = outbound.EndpointHealthEventDelete
				}

				kv := ev.Kv
				if ev.Type == clientv3.EventTypeDelete && ev.PrevKv != nil {
					kv = ev.PrevKv
				}
				if kv != nil && len(kv.Value) > 0 {
					var health model.EndpointHealth
					if err := json.Unmarshal(kv.Value, &health); err == nil {
						event.Health = &health
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

func endpointHealthKey(serviceID model.ServiceID, addr string) string {
	return endpointHealthPrefix + serviceID.Namespace + "/" + serviceID.Name + "/" + addr
}

// Compile-time interface check.
var _ outbound.EndpointHealthStore = (*EtcdEndpointHealthStore)(nil)
