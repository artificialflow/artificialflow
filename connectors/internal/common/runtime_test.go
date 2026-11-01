package common

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/model"
)

type staticLoader map[string]any

func (l staticLoader) Load(context.Context, int64) (map[string]any, error) { return l, nil }

type staticSecrets map[string]string

func (s staticSecrets) ResolveSecret(_ context.Context, name string) (string, error) {
	value, ok := s[name]
	if !ok {
		return "", errors.New("missing secret")
	}
	return value, nil
}

func TestRuntimeBindsInputsResolvesSecretsRetriesAndCaches(t *testing.T) {
	metrics := &RuntimeMetrics{}
	attempts := 0
	runtime, err := NewRuntime(RuntimeConfig{
		BaseURL:      "http://localhost",
		JobType:      "io.artificialflow.connector.webhook",
		WorkerName:   "test",
		MaxAttempts:  2,
		RetryBackoff: time.Millisecond,
		Loader: staticLoader{
			"webhookUrl":   "https://example.test/hook",
			"webhookToken": "secret://WEBHOOK_TOKEN",
		},
		Secrets: staticSecrets{"WEBHOOK_TOKEN": "resolved-token"},
		Metrics: metrics,
		Execute: func(_ context.Context, inputs map[string]any) (map[string]any, error) {
			attempts++
			if inputs["webhookToken"] != "resolved-token" {
				t.Fatalf("secret was not resolved: %#v", inputs)
			}
			if inputs["artificialflowOperationId"] != "io.artificialflow.connector.webhook:42" {
				t.Fatalf("stable operation id missing: %#v", inputs)
			}
			if attempts == 1 {
				return nil, errors.New("temporary")
			}
			return map[string]any{"ok": true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	job := model.Job{Key: 42, ProcessInstanceKey: 7}
	output, err := runtime.Handle(context.Background(), job)
	if err != nil || output["ok"] != true || attempts != 2 {
		t.Fatalf("output=%#v attempts=%d err=%v", output, attempts, err)
	}
	if _, err := runtime.Handle(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || metrics.IdempotencyHits.Load() != 1 || metrics.Attempts.Load() != 2 {
		t.Fatalf("attempts=%d metrics=%+v", attempts, metrics)
	}
}

func TestRuntimeRejectsMissingAndInvalidInputsBeforeAdapter(t *testing.T) {
	for name, variables := range map[string]map[string]any{
		"missing required": {},
		"invalid number":   {"url": "https://example.test", "timeoutMs": "later"},
	} {
		t.Run(name, func(t *testing.T) {
			called := false
			runtime, err := NewRuntime(RuntimeConfig{
				BaseURL: "http://localhost", JobType: "io.artificialflow.connector.http", WorkerName: "test",
				Loader: staticLoader(variables),
				Execute: func(context.Context, map[string]any) (map[string]any, error) {
					called = true
					return nil, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.Handle(context.Background(), model.Job{Key: 1}); err == nil {
				t.Fatal("expected binding error")
			}
			if called {
				t.Fatal("adapter called with invalid inputs")
			}
		})
	}
}

func TestRuntimeBoundsRetries(t *testing.T) {
	attempts := 0
	runtime, err := NewRuntime(RuntimeConfig{
		BaseURL: "http://localhost", JobType: "io.artificialflow.connector.http", WorkerName: "test",
		MaxAttempts: 99, RetryBackoff: time.Millisecond,
		Loader: staticLoader{"url": "https://example.test"},
		Execute: func(context.Context, map[string]any) (map[string]any, error) {
			attempts++
			return nil, errors.New("failed")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = runtime.Handle(context.Background(), model.Job{Key: 1})
	if attempts != maxRuntimeAttempts {
		t.Fatalf("attempts=%d want=%d", attempts, maxRuntimeAttempts)
	}
}
