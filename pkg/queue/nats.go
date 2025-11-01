package queue

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// NATSProvider implements the Provider interface for NATS JetStream
type NATSProvider struct {
	url        string
	stream     string
	consumer   string
	subject    string
	jetstream  bool
	conn       *nats.Conn
	js         nats.JetStreamContext
}

// NewNATSProvider creates a new NATS queue provider
func NewNATSProvider(cfg Config) (Provider, error) {
	provider := &NATSProvider{
		url:       cfg.URL,
		stream:    cfg.Attributes["stream"],
		consumer:  cfg.Attributes["consumer"],
		subject:   cfg.Attributes["subject"],
		jetstream: cfg.Attributes["jetstream"] == "true",
	}

	// Validate required attributes
	if !provider.jetstream {
		return nil, fmt.Errorf("NATS provider requires JetStream (jetstream: true)")
	}
	if provider.stream == "" {
		return nil, fmt.Errorf("NATS provider requires 'stream' attribute")
	}
	if provider.consumer == "" {
		return nil, fmt.Errorf("NATS provider requires 'consumer' attribute")
	}

	return provider, nil
}

// Connect establishes connection to NATS
func (n *NATSProvider) Connect() error {
	var err error
	n.conn, err = nats.Connect(n.url, nats.Timeout(5*time.Second))
	if err != nil {
		return fmt.Errorf("failed to connect to NATS at %s: %w", n.url, err)
	}

	// Get JetStream context
	n.js, err = n.conn.JetStream()
	if err != nil {
		n.conn.Close()
		return fmt.Errorf("failed to get JetStream context: %w", err)
	}

	return nil
}

// GetMetrics collects queue metrics from NATS JetStream
func (n *NATSProvider) GetMetrics(windowSec int) (*Metrics, error) {
	if n.js == nil {
		return nil, fmt.Errorf("not connected to NATS")
	}

	// Get stream info (initial sample)
	streamInfo1, err := n.js.StreamInfo(n.stream)
	if err != nil {
		return nil, fmt.Errorf("failed to get stream info for '%s': %w", n.stream, err)
	}

	// Get consumer info (initial sample)
	consumerInfo1, err := n.js.ConsumerInfo(n.stream, n.consumer)
	if err != nil {
		return nil, fmt.Errorf("failed to get consumer info for '%s/%s': %w", n.stream, n.consumer, err)
	}

	// Wait for window duration to calculate rates
	time.Sleep(time.Duration(windowSec) * time.Second)

	// Get second samples
	streamInfo2, err := n.js.StreamInfo(n.stream)
	if err != nil {
		return nil, fmt.Errorf("failed to get stream info (second sample): %w", err)
	}

	consumerInfo2, err := n.js.ConsumerInfo(n.stream, n.consumer)
	if err != nil {
		return nil, fmt.Errorf("failed to get consumer info (second sample): %w", err)
	}

	// Calculate metrics
	metrics := &Metrics{
		Timestamp: time.Now(),
		Custom:    make(map[string]float64),
	}

	// Backlog: messages pending in consumer
	metrics.Backlog = float64(consumerInfo2.NumPending)

	// Lag: stream sequence lag (approximate)
	lag := int64(streamInfo2.State.LastSeq) - int64(consumerInfo2.Delivered.Stream)
	if lag < 0 {
		lag = 0
	}
	metrics.Lag = float64(lag)

	// Rate in: msgs/sec published to stream
	msgDelta := streamInfo2.State.Msgs - streamInfo1.State.Msgs
	metrics.RateIn = float64(msgDelta) / float64(windowSec)

	// Rate out: msgs/sec consumed (acked)
	ackDelta := consumerInfo2.AckFloor.Stream - consumerInfo1.AckFloor.Stream
	metrics.RateOut = float64(ackDelta) / float64(windowSec)

	// Additional NATS-specific metrics
	metrics.Custom["num_ack_pending"] = float64(consumerInfo2.NumAckPending)
	metrics.Custom["num_redelivered"] = float64(consumerInfo2.NumRedelivered)
	metrics.Custom["num_waiting"] = float64(consumerInfo2.NumWaiting)

	return metrics, nil
}

// Validate checks if the stream and consumer exist
func (n *NATSProvider) Validate() error {
	if n.js == nil {
		if err := n.Connect(); err != nil {
			return err
		}
	}

	// Check if stream exists
	_, err := n.js.StreamInfo(n.stream)
	if err != nil {
		return fmt.Errorf("stream '%s' not found or inaccessible: %w", n.stream, err)
	}

	// Check if consumer exists
	_, err = n.js.ConsumerInfo(n.stream, n.consumer)
	if err != nil {
		return fmt.Errorf("consumer '%s' not found in stream '%s': %w", n.consumer, n.stream, err)
	}

	return nil
}

// Close closes the NATS connection
func (n *NATSProvider) Close() error {
	if n.conn != nil {
		n.conn.Close()
		n.conn = nil
		n.js = nil
	}
	return nil
}

// Register NATS provider on package init
func init() {
	Register("nats", NewNATSProvider)
}
