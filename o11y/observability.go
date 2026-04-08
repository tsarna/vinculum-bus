package o11y

import (
	"context"
)

// MetricsPublisher defines the minimal interface needed by StandaloneExporter
// to publish metrics events. This avoids circular dependencies with the bus package.
type MetricsPublisher interface {
	Publish(ctx context.Context, topic string, message any) error
}
