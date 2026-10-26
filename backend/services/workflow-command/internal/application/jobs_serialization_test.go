package application

import (
	"strings"
	"testing"
)

func TestSerializeJobVariables(t *testing.T) {
	got, err := serializeJobVariables(map[string]any{"approved": true, "count": 2})
	if err != nil {
		t.Fatalf("serialize variables: %v", err)
	}
	if got != `{"approved":true,"count":2}` {
		t.Fatalf("unexpected serialized variables: %s", got)
	}

	empty, err := serializeJobVariables(nil)
	if err != nil || empty != "{}" {
		t.Fatalf("empty variables = %q, err=%v", empty, err)
	}
}

func TestSerializeJobVariablesRejectsUnsupportedValues(t *testing.T) {
	_, err := serializeJobVariables(map[string]any{"callback": func() {}})
	if err == nil || !strings.Contains(err.Error(), "serialize completed job variables") {
		t.Fatalf("expected contextual serialization error, got %v", err)
	}
}
