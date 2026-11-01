package messaging

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
)

const (
	defaultTopicPartitions     = 3
	defaultTopicReplication    = 1
	defaultTopicReadyTimeout   = 60 * time.Second
	defaultTopicReadyPollEvery = 1 * time.Second
)

// EnsureTopicsReady creates the required Kafka topics (if missing) and waits until
// each topic is visible with at least one partition. Fresh Debezium installs create
// CDC topics asynchronously; kafka-go consumer groups that join before topics exist
// can receive an empty assignment and never recover without a restart.
// Existing topics keep their partition count; partitions <= 0 uses the default.
func EnsureTopicsReady(ctx context.Context, brokers []string, topics []string, partitions int, waitTimeout time.Duration) error {
	normalized := uniqueTopics(topics)
	if len(brokers) == 0 {
		return fmt.Errorf("kafka brokers are required")
	}
	if len(normalized) == 0 {
		return fmt.Errorf("kafka topics are required")
	}
	if waitTimeout <= 0 {
		waitTimeout = defaultTopicReadyTimeout
	}

	if partitions <= 0 {
		partitions = defaultTopicPartitions
	}
	if err := createKafkaTopics(ctx, brokers, normalized, partitions); err != nil {
		return err
	}
	return waitForKafkaTopics(ctx, brokers, normalized, waitTimeout)
}

func TopicsHaveBacklog(ctx context.Context, brokers []string, topics []string) (bool, error) {
	normalized := uniqueTopics(topics)
	if len(brokers) == 0 || len(normalized) == 0 {
		return false, nil
	}

	conn, err := dialKafka(ctx, brokers[0])
	if err != nil {
		return false, err
	}
	defer conn.Close()

	for _, topic := range normalized {
		partitions, err := conn.ReadPartitions(topic)
		if err != nil {
			if isUnknownTopicError(err) {
				continue
			}
			return false, fmt.Errorf("read partitions for %q: %w", topic, err)
		}
		for _, partition := range partitions {
			first, last, err := readPartitionOffsets(ctx, brokers, topic, partition.ID)
			if err != nil {
				return false, err
			}
			if last > first {
				return true, nil
			}
		}
	}
	return false, nil
}

func createKafkaTopics(ctx context.Context, brokers []string, topics []string, partitions int) error {
	controller, err := dialKafkaController(ctx, brokers)
	if err != nil {
		return err
	}
	defer controller.Close()

	configs := make([]kafka.TopicConfig, 0, len(topics))
	for _, topic := range topics {
		configs = append(configs, kafka.TopicConfig{
			Topic:             topic,
			NumPartitions:     partitions,
			ReplicationFactor: defaultTopicReplication,
		})
	}

	err = controller.CreateTopics(configs...)
	if err == nil || isIgnorableTopicCreateError(err) {
		return nil
	}
	return fmt.Errorf("create kafka topics: %w", err)
}

func waitForKafkaTopics(ctx context.Context, brokers []string, topics []string, waitTimeout time.Duration) error {
	deadline := time.Now().Add(waitTimeout)
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		conn, err := dialKafka(ctx, brokers[0])
		if err != nil {
			lastErr = err
		} else {
			missing := make([]string, 0)
			for _, topic := range topics {
				partitions, readErr := conn.ReadPartitions(topic)
				if readErr != nil {
					lastErr = readErr
					missing = append(missing, topic)
					continue
				}
				if len(partitions) == 0 {
					missing = append(missing, topic)
				}
			}
			_ = conn.Close()
			if len(missing) == 0 {
				return nil
			}
			lastErr = fmt.Errorf("topics not ready: %s", strings.Join(missing, ", "))
		}

		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("timed out waiting for kafka topics: %w", lastErr)
			}
			return fmt.Errorf("timed out waiting for kafka topics")
		}

		timer := time.NewTimer(defaultTopicReadyPollEvery)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func readPartitionOffsets(ctx context.Context, brokers []string, topic string, partition int) (int64, int64, error) {
	partitionConn, err := kafka.DialPartition(ctx, "tcp", brokers[0], kafka.Partition{
		Topic: topic,
		ID:    partition,
	})
	if err != nil {
		return 0, 0, err
	}
	defer partitionConn.Close()

	first, err := partitionConn.ReadFirstOffset()
	if err != nil {
		return 0, 0, err
	}
	last, err := partitionConn.ReadLastOffset()
	if err != nil {
		return 0, 0, err
	}
	return first, last, nil
}

func dialKafkaController(ctx context.Context, brokers []string) (*kafka.Conn, error) {
	conn, err := dialKafka(ctx, brokers[0])
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	controller, err := conn.Controller()
	if err != nil {
		return nil, fmt.Errorf("resolve kafka controller: %w", err)
	}
	address := net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port))
	return dialKafka(ctx, address)
}

func dialKafka(ctx context.Context, address string) (*kafka.Conn, error) {
	dialer := &kafka.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("dial kafka at %s: %w", address, err)
	}
	return conn, nil
}

func uniqueTopics(topics []string) []string {
	seen := make(map[string]struct{}, len(topics))
	out := make([]string, 0, len(topics))
	for _, topic := range topics {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			continue
		}
		if _, ok := seen[topic]; ok {
			continue
		}
		seen[topic] = struct{}{}
		out = append(out, topic)
	}
	return out
}

func isIgnorableTopicCreateError(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, kafka.TopicAlreadyExists) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "already exists") ||
		strings.Contains(message, "topic with this name already exists")
}

func isUnknownTopicError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, kafka.UnknownTopicOrPartition) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unknown topic") ||
		strings.Contains(message, "unknown_topic_or_partition")
}
