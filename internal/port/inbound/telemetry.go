package inbound

import (
	"context"

	"github.com/esachser/omnimeshcontrol/internal/domain/model"
)

// MetricsTargetProvider defines the inbound port for listing active agent
// scrape targets in Prometheus HTTP service discovery format (RFC-0005 §9.1).
type MetricsTargetProvider interface {
	// ListMetricsTargets returns target groups for all currently connected agents.
	ListMetricsTargets(ctx context.Context) ([]model.MetricTargetGroup, error)
}
