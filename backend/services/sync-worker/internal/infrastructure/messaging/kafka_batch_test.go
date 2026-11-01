package messaging

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	pb "github.com/artificialflow/artificialflow/backend/api/v1/go"
	"github.com/artificialflow/artificialflow/backend/services/sync-worker/internal/application"
	"github.com/artificialflow/artificialflow/backend/services/sync-worker/internal/domain/repository"
	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"
)

type recordingRepo struct {
	calls        []string
	failBulkIDs  map[string]bool
	bulkTransErr error
}

func (r *recordingRepo) Upsert(_ context.Context, _ string, id string, _ map[string]any) error {
	r.calls = append(r.calls, "upsert:"+id)
	return nil
}

func (r *recordingRepo) Delete(_ context.Context, _ string, id string) error {
	r.calls = append(r.calls, "delete:"+id)
	return nil
}

func (r *recordingRepo) UpdateWithScript(_ context.Context, _ string, id string, _ string, _ map[string]any) error {
	r.calls = append(r.calls, "update:"+id)
	return nil
}

func (r *recordingRepo) UpsertWithScript(_ context.Context, _ string, id string, _ string, _ map[string]any, _ map[string]any) error {
	r.calls = append(r.calls, "upsert-script:"+id)
	return nil
}

func (r *recordingRepo) Bulk(_ context.Context, ops []repository.WriteOp) ([]repository.BulkItemResult, error) {
	call := "bulk:"
	for i, op := range ops {
		if i > 0 {
			call += ","
		}
		call += op.ID
	}
	r.calls = append(r.calls, call)
	if r.bulkTransErr != nil {
		return nil, r.bulkTransErr
	}
	out := make([]repository.BulkItemResult, len(ops))
	for i, op := range ops {
		out[i] = repository.BulkItemResult{Status: 200}
		if r.failBulkIDs[op.ID] {
			out[i] = repository.BulkItemResult{Status: 400, Error: errors.New("mapping rejected")}
		}
	}
	return out, nil
}

func jobActivatedMessage(t *testing.T, key int64, eventID string) kafka.Message {
	t.Helper()
	payload, err := proto.Marshal(&pb.JobActivated{Key: key, Worker: "w"})
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{
		Topic: "workflow.events.v1", Offset: key, Value: payload,
		Headers: []kafka.Header{
			{Key: "type", Value: []byte("JobActivated")},
			{Key: "event_id", Value: []byte(eventID)},
			{Key: "tenant_id", Value: []byte("tenant-a")},
		},
	}
}

func debeziumMessage(key int) kafka.Message {
	return kafka.Message{
		Topic: "artificialflow.public.process_instance", Offset: int64(key),
		Value: []byte(fmt.Sprintf(`{"before":null,"after":{"key":%d},"op":"c"}`, key)),
	}
}

func newBatchTestConsumer(repo *recordingRepo) *KafkaConsumer {
	service := application.NewSyncService(repo, "artificialflow")
	return NewKafkaConsumer(Config{MaxProcessRetries: 0, RetryBackoff: 1}, service)
}

func TestProcessBatchWritesEventRunsInOneBulkPreservingOrder(t *testing.T) {
	repo := &recordingRepo{}
	consumer := newBatchTestConsumer(repo)

	consumer.processBatch(context.Background(), []kafka.Message{
		jobActivatedMessage(t, 1, "e1"),
		jobActivatedMessage(t, 2, "e2"),
		debeziumMessage(3),
		jobActivatedMessage(t, 4, "e4"),
	})

	want := []string{"bulk:1,2", "upsert:3", "bulk:4"}
	if !reflect.DeepEqual(repo.calls, want) {
		t.Fatalf("calls = %v, want %v", repo.calls, want)
	}
	if got := consumer.successCount.Load(); got != 4 {
		t.Fatalf("success count = %d, want 4", got)
	}
}

func TestProcessBatchRetriesOnlyFailedBulkItemsIndividually(t *testing.T) {
	repo := &recordingRepo{failBulkIDs: map[string]bool{"2": true}}
	consumer := newBatchTestConsumer(repo)

	consumer.processBatch(context.Background(), []kafka.Message{
		jobActivatedMessage(t, 1, "e1"),
		jobActivatedMessage(t, 2, "e2"),
		jobActivatedMessage(t, 3, "e3"),
	})

	want := []string{"bulk:1,2,3", "upsert-script:2"}
	if !reflect.DeepEqual(repo.calls, want) {
		t.Fatalf("calls = %v, want %v", repo.calls, want)
	}
	if got := consumer.successCount.Load(); got != 3 {
		t.Fatalf("success count = %d, want 3", got)
	}
}

func TestProcessBatchFallsBackPerMessageOnBulkTransportError(t *testing.T) {
	repo := &recordingRepo{bulkTransErr: errors.New("connection reset")}
	consumer := newBatchTestConsumer(repo)

	consumer.processBatch(context.Background(), []kafka.Message{
		jobActivatedMessage(t, 1, "e1"),
		jobActivatedMessage(t, 2, "e2"),
	})

	want := []string{"bulk:1,2", "upsert-script:1", "upsert-script:2"}
	if !reflect.DeepEqual(repo.calls, want) {
		t.Fatalf("calls = %v, want %v", repo.calls, want)
	}
}

func TestProcessBatchSkipsAlreadyProjectedEvents(t *testing.T) {
	repo := &recordingRepo{}
	consumer := newBatchTestConsumer(repo)

	consumer.processBatch(context.Background(), []kafka.Message{jobActivatedMessage(t, 1, "e1")})
	consumer.processBatch(context.Background(), []kafka.Message{
		jobActivatedMessage(t, 1, "e1"),
		jobActivatedMessage(t, 2, "e2"),
	})

	want := []string{"bulk:1", "bulk:2"}
	if !reflect.DeepEqual(repo.calls, want) {
		t.Fatalf("calls = %v, want %v", repo.calls, want)
	}
}

func TestRecentEventSetEvictsOldestBeyondCapacity(t *testing.T) {
	set := newRecentEventSet(2)
	set.add("a")
	set.add("b")
	set.add("a")
	set.add("c")
	if set.contains("a") {
		t.Fatal("oldest key must be evicted once capacity is exceeded")
	}
	if !set.contains("b") || !set.contains("c") {
		t.Fatal("recent keys must be retained")
	}
	if len(set.keys) != 2 {
		t.Fatalf("set size = %d, want 2", len(set.keys))
	}
}
