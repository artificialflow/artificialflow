package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/logger"
)

//go:embed connector-register.default.json
var defaultConnectorRegisterJSON []byte

type connectorBootstrapOptions struct {
	Enabled                  bool
	ConnectURL               string
	ConnectorName            string
	ConnectorFile            string
	ConnectorJSON            string
	ConnectorTopicPrefix     string
	ConnectorSlotName        string
	ConnectorDatabaseUser    string
	ConnectorDatabaseHost    string
	ConnectorDatabasePort    string
	ConnectorDatabaseName    string
	ConnectorDatabasePass    string
	WaitTimeout              time.Duration
	PollInterval             time.Duration
	BlockedConnectorNames    []string
	AllowParallelIdentifiers bool
}

type connectorCreateRequest struct {
	Name   string         `json:"name"`
	Config map[string]any `json:"config"`
}

type connectorStatusResponse struct {
	Name      string `json:"name"`
	Connector struct {
		State string `json:"state"`
	} `json:"connector"`
	Tasks []struct {
		State string `json:"state"`
	} `json:"tasks"`
}

func ensureConnectorBootstrap(ctx context.Context, log *logger.Logger, opts connectorBootstrapOptions) error {
	if !opts.Enabled {
		log.Info(ctx, "connector bootstrap disabled", nil)
		return nil
	}

	opts.ConnectURL = strings.TrimSpace(opts.ConnectURL)
	opts.ConnectorName = strings.TrimSpace(opts.ConnectorName)
	if opts.ConnectURL == "" {
		return fmt.Errorf("CONNECT_URL is required when connector bootstrap is enabled")
	}
	if opts.ConnectorName == "" {
		return fmt.Errorf("CONNECTOR_NAME is required when connector bootstrap is enabled")
	}
	if opts.WaitTimeout <= 0 {
		opts.WaitTimeout = 180 * time.Second
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 3 * time.Second
	}

	request, payloadSource, err := buildConnectorCreateRequest(opts)
	if err != nil {
		return fmt.Errorf("failed to build connector request: %w", err)
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal connector request: %w", err)
	}

	baseURL := strings.TrimSuffix(opts.ConnectURL, "/")
	client := &http.Client{Timeout: 10 * time.Second}
	deadline := time.Now().Add(opts.WaitTimeout)

	log.Info(ctx, "bootstrapping kafka connector", map[string]any{
		"connect_url":      baseURL,
		"connector_name":   opts.ConnectorName,
		"payload_source":   payloadSource,
		"wait_timeout_sec": int(opts.WaitTimeout.Seconds()),
	})

	if err := waitUntil(ctx, deadline, opts.PollInterval, func() (bool, error) {
		return isConnectReady(ctx, client, baseURL)
	}); err != nil {
		return fmt.Errorf("timed out waiting for Kafka Connect readiness: %w", err)
	}

	if !opts.AllowParallelIdentifiers {
		for _, blockedName := range opts.BlockedConnectorNames {
			blockedName = strings.TrimSpace(blockedName)
			if blockedName == "" || blockedName == opts.ConnectorName {
				continue
			}
			conflicts, err := hasConnectorConflict(ctx, client, baseURL, blockedName)
			if err != nil {
				return fmt.Errorf("failed to verify conflicting connector %q: %w", blockedName, err)
			}
			if conflicts {
				return fmt.Errorf(
					"refusing to start connector %q while conflicting connector %q or one of its tasks may be active",
					opts.ConnectorName,
					blockedName,
				)
			}
		}
	}

	exists, err := connectorExists(ctx, client, baseURL, opts.ConnectorName)
	if err != nil {
		return fmt.Errorf("failed to check connector %q existence: %w", opts.ConnectorName, err)
	}

	if !exists {
		status, responseBody, err := createConnector(ctx, client, baseURL, payload)
		if err != nil {
			return fmt.Errorf("failed to create connector %q: %w", opts.ConnectorName, err)
		}
		switch status {
		case http.StatusCreated, http.StatusOK:
			log.Info(ctx, "connector created", map[string]any{"connector_name": opts.ConnectorName})
		case http.StatusConflict:
			log.Info(ctx, "connector already exists (race)", map[string]any{"connector_name": opts.ConnectorName})
		default:
			return fmt.Errorf("connector create failed for %q with status=%d body=%s", opts.ConnectorName, status, responseBody)
		}
	} else {
		log.Info(ctx, "connector already exists; validating RUNNING status", map[string]any{"connector_name": opts.ConnectorName})
	}

	if err := waitUntil(ctx, deadline, opts.PollInterval, func() (bool, error) {
		return isConnectorRunning(ctx, client, baseURL, opts.ConnectorName)
	}); err != nil {
		return fmt.Errorf("connector %q did not become RUNNING: %w", opts.ConnectorName, err)
	}

	log.Info(ctx, "connector bootstrap complete", map[string]any{"connector_name": opts.ConnectorName})
	return nil
}

func buildConnectorCreateRequest(opts connectorBootstrapOptions) (connectorCreateRequest, string, error) {
	var (
		raw []byte
		src string
	)

	switch {
	case strings.TrimSpace(opts.ConnectorJSON) != "":
		raw = []byte(strings.TrimSpace(opts.ConnectorJSON))
		src = "CONNECTOR_JSON"
	case strings.TrimSpace(opts.ConnectorFile) != "":
		data, err := os.ReadFile(strings.TrimSpace(opts.ConnectorFile))
		if err != nil {
			return connectorCreateRequest{}, "", fmt.Errorf("read CONNECTOR_FILE: %w", err)
		}
		raw = data
		src = "CONNECTOR_FILE"
	default:
		raw = defaultConnectorRegisterJSON
		src = "embedded-default"
	}

	request := connectorCreateRequest{}
	if err := json.Unmarshal(raw, &request); err == nil && len(request.Config) > 0 {
		request.Name = strings.TrimSpace(opts.ConnectorName)
		if request.Name == "" {
			return connectorCreateRequest{}, "", fmt.Errorf("connector name cannot be empty")
		}
		applyConnectorConfigOverrides(request.Config, opts)
		return request, src, nil
	}

	configOnly := map[string]any{}
	if err := json.Unmarshal(raw, &configOnly); err != nil {
		return connectorCreateRequest{}, "", fmt.Errorf("invalid connector JSON payload: %w", err)
	}
	if len(configOnly) == 0 {
		return connectorCreateRequest{}, "", fmt.Errorf("connector config payload is empty")
	}

	request = connectorCreateRequest{
		Name:   strings.TrimSpace(opts.ConnectorName),
		Config: configOnly,
	}
	if request.Name == "" {
		return connectorCreateRequest{}, "", fmt.Errorf("connector name cannot be empty")
	}
	applyConnectorConfigOverrides(request.Config, opts)
	return request, src, nil
}

func applyConnectorConfigOverrides(config map[string]any, opts connectorBootstrapOptions) {
	for key, value := range map[string]string{
		"topic.prefix":      opts.ConnectorTopicPrefix,
		"slot.name":         opts.ConnectorSlotName,
		"database.user":     opts.ConnectorDatabaseUser,
		"database.hostname": opts.ConnectorDatabaseHost,
		"database.port":     opts.ConnectorDatabasePort,
		"database.dbname":   opts.ConnectorDatabaseName,
		"database.password": opts.ConnectorDatabasePass,
	} {
		if value = strings.TrimSpace(value); value != "" {
			config[key] = value
		}
	}
}

func waitUntil(ctx context.Context, deadline time.Time, pollInterval time.Duration, check func() (bool, error)) error {
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		ok, err := check()
		if err != nil {
			lastErr = err
		} else if ok {
			return nil
		}

		if time.Now().After(deadline) {
			if lastErr != nil {
				return lastErr
			}
			return fmt.Errorf("timeout exceeded")
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func isConnectReady(ctx context.Context, client *http.Client, baseURL string) (bool, error) {
	status, _, err := httpGet(ctx, client, baseURL+"/connectors")
	if err != nil {
		return false, err
	}
	if status == http.StatusOK {
		return true, nil
	}
	return false, fmt.Errorf("connectors endpoint returned status=%d", status)
}

func connectorExists(ctx context.Context, client *http.Client, baseURL, connectorName string) (bool, error) {
	status, body, err := httpGet(ctx, client, connectorByNameURL(baseURL, connectorName))
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("unexpected status=%d body=%s", status, body)
	}
}

func createConnector(ctx context.Context, client *http.Client, baseURL string, payload []byte) (int, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/connectors", bytes.NewReader(payload))
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, "", err
	}
	return response.StatusCode, strings.TrimSpace(string(body)), nil
}

func isConnectorRunning(ctx context.Context, client *http.Client, baseURL, connectorName string) (bool, error) {
	parsed, exists, err := getConnectorStatus(ctx, client, baseURL, connectorName)
	if err != nil || !exists {
		return false, err
	}

	if len(parsed.Tasks) == 0 {
		return false, nil
	}
	for _, task := range parsed.Tasks {
		if !strings.EqualFold(strings.TrimSpace(task.State), "RUNNING") {
			return false, nil
		}
	}

	connectorState := strings.ToUpper(strings.TrimSpace(parsed.Connector.State))
	return connectorState == "RUNNING" || connectorState == "UNASSIGNED", nil
}

func hasConnectorConflict(ctx context.Context, client *http.Client, baseURL, connectorName string) (bool, error) {
	parsed, exists, err := getConnectorStatus(ctx, client, baseURL, connectorName)
	if err != nil || !exists {
		return false, err
	}

	if connectorStateMayBeActive(parsed.Connector.State) {
		return true, nil
	}
	for _, task := range parsed.Tasks {
		if connectorStateMayBeActive(task.State) {
			return true, nil
		}
	}
	return false, nil
}

func connectorStateMayBeActive(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "FAILED", "PAUSED", "STOPPED":
		return false
	default:
		// RUNNING and UNASSIGNED are active. Unknown or omitted states are also
		// treated as active because starting both connector generations can
		// duplicate the same database changes.
		return true
	}
}

func getConnectorStatus(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	connectorName string,
) (connectorStatusResponse, bool, error) {
	status, body, err := httpGet(ctx, client, connectorStatusURL(baseURL, connectorName))
	if err != nil {
		return connectorStatusResponse{}, false, err
	}
	if status == http.StatusNotFound {
		return connectorStatusResponse{}, false, nil
	}
	if status != http.StatusOK {
		return connectorStatusResponse{}, false, fmt.Errorf("unexpected status=%d body=%s", status, body)
	}

	parsed := connectorStatusResponse{}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return connectorStatusResponse{}, false, fmt.Errorf("decode connector status: %w", err)
	}
	return parsed, true, nil
}

func httpGet(ctx context.Context, client *http.Client, targetURL string) (int, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, "", err
	}
	return response.StatusCode, strings.TrimSpace(string(body)), nil
}

func connectorByNameURL(baseURL, connectorName string) string {
	return fmt.Sprintf("%s/connectors/%s", baseURL, url.PathEscape(strings.TrimSpace(connectorName)))
}

func connectorStatusURL(baseURL, connectorName string) string {
	return fmt.Sprintf("%s/connectors/%s/status", baseURL, url.PathEscape(strings.TrimSpace(connectorName)))
}
