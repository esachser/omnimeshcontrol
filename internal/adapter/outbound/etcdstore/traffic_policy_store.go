package etcdstore

import (
	"context"
	"fmt"

	"github.com/esachser/omnimeshcontrol/internal/codec"
	"github.com/esachser/omnimeshcontrol/internal/domain"
	"github.com/esachser/omnimeshcontrol/internal/domain/model"
	"github.com/esachser/omnimeshcontrol/internal/port/outbound"
)

const trafficPolicyPrefix = "traffic.omnimesh.io/v1/TrafficPolicy/"

// EtcdTrafficPolicyStore implements the TrafficPolicyStore outbound port using ResourceStore.
type EtcdTrafficPolicyStore struct {
	resources outbound.ResourceStore
}

// NewEtcdTrafficPolicyStore creates a new EtcdTrafficPolicyStore.
func NewEtcdTrafficPolicyStore(resources outbound.ResourceStore) *EtcdTrafficPolicyStore {
	return &EtcdTrafficPolicyStore{resources: resources}
}

// PutTrafficPolicy creates or updates a traffic policy resource.
func (s *EtcdTrafficPolicyStore) PutTrafficPolicy(ctx context.Context, policy *model.TrafficPolicy) error {
	resource, err := codec.TrafficPolicyToResource(policy)
	if err != nil {
		return fmt.Errorf("traffic policy store: encode resource: %w", err)
	}

	key := trafficPolicyKey(policy.Namespace, policy.Name)
	if _, err := s.resources.Put(ctx, key, resource); err != nil {
		return fmt.Errorf("traffic policy store: put %q: %w", key, err)
	}
	return nil
}

// GetTrafficPolicy retrieves a traffic policy by namespace and name.
func (s *EtcdTrafficPolicyStore) GetTrafficPolicy(ctx context.Context, namespace, name string) (*model.TrafficPolicy, error) {
	key := trafficPolicyKey(namespace, name)
	resource, err := s.resources.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("traffic policy store: get %q: %w", key, err)
	}

	policy, err := codec.ResourceToTrafficPolicy(resource)
	if err != nil {
		return nil, fmt.Errorf("traffic policy store: decode %q: %w", key, err)
	}
	return policy, nil
}

// GetPolicyByService retrieves a traffic policy by target service ID.
func (s *EtcdTrafficPolicyStore) GetPolicyByService(ctx context.Context, serviceID model.ServiceID) (*model.TrafficPolicy, error) {
	policies, err := s.listAll(ctx)
	if err != nil {
		return nil, err
	}
	for _, policy := range policies {
		if policy.TargetService == serviceID {
			return policy, nil
		}
	}
	return nil, domain.ErrPolicyNotFound
}

// ListTrafficPolicies lists all traffic policies in a namespace.
func (s *EtcdTrafficPolicyStore) ListTrafficPolicies(ctx context.Context, namespace string) ([]*model.TrafficPolicy, error) {
	prefix := trafficPolicyPrefix + namespace + "/"
	resources, err := s.resources.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("traffic policy store: list %q: %w", prefix, err)
	}

	policies := make([]*model.TrafficPolicy, 0, len(resources))
	for _, resource := range resources {
		policy, err := codec.ResourceToTrafficPolicy(resource)
		if err != nil {
			return nil, fmt.Errorf("traffic policy store: decode list item: %w", err)
		}
		policies = append(policies, policy)
	}
	return policies, nil
}

// DeleteTrafficPolicy removes a traffic policy.
func (s *EtcdTrafficPolicyStore) DeleteTrafficPolicy(ctx context.Context, namespace, name string) error {
	key := trafficPolicyKey(namespace, name)
	if err := s.resources.Delete(ctx, key); err != nil {
		return fmt.Errorf("traffic policy store: delete %q: %w", key, err)
	}
	return nil
}

// WatchTrafficPolicies watches traffic policy changes.
func (s *EtcdTrafficPolicyStore) WatchTrafficPolicies(ctx context.Context) (<-chan outbound.TrafficPolicyEvent, error) {
	resourceCh, err := s.resources.Watch(ctx, trafficPolicyPrefix)
	if err != nil {
		return nil, fmt.Errorf("traffic policy store: watch: %w", err)
	}

	eventCh := make(chan outbound.TrafficPolicyEvent, 64)
	go func() {
		defer close(eventCh)
		for event := range resourceCh {
			policyEvent := outbound.TrafficPolicyEvent{}
			switch event.Type {
			case outbound.ResourceEventPut:
				policyEvent.Type = outbound.TrafficPolicyEventPut
			case outbound.ResourceEventDelete:
				policyEvent.Type = outbound.TrafficPolicyEventDelete
			}
			if event.Resource != nil {
				if policy, err := codec.ResourceToTrafficPolicy(event.Resource); err == nil {
					policyEvent.Policy = policy
				}
			}

			select {
			case eventCh <- policyEvent:
			case <-ctx.Done():
				return
			}
		}
	}()

	return eventCh, nil
}

func (s *EtcdTrafficPolicyStore) listAll(ctx context.Context) ([]*model.TrafficPolicy, error) {
	resources, err := s.resources.List(ctx, trafficPolicyPrefix)
	if err != nil {
		return nil, fmt.Errorf("traffic policy store: list all: %w", err)
	}

	policies := make([]*model.TrafficPolicy, 0, len(resources))
	for _, resource := range resources {
		policy, err := codec.ResourceToTrafficPolicy(resource)
		if err != nil {
			return nil, fmt.Errorf("traffic policy store: decode list item: %w", err)
		}
		policies = append(policies, policy)
	}
	return policies, nil
}

func trafficPolicyKey(namespace, name string) string {
	return trafficPolicyPrefix + namespace + "/" + name
}

// Compile-time interface check.
var _ outbound.TrafficPolicyStore = (*EtcdTrafficPolicyStore)(nil)
