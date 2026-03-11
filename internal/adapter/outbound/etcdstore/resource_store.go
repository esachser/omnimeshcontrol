package etcdstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const resourcePrefix = "/omnimesh/resources/"

// EtcdResourceStore implements the ResourceStore outbound port
// using an embedded etcd instance.
type EtcdResourceStore struct {
	client *clientv3.Client
}

// NewEtcdResourceStore creates a new EtcdResourceStore.
func NewEtcdResourceStore(client *clientv3.Client) *EtcdResourceStore {
	return &EtcdResourceStore{client: client}
}

// fullKey builds the full etcd key for a resource.
func (s *EtcdResourceStore) fullKey(key string) string {
	return resourcePrefix + key
}

// Put creates or updates a resource, returning the new revision.
func (s *EtcdResourceStore) Put(ctx context.Context, key string, resource *model.Resource) (int64, error) {
	now := time.Now()
	// Get current revision
	respGet, err := s.client.Get(ctx, s.fullKey(key))
	if err != nil {
		return 0, fmt.Errorf("resource store: get %q: %w", key, err)
	}
	if len(respGet.Kvs) > 0 {
		resource.Metadata.Revision = respGet.Kvs[0].ModRevision + 1
		// Parse value and copy CreatedAt
		var existing model.Resource
		if err := json.Unmarshal(respGet.Kvs[0].Value, &existing); err == nil {
			resource.Metadata.CreatedAt = existing.Metadata.CreatedAt
		}
	}
	if resource.Metadata.CreatedAt == 0 {
		resource.Metadata.CreatedAt = uint64(now.UnixMilli())
	}
	resource.Metadata.UpdatedAt = uint64(now.UnixMilli())
	data, err := json.Marshal(resource)
	if err != nil {
		return 0, fmt.Errorf("resource store: marshal: %w", err)
	}

	respPut, err := s.client.Put(ctx, s.fullKey(key), string(data))
	if err != nil {
		return 0, fmt.Errorf("resource store: put %q: %w", key, err)
	}
	return respPut.Header.Revision, nil
}

// Get retrieves a resource by key.
func (s *EtcdResourceStore) Get(ctx context.Context, key string) (*model.Resource, error) {
	resp, err := s.client.Get(ctx, s.fullKey(key))
	if err != nil {
		return nil, fmt.Errorf("resource store: get %q: %w", key, err)
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("resource store: get %q: %w", key, domain.ErrResourceNotFound)
	}

	var resource model.Resource
	if err := json.Unmarshal(resp.Kvs[0].Value, &resource); err != nil {
		return nil, fmt.Errorf("resource store: unmarshal %q: %w", key, err)
	}
	resource.Metadata.Revision = resp.Kvs[0].ModRevision
	return &resource, nil
}

// List retrieves all resources under a key prefix.
func (s *EtcdResourceStore) List(ctx context.Context, prefix string) ([]*model.Resource, error) {
	resp, err := s.client.Get(ctx, s.fullKey(prefix), clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("resource store: list %q: %w", prefix, err)
	}

	resources := make([]*model.Resource, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var resource model.Resource
		if err := json.Unmarshal(kv.Value, &resource); err != nil {
			return nil, fmt.Errorf("resource store: unmarshal: %w", err)
		}
		resource.Metadata.Revision = kv.ModRevision
		resources = append(resources, &resource)
	}
	return resources, nil
}

// Delete removes a resource by key.
func (s *EtcdResourceStore) Delete(ctx context.Context, key string) error {
	resp, err := s.client.Delete(ctx, s.fullKey(key))
	if err != nil {
		return fmt.Errorf("resource store: delete %q: %w", key, err)
	}
	if resp.Deleted == 0 {
		return fmt.Errorf("resource store: delete %q: %w", key, domain.ErrResourceNotFound)
	}
	return nil
}

// Watch returns a channel of events for resources under the given prefix.
func (s *EtcdResourceStore) Watch(ctx context.Context, prefix string) (<-chan outbound.ResourceEvent, error) {
	watchCh := s.client.Watch(ctx, s.fullKey(prefix), clientv3.WithPrefix(), clientv3.WithPrevKV())

	eventCh := make(chan outbound.ResourceEvent, 64)
	go func() {
		defer close(eventCh)
		for resp := range watchCh {
			for _, ev := range resp.Events {
				event := outbound.ResourceEvent{
					Key:      strings.TrimPrefix(string(ev.Kv.Key), resourcePrefix),
					Revision: ev.Kv.ModRevision,
				}
				switch ev.Type {
				case clientv3.EventTypePut:
					event.Type = outbound.ResourceEventPut
					var resource model.Resource
					if err := json.Unmarshal(ev.Kv.Value, &resource); err == nil {
						resource.Metadata.Revision = ev.Kv.ModRevision
						event.Resource = &resource
					}
				case clientv3.EventTypeDelete:
					event.Type = outbound.ResourceEventDelete
					kv := ev.PrevKv
					if kv == nil {
						kv = ev.Kv
					}
					if kv != nil && len(kv.Value) > 0 {
						var resource model.Resource
						if err := json.Unmarshal(kv.Value, &resource); err == nil {
							resource.Metadata.Revision = kv.ModRevision
							event.Resource = &resource
						}
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

// CompareAndPut performs an optimistic concurrency update.
func (s *EtcdResourceStore) CompareAndPut(
	ctx context.Context,
	key string,
	expectedRev int64,
	resource *model.Resource,
) (int64, error) {
	data, err := json.Marshal(resource)
	if err != nil {
		return 0, fmt.Errorf("resource store: marshal: %w", err)
	}

	fullKey := s.fullKey(key)
	txnResp, err := s.client.Txn(ctx).
		If(clientv3.Compare(clientv3.ModRevision(fullKey), "=", expectedRev)).
		Then(clientv3.OpPut(fullKey, string(data))).
		Commit()
	if err != nil {
		return 0, fmt.Errorf("resource store: compare-and-put %q: %w", key, err)
	}
	if !txnResp.Succeeded {
		return 0, fmt.Errorf("resource store: compare-and-put %q: %w", key, domain.ErrRevisionConflict)
	}
	return txnResp.Header.Revision, nil
}

// Compile-time interface check.
var _ outbound.ResourceStore = (*EtcdResourceStore)(nil)
