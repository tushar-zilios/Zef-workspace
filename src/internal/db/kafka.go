package db

import (
	"context"
	"crypto/tls"
	"fmt"

	"workspace/src/internal/logger"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/scram"
)

var kafkaWriter *kafka.Writer

// InitKafka initializes a producer-only Kafka connection to the shared Aiven broker also used
// by Zef-backend/Zef-accountant/Zef-cto for the "audit-events" topic consumed by Zef-audit.
// This service never consumes; it only publishes.
func InitKafka(ctx context.Context, broker, username, password string) error {
	if broker == "" {
		logger.LogDB("KAFKA_BROKER not set; skipping Kafka initialization")
		return nil
	}

	var transport *kafka.Transport
	if username != "" && password != "" {
		mechanism, err := scram.Mechanism(scram.SHA256, username, password)
		if err != nil {
			return fmt.Errorf("failed to create scram mechanism: %w", err)
		}
		transport = &kafka.Transport{
			SASL: mechanism,
			TLS:  &tls.Config{InsecureSkipVerify: true},
		}
	}

	kafkaWriter = &kafka.Writer{
		Addr:      kafka.TCP(broker),
		Balancer:  &kafka.LeastBytes{},
		Transport: transport,
	}
	logger.LogDB("Kafka producer initialized.")
	return nil
}

func CloseKafka() {
	if kafkaWriter != nil {
		logger.LogDB("Closing Kafka writer.")
		kafkaWriter.Close()
	}
}

func Publish(ctx context.Context, topic string, key []byte, value []byte) error {
	if kafkaWriter == nil {
		return fmt.Errorf("kafka writer not initialized")
	}
	return kafkaWriter.WriteMessages(ctx, kafka.Message{
		Topic: topic,
		Key:   key,
		Value: value,
	})
}
