package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	pb "github.com/artificialflow/artificialflow/backend/api/v1/go"
	"github.com/artificialflow/artificialflow/backend/libs/auth"
	domainmodel "github.com/artificialflow/artificialflow/backend/services/sync-worker/internal/domain/model"
	"github.com/artificialflow/artificialflow/backend/services/sync-worker/internal/domain/repository"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type recordingRepository struct {
	index   string
	id      string
	doc     map[string]any
	deleted bool
	err     error
}

func (r *recordingRepository) Upsert(_ context.Context, index, id string, doc map[string]any) error {
	r.index = index
	r.id = id
	r.doc = doc
	return r.err
}

func (r *recordingRepository) Delete(_ context.Context, index, id string) error {
	r.index = index
	r.id = id
	r.deleted = true
	return r.err
}

func (r *recordingRepository) UpdateWithScript(_ context.Context, index, _ string, _ string, _ map[string]any) error {
	r.index = index
	return nil
}

func (r *recordingRepository) UpsertWithScript(_ context.Context, index, id string, _ string, _ map[string]any, upsert map[string]any) error {
	r.index = index
	r.id = id
	r.doc = upsert
	return r.err
}

func (r *recordingRepository) Bulk(_ context.Context, ops []repository.WriteOp) ([]repository.BulkItemResult, error) {
	out := make([]repository.BulkItemResult, len(ops))
	for i, op := range ops {
		if err := r.apply(op); err != nil {
			out[i] = repository.BulkItemResult{Status: 500, Error: err}
			continue
		}
		out[i] = repository.BulkItemResult{Status: 200}
	}
	return out, r.err
}

func (r *recordingRepository) apply(op repository.WriteOp) error {
	switch op.Kind {
	case repository.WriteIndex:
		return r.Upsert(context.Background(), op.Index, op.ID, op.Doc)
	case repository.WriteDelete:
		return r.Delete(context.Background(), op.Index, op.ID)
	case repository.WriteUpdateScript:
		return r.UpdateWithScript(context.Background(), op.Index, op.ID, op.Script, op.Params)
	case repository.WriteUpsertScript:
		return r.UpsertWithScript(context.Background(), op.Index, op.ID, op.Script, op.Params, op.Upsert)
	default:
		return fmt.Errorf("unsupported write kind %d", op.Kind)
	}
}

func TestSyncServiceDefaultsToCanonicalIndexPrefix(t *testing.T) {
	service := NewSyncService(&recordingRepository{}, "")

	if got := service.indexNameForTopic("artificialflow.public.process_instance"); got != "artificialflow-process_instance" {
		t.Fatalf("expected canonical index, got %q", got)
	}
	if got := service.indexNameForTopic("artificialflow.public.job"); got != "artificialflow-job" {
		t.Fatalf("source topic prefix must not control the write index, got %q", got)
	}
}

func TestSyncServicePreservesExplicitLegacyIndexOverride(t *testing.T) {
	service := NewSyncService(&recordingRepository{}, "artificialflow")

	if got := service.indexNameForTopic("artificialflow.public.process"); got != "artificialflow-process" {
		t.Fatalf("expected explicit legacy index override, got %q", got)
	}
}

func TestProcessMessageMirrorsCreateAndDelete(t *testing.T) {
	repo := &recordingRepository{}
	service := NewSyncService(repo, "")
	ctx := context.Background()

	if err := service.ProcessMessage(ctx, "artificialflow.public.job", domainmodel.DebeziumMessage{
		Op:    "c",
		After: json.RawMessage(`{"key":42,"state":"CREATED"}`),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if repo.index != "artificialflow-job" || repo.id != "42" || repo.doc["state"] != "CREATED" {
		t.Fatalf("unexpected upsert: index=%q id=%q doc=%v", repo.index, repo.id, repo.doc)
	}

	if err := service.ProcessMessage(ctx, "artificialflow.public.job", domainmodel.DebeziumMessage{
		Op:     "d",
		Before: json.RawMessage(`{"key":42}`),
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !repo.deleted || repo.id != "42" {
		t.Fatalf("unexpected delete: deleted=%v id=%q", repo.deleted, repo.id)
	}
}

func TestProcessMessageStoresVariableValuesAsCanonicalJSON(t *testing.T) {
	for name, value := range map[string]string{
		"boolean": `true`,
		"number":  `42`,
		"object":  `{"approved":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			repo := &recordingRepository{}
			payload := json.RawMessage(`{"scope_key":7,"process_instance_key":9,"name":"input","value":` + value + `}`)
			if err := NewSyncService(repo, "").ProcessMessage(
				context.Background(),
				"artificialflow.public.variable",
				domainmodel.DebeziumMessage{Op: "c", After: payload},
			); err != nil {
				t.Fatal(err)
			}
			if got := repo.doc["value"]; got != value {
				t.Fatalf("stored value = %#v, want canonical JSON %q", got, value)
			}
		})
	}
}

func TestJobCompletedCanCreateOutOfOrderProjection(t *testing.T) {
	repo := &recordingRepository{}
	service := NewSyncService(repo, "")
	event := &pb.JobCompleted{
		Key: 42, Worker: "worker-a", UpdatedAt: timestamppb.Now(),
	}
	payload, err := proto.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{TenantID: "tenant-a"})

	if err := service.ProcessEvent(ctx, "JobCompleted", payload); err != nil {
		t.Fatal(err)
	}
	if repo.index != "artificialflow-job" || repo.id != "42" {
		t.Fatalf("unexpected projection target: %s/%s", repo.index, repo.id)
	}
	if repo.doc["state"] != "COMPLETED" || repo.doc["tenant_id"] != "tenant-a" {
		t.Fatalf("unexpected scripted upsert fallback: %v", repo.doc)
	}
}

func TestPlanEventVariableUpdatedPreservesTwoOpOrder(t *testing.T) {
	service := NewSyncService(&recordingRepository{}, "")
	event := &pb.VariableUpdated{
		ProcessInstanceKey: 9,
		ScopeKey:           7,
		Name:               "input",
		Value:              `"ok"`,
		UpdatedAt:          timestamppb.Now(),
	}
	payload, err := proto.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{TenantID: "tenant-a"})

	ops, err := service.PlanEvent(ctx, "VariableUpdated", payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(ops))
	}
	if ops[0].Kind != repository.WriteIndex || !strings.HasSuffix(ops[0].Index, "variable") {
		t.Fatalf("op0 = %#v", ops[0])
	}
	if ops[1].Kind != repository.WriteUpsertScript || !strings.HasSuffix(ops[1].Index, "process_instance") {
		t.Fatalf("op1 = %#v", ops[1])
	}
	if ops[1].ID != "9" {
		t.Fatalf("expected process instance id 9, got %q", ops[1].ID)
	}
}

func TestProcessMessageFailureBoundaries(t *testing.T) {
	service := NewSyncService(&recordingRepository{}, "")
	tests := []struct {
		name string
		msg  domainmodel.DebeziumMessage
		want string
	}{
		{name: "malformed document", msg: domainmodel.DebeziumMessage{Op: "c", After: json.RawMessage(`{`)}, want: "extract id/doc"},
		{name: "missing id", msg: domainmodel.DebeziumMessage{Op: "c", After: json.RawMessage(`{"name":"x"}`)}, want: "unable to determine document id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := service.ProcessMessage(context.Background(), "artificialflow.public.job", tc.msg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}

	repo := &recordingRepository{err: errors.New("storage unavailable")}
	err := NewSyncService(repo, "").ProcessMessage(context.Background(), "artificialflow.public.job", domainmodel.DebeziumMessage{
		Op: "u", After: json.RawMessage(`{"key":7}`),
	})
	if err == nil || !strings.Contains(err.Error(), "upsert failed") {
		t.Fatalf("expected repository error, got %v", err)
	}
}
