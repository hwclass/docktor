package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
)

func main() {
	// Configuration from environment
	natsURL := getEnv("NATS_URL", "nats://localhost:4222")
	stream := getEnv("STREAM", "EVENTS")
	subject := getEnv("SUBJECT", "events.web")
	rate := getEnvInt("RATE", 100)
	burstRate := getEnvInt("BURST_RATE", 500)
	burstInterval := getEnvInt("BURST_INTERVAL", 60)
	burstDuration := getEnvInt("BURST_DURATION", 10)

	log.Printf("Starting NATS producer")
	log.Printf("  NATS URL: %s", natsURL)
	log.Printf("  Stream: %s", stream)
	log.Printf("  Subject: %s", subject)
	log.Printf("  Baseline rate: %d msgs/sec", rate)
	log.Printf("  Burst rate: %d msgs/sec", burstRate)

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

	// Start publishing
	log.Println("Starting message production...")
	msgCount := 0
	startTime := time.Now()
	lastBurst := time.Now()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	currentRate := rate

	for range ticker.C {
		// Check if we should burst
		if time.Since(lastBurst) >= time.Duration(burstInterval)*time.Second {
			log.Printf("🔥 Starting burst mode for %d seconds", burstDuration)
			currentRate = burstRate
			lastBurst = time.Now()

			// Burst for specified duration
			go func() {
				time.Sleep(time.Duration(burstDuration) * time.Second)
				currentRate = rate
				log.Printf("✅ Burst complete, returning to baseline rate")
			}()
		}

		// Publish messages at current rate
		for i := 0; i < currentRate; i++ {
			msg := map[string]interface{}{
				"id":        msgCount,
				"timestamp": time.Now().Unix(),
				"data":      fmt.Sprintf("message-%d", msgCount),
			}

			data, _ := json.Marshal(msg)
			_, err := js.Publish(subject, data)
			if err != nil {
				log.Printf("⚠️  Failed to publish message: %v", err)
			} else {
				msgCount++
			}
		}

		// Log stats every 10 seconds
		if msgCount%1000 == 0 {
			elapsed := time.Since(startTime).Seconds()
			avgRate := float64(msgCount) / elapsed
			log.Printf("📊 Total: %d msgs | Avg rate: %.1f msgs/sec | Current: %d msgs/sec",
				msgCount, avgRate, currentRate)
		}
	}
}

func ensureStream(js nats.JetStreamContext, stream, subject string) {
	// Check if stream exists
	_, err := js.StreamInfo(stream)
	if err != nil {
		// Create stream
		log.Printf("Creating stream: %s", stream)
		_, err = js.AddStream(&nats.StreamConfig{
			Name:     stream,
			Subjects: []string{subject},
			Storage:  nats.FileStorage,
			MaxAge:   time.Hour * 24, // Keep messages for 24 hours
		})
		if err != nil {
			log.Fatalf("Failed to create stream: %v", err)
		}
		log.Printf("✅ Stream created successfully")
	} else {
		log.Printf("✅ Stream already exists: %s", stream)
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
