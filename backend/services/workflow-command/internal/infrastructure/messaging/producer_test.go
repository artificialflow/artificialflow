package messaging

import (
	"context"
	workflowapi "github.com/artificialflow/artificialflow/backend/api/v1/go"
	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestNewEventEnvelopeUsesContextTenant(t *testing.T) {
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{TenantID: "tenant-a"})
	envelope := newEventEnvelope(ctx, &workflowapi.JobActivated{Key: 42}, "JobActivated")
	if envelope.TenantID != "tenant-a" {
		t.Fatalf("tenant = %q, want tenant-a", envelope.TenantID)
	}
	if envelope.AggregateType != "job" || envelope.AggregateID != "42" || envelope.EventID == "" {
		t.Fatalf("unexpected envelope identity: %#v", envelope)
	}
}

func TestAggregateIDForEvent(t *testing.T) {
	tests := []struct {
		name  string
		event proto.Message
		want  string
	}{
		{
			name:  "process instance",
			event: &workflowapi.ProcessInstanceCreated{Key: 1001},
			want:  "process-instance:1001",
		},
		{
			name:  "job event",
			event: &workflowapi.JobActivated{Key: 2002},
			want:  "job:2002",
		},
		{
			name:  "variable with process instance",
			event: &workflowapi.VariableUpdated{ProcessInstanceKey: 3003, ScopeKey: 9},
			want:  "process-instance:3003",
		},
		{
			name:  "unknown event type",
			event: &emptypb.Empty{},
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := aggregateIDForEvent(tc.event)
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}
