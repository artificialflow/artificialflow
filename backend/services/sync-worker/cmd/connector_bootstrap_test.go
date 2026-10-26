package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/logger"
)

func TestEnsureConnectorBootstrap_Disabled(t *testing.T) {
	err := ensureConnectorBootstrap(context.Background(), logger.New("sync-worker-test"), connectorBootstrapOptions{
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("expected no error when disabled, got %v", err)
	}
}

func TestEnsureConnectorBootstrap_RejectsRunningBlockedConnector(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/connectors":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`["blocked-postgres-connector"]`))
		case "/connectors/blocked-postgres-connector/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"blocked-postgres-connector","connector":{"state":"RUNNING"},"tasks":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	err := ensureConnectorBootstrap(context.Background(), logger.New("sync-worker-test"), connectorBootstrapOptions{
		Enabled:               true,
		ConnectURL:            server.URL,
		ConnectorName:         "artificialflow-postgres-connector",
		ConnectorJSON:         `{"config":{"connector.class":"io.debezium.connector.postgresql.PostgresConnector"}}`,
		BlockedConnectorNames: []string{"blocked-postgres-connector"},
		WaitTimeout:           time.Second,
		PollInterval:          time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "conflicting connector") {
		t.Fatalf("expected running blocked connector rejection, got %v", err)
	}
}

func TestHasConnectorConflict_SeparatesActivityFromReadiness(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		want      bool
		wantError bool
	}{
		{
			name:   "running connector without tasks",
			status: `{"connector":{"state":"RUNNING"},"tasks":[]}`,
			want:   true,
		},
		{
			name:   "paused connector with running task",
			status: `{"connector":{"state":"PAUSED"},"tasks":[{"state":"RUNNING"}]}`,
			want:   true,
		},
		{
			name:   "unassigned connector",
			status: `{"connector":{"state":"UNASSIGNED"},"tasks":[{"state":"FAILED"}]}`,
			want:   true,
		},
		{
			name:   "unassigned task",
			status: `{"connector":{"state":"PAUSED"},"tasks":[{"state":"UNASSIGNED"}]}`,
			want:   true,
		},
		{
			name:   "unknown state is conservative",
			status: `{"connector":{"state":"RESTARTING"},"tasks":[]}`,
			want:   true,
		},
		{
			name:   "failed and stopped states are safe",
			status: `{"connector":{"state":"FAILED"},"tasks":[{"state":"STOPPED"},{"state":"PAUSED"}]}`,
			want:   false,
		},
		{
			name:      "malformed API response",
			status:    `{`,
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(test.status))
			}))
			defer server.Close()

			got, err := hasConnectorConflict(context.Background(), server.Client(), server.URL, "legacy")
			if test.wantError {
				if err == nil {
					t.Fatal("expected API response error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected conflict check error: %v", err)
			}
			if got != test.want {
				t.Fatalf("conflict=%v, want %v", got, test.want)
			}
		})
	}
}

func TestHasConnectorConflict_PropagatesAPIErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"unavailable"}`))
	}))
	defer server.Close()

	if _, err := hasConnectorConflict(context.Background(), server.Client(), server.URL, "legacy"); err == nil {
		t.Fatal("expected connector status API error")
	}
}

func TestEnsureConnectorBootstrap_RegistersWhenMissing(t *testing.T) {
	var postCalls atomic.Int32
	created := atomic.Bool{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/connectors":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		case r.Method == http.MethodPost && r.URL.Path == "/connectors":
			postCalls.Add(1)
			created.Store(true)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"artificialflow-postgres-connector"}`))
			return
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/connectors/") && strings.HasSuffix(r.URL.Path, "/status"):
			if created.Load() {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"connector":{"state":"RUNNING"},"tasks":[{"state":"RUNNING"}]}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/connectors/"):
			if created.Load() {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"name":"artificialflow-postgres-connector"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	err := ensureConnectorBootstrap(context.Background(), logger.New("sync-worker-test"), connectorBootstrapOptions{
		Enabled:       true,
		ConnectURL:    server.URL,
		ConnectorName: "artificialflow-postgres-connector",
		ConnectorJSON: `{"config":{"connector.class":"io.debezium.connector.postgresql.PostgresConnector"}}`,
		WaitTimeout:   2 * time.Second,
		PollInterval:  10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("expected bootstrap success, got %v", err)
	}
	if postCalls.Load() != 1 {
		t.Fatalf("expected one connector create call, got %d", postCalls.Load())
	}
}

func TestEnsureConnectorBootstrap_SkipsCreateWhenAlreadyExists(t *testing.T) {
	var postCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/connectors":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/connectors/") && strings.HasSuffix(r.URL.Path, "/status"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"connector":{"state":"RUNNING"},"tasks":[{"state":"RUNNING"}]}`))
			return
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/connectors/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"artificialflow-postgres-connector"}`))
			return
		case r.Method == http.MethodPost && r.URL.Path == "/connectors":
			postCalls.Add(1)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"artificialflow-postgres-connector"}`))
			return
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	err := ensureConnectorBootstrap(context.Background(), logger.New("sync-worker-test"), connectorBootstrapOptions{
		Enabled:       true,
		ConnectURL:    server.URL,
		ConnectorName: "artificialflow-postgres-connector",
		ConnectorJSON: `{"config":{"connector.class":"io.debezium.connector.postgresql.PostgresConnector"}}`,
		WaitTimeout:   2 * time.Second,
		PollInterval:  10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("expected bootstrap success, got %v", err)
	}
	if postCalls.Load() != 0 {
		t.Fatalf("expected no connector create call when exists, got %d", postCalls.Load())
	}
}

func TestEnsureConnectorBootstrap_AllowsRecoveringConnectorWithRunningTask(t *testing.T) {
	var postCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/connectors":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/connectors/") && strings.HasSuffix(r.URL.Path, "/status"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"connector":{"state":"UNASSIGNED"},"tasks":[{"state":"RUNNING"}]}`))
			return
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/connectors/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"artificialflow-postgres-connector"}`))
			return
		case r.Method == http.MethodPost && r.URL.Path == "/connectors":
			postCalls.Add(1)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"artificialflow-postgres-connector"}`))
			return
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	err := ensureConnectorBootstrap(context.Background(), logger.New("sync-worker-test"), connectorBootstrapOptions{
		Enabled:       true,
		ConnectURL:    server.URL,
		ConnectorName: "artificialflow-postgres-connector",
		ConnectorJSON: `{"config":{"connector.class":"io.debezium.connector.postgresql.PostgresConnector"}}`,
		WaitTimeout:   2 * time.Second,
		PollInterval:  10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("expected bootstrap success while connector is recovering, got %v", err)
	}
	if postCalls.Load() != 0 {
		t.Fatalf("expected no connector create call when exists, got %d", postCalls.Load())
	}
}

func TestEnsureConnectorBootstrap_HandlesConflictAsSuccess(t *testing.T) {
	var postCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/connectors":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/connectors/") && strings.HasSuffix(r.URL.Path, "/status"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"connector":{"state":"RUNNING"},"tasks":[{"state":"RUNNING"}]}`))
			return
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/connectors/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
			return
		case r.Method == http.MethodPost && r.URL.Path == "/connectors":
			postCalls.Add(1)
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error_code":409}`))
			return
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	err := ensureConnectorBootstrap(context.Background(), logger.New("sync-worker-test"), connectorBootstrapOptions{
		Enabled:       true,
		ConnectURL:    server.URL,
		ConnectorName: "artificialflow-postgres-connector",
		ConnectorJSON: `{"config":{"connector.class":"io.debezium.connector.postgresql.PostgresConnector"}}`,
		WaitTimeout:   2 * time.Second,
		PollInterval:  10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("expected bootstrap success on 409, got %v", err)
	}
	if postCalls.Load() != 1 {
		t.Fatalf("expected one create call, got %d", postCalls.Load())
	}
}

func TestEnsureConnectorBootstrap_TimesOutWhenConnectorNotRunning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/connectors":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/connectors/") && strings.HasSuffix(r.URL.Path, "/status"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"connector":{"state":"FAILED"},"tasks":[{"state":"FAILED"}]}`))
			return
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/connectors/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"artificialflow-postgres-connector"}`))
			return
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	err := ensureConnectorBootstrap(context.Background(), logger.New("sync-worker-test"), connectorBootstrapOptions{
		Enabled:       true,
		ConnectURL:    server.URL,
		ConnectorName: "artificialflow-postgres-connector",
		ConnectorJSON: `{"config":{"connector.class":"io.debezium.connector.postgresql.PostgresConnector"}}`,
		WaitTimeout:   200 * time.Millisecond,
		PollInterval:  10 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error when connector never becomes RUNNING")
	}
	if !strings.Contains(err.Error(), "did not become RUNNING") {
		t.Fatalf("expected RUNNING timeout error, got %v", err)
	}
}

func TestBuildConnectorCreateRequest_OverridesNameAndParsesConfigOnlyPayload(t *testing.T) {
	request, source, err := buildConnectorCreateRequest(connectorBootstrapOptions{
		ConnectorName: "workflow-custom-connector",
		ConnectorJSON: `{"connector.class":"io.debezium.connector.postgresql.PostgresConnector"}`,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if source != "CONNECTOR_JSON" {
		t.Fatalf("expected source CONNECTOR_JSON, got %s", source)
	}
	if request.Name != "workflow-custom-connector" {
		t.Fatalf("expected connector name override, got %s", request.Name)
	}
	if got := request.Config["connector.class"]; got != "io.debezium.connector.postgresql.PostgresConnector" {
		t.Fatalf("expected connector.class in config, got %#v", got)
	}
}

func TestBuildConnectorCreateRequest_UsesEmbeddedDefaultWhenNoOverride(t *testing.T) {
	request, source, err := buildConnectorCreateRequest(connectorBootstrapOptions{
		ConnectorName: "artificialflow-postgres-connector",
	})
	if err != nil {
		t.Fatalf("expected no error from embedded default, got %v", err)
	}
	if source != "embedded-default" {
		t.Fatalf("expected embedded-default source, got %s", source)
	}
	if request.Name != "artificialflow-postgres-connector" {
		t.Fatalf("expected connector name, got %s", request.Name)
	}
	if _, ok := request.Config["connector.class"]; !ok {
		payload, _ := json.Marshal(request)
		t.Fatalf("expected connector.class in embedded default payload, got %s", payload)
	}
	for key, want := range map[string]any{
		"topic.prefix":  "artificialflow",
		"slot.name":     "artificialflow_slot",
		"database.user": "artificialflow",
	} {
		if got := request.Config[key]; got != want {
			t.Fatalf("expected embedded %s=%q, got %#v", key, want, got)
		}
	}
}

func TestBuildConnectorCreateRequest_AppliesPersistentIdentifierOverrides(t *testing.T) {
	request, _, err := buildConnectorCreateRequest(connectorBootstrapOptions{
		ConnectorName:         "artificialflow-postgres-connector",
		ConnectorTopicPrefix:  "artificialflow",
		ConnectorSlotName:     "artificialflow_slot",
		ConnectorDatabaseUser: "artificialflow",
	})
	if err != nil {
		t.Fatalf("expected no error from explicit overrides, got %v", err)
	}
	for key, want := range map[string]any{
		"topic.prefix":  "artificialflow",
		"slot.name":     "artificialflow_slot",
		"database.user": "artificialflow",
	} {
		if got := request.Config[key]; got != want {
			t.Fatalf("expected override %s=%q, got %#v", key, want, got)
		}
	}
}

func TestBuildConnectorCreateRequest_AppliesDatabaseConnectionOverrides(t *testing.T) {
	request, _, err := buildConnectorCreateRequest(connectorBootstrapOptions{
		ConnectorName:         "artificialflow-postgres-connector",
		ConnectorDatabaseHost: "release-artificialflow-postgresql",
		ConnectorDatabasePort: "6432",
		ConnectorDatabaseName: "workflow",
		ConnectorDatabasePass: "s3cret",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	for key, want := range map[string]any{
		"database.hostname": "release-artificialflow-postgresql",
		"database.port":     "6432",
		"database.dbname":   "workflow",
		"database.password": "s3cret",
		"database.user":     "artificialflow",
	} {
		if got := request.Config[key]; got != want {
			t.Fatalf("expected %s=%q, got %#v", key, want, got)
		}
	}
}
