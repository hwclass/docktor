package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
)

func main() {
	// Configuration from environment
	natsURL := getEnv("NATS_URL", "nats://localhost:4222")
	stream := getEnv("STREAM", "EVENTS")
	consumer := getEnv("CONSUMER", "WEB_WORKERS")
	subject := getEnv("SUBJECT", "events.web")
	processTimeMs := getEnvInt("PROCESS_TIME_MS", 50)
	batchSize := getEnvInt("BATCH_SIZE", 10)

	hostname, _ := os.Hostname()
	log.Printf("Starting NATS consumer: %s", hostname)
	log.Printf("  NATS URL: %s", natsURL)
	log.Printf("  Stream: %s", stream)
	log.Printf("  Consumer: %s", consumer)
	log.Printf("  Subject: %s", subject)
	log.Printf("  Process time: %dms per message", processTimeMs)

	// Connect to NATS
	nc, err := nats.Connect(natsURL,
		nats.Timeout(10*time.Second),
		nats.ReconnectWait(2*time.Second),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	// Get JetStream context
	js, err := nc.JetStream()
	if err != nil {
		log.Fatalf("Failed to get JetStream context: %v", err)
	}

	// Ensure stream exists
	ensureStream(js, stream, subject)

	// Ensure consumer exists
	ensureConsumer(js, stream, consumer)

	// Subscribe to messages
	log.Println("Starting message consumption...")
	sub, err := js.PullSubscribe(subject, consumer,
		nats.ManualAck(),
		nats.AckWait(30*time.Second),
	)
	if err != nil {
		log.Fatalf("Failed to subscribe: %v", err)
	}

	// Handle shutdown gracefully
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	msgCount := 0
	startTime := time.Now()

	// Main processing loop
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-done:
				log.Println("Shutting down consumer...")
				return
			case <-ticker.C:
				// Fetch and process messages
				msgs, err := sub.Fetch(batchSize, nats.MaxWait(time.Second))
				if err != nil {
					if err != nats.ErrTimeout {
						log.Printf("⚠️  Fetch error: %v", err)
					}
					continue
				}

				for _, msg := range msgs {
					// Process message
					processMessage(msg.Data, processTimeMs)

					// Ack message
					if err := msg.Ack(); err != nil {
						log.Printf("⚠️  Failed to ack message: %v", err)
					} else {
						msgCount++
					}
				}

				// Log stats every 100 messages
				if msgCount%100 == 0 && msgCount > 0 {
					elapsed := time.Since(startTime).Seconds()
					avgRate := float64(msgCount) / elapsed
					log.Printf("[%s] 📊 Processed: %d msgs | Avg rate: %.1f msgs/sec",
						hostname, msgCount, avgRate)
				}
			}
		}
	}()

	<-done
	log.Printf("Consumer stopped. Total processed: %d messages", msgCount)
}

func processMessage(data []byte, processTimeMs int) {
	// Parse message
	var msg map[string]interface{}
	if err := json.Unmarshal(data, &msg); err != nil {
		log.Printf("⚠️  Failed to parse message: %v", err)
		return
	}

	// Simulate processing time
	time.Sleep(time.Duration(processTimeMs) * time.Millisecond)
}

func ensureStream(js nats.JetStreamContext, stream, subject string) {
	_, err := js.StreamInfo(stream)
	if err != nil {
		log.Printf("Creating stream: %s", stream)
		_, err = js.AddStream(&nats.StreamConfig{
			Name:     stream,
			Subjects: []string{subject},
			Storage:  nats.FileStorage,
			MaxAge:   time.Hour * 24,
		})
		if err != nil {
			log.Fatalf("Failed to create stream: %v", err)
		}
		log.Printf("✅ Stream created successfully")
	} else {
		log.Printf("✅ Stream already exists: %s", stream)
	}
}

func ensureConsumer(js nats.JetStreamContext, stream, consumer string) {
	_, err := js.ConsumerInfo(stream, consumer)
	if err != nil {
		log.Printf("Creating consumer: %s", consumer)
		_, err = js.AddConsumer(stream, &nats.ConsumerConfig{
			Durable:   consumer,
			AckPolicy: nats.AckExplicitPolicy,
			AckWait:   30 * time.Second,
			MaxDeliver: 3,
		})
		if err != nil {
			log.Fatalf("Failed to create consumer: %v", err)
		}
		log.Printf("✅ Consumer created successfully")
	} else {
		log.Printf("✅ Consumer already exists: %s", consumer)
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}
