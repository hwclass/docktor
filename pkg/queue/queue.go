package queue

import "time"

// Metrics represents queue metrics collected over a time window
type Metrics struct {
	Backlog  float64           // Messages pending in queue/consumer
	Lag      float64           // Consumer lag (if applicable)
	RateIn   float64           // Messages/sec published
	RateOut  float64           // Messages/sec consumed
	Custom   map[string]float64 // Additional vendor-specific metrics
	Timestamp time.Time         // When metrics were collected
}

// Config represents queue backend configuration
type Config struct {
	Kind       string            // "nats", "kafka", "rabbitmq", "sqs"
	URL        string            // Connection URL
	Attributes map[string]string // Vendor-specific attributes
}

// Provider interface for queue backend implementations
type Provider interface {
	// Connect establishes connection to the queue backend
	Connect() error

	// GetMetrics collects queue metrics over the specified window
	GetMetrics(windowSec int) (*Metrics, error)

	// Close closes the connection and cleans up resources
	Close() error

	// Validate checks if the queue/stream/consumer configuration is valid
	Validate() error
}

// Registry holds all registered queue providers
var registry = make(map[string]func(Config) (Provider, error))

// Register adds a queue provider to the registry
func Register(kind string, factory func(Config) (Provider, error)) {
	registry[kind] = factory
}

// NewProvider creates a queue provider instance for the given config
func NewProvider(cfg Config) (Provider, error) {
	factory, exists := registry[cfg.Kind]
	if !exists {
		return nil, &UnsupportedKindError{Kind: cfg.Kind}
	}
	return factory(cfg)
}

// UnsupportedKindError represents an unsupported queue kind
type UnsupportedKindError struct {
	Kind string
}

func (e *UnsupportedKindError) Error() string {
	return "unsupported queue kind: " + e.Kind
}
