package id_test

import (
	"github.com/artificialflow/artificialflow/backend/libs/id"
	"testing"
	"time"
)

func TestResolveSnowflakeNodeID(t *testing.T) {
	tests := []struct {
		name     string
		explicit string
		podName  string
		want     int64
		wantErr  bool
	}{
		{name: "explicit", explicit: "42", podName: "command-3", want: 42},
		{name: "stateful set ordinal zero", podName: "artificialflow-command-0", want: 1},
		{name: "stateful set ordinal one", podName: "artificialflow-command-1", want: 2},
		{name: "standalone default", want: 1},
		{name: "invalid explicit", explicit: "node-1", wantErr: true},
		{name: "explicit out of range", explicit: "1024", wantErr: true},
		{name: "deployment pod name rejected", podName: "artificialflow-command-7d9f8c-x2abc", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := id.ResolveSnowflakeNodeID(tt.explicit, tt.podName)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveSnowflakeNodeID() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ResolveSnowflakeNodeID() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSnowflakeGeneration(t *testing.T) {
	// Initialize default node
	id.InitDefaultNode(1)

	// Generate a few IDs
	id1 := id.GenerateSnowflake()
	id2 := id.GenerateSnowflake()

	if id1 == 0 {
		t.Error("Generated ID should not be 0")
	}
	if id2 == 0 {
		t.Error("Generated ID should not be 0")
	}
	if id1 >= id2 {
		t.Errorf("IDs should be increasing: %d >= %d", id1, id2)
	}

	// Verify uniqueness (simple check)
	count := 1000
	ids := make(map[int64]bool)
	for i := 0; i < count; i++ {
		uid := id.GenerateSnowflake()
		if ids[uid] {
			t.Errorf("Duplicate ID generated: %d", uid)
		}
		ids[uid] = true
	}
}

func TestUUIDv7Generation(t *testing.T) {
	uuid1 := id.GenerateUUIDv7()
	if uuid1 == "" {
		t.Error("Generated UUID should not be empty")
	}

	// Verify it looks like a UUID (length check)
	if len(uuid1) != 36 {
		t.Errorf("Invalid UUID length: %d, expected 36", len(uuid1))
	}

	// Wait a bit to ensure time difference if v7 is time-ordered
	time.Sleep(2 * time.Millisecond)

	uuid2 := id.GenerateUUIDv7()
	if uuid1 == uuid2 {
		t.Error("Generated UUIDs should be unique")
	}

	// Lexicographical comparison for v7 should roughly work for ordering
	if uuid1 >= uuid2 {
		t.Logf("Warning: UUIDv7 might not be strictly ordered strings if fallback v4 used, or clock issues: %s >= %s", uuid1, uuid2)
	}
}
