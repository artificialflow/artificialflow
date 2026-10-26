package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/artificialflow/artificialflow/connectors/internal/common"
)

const jobType = "io.artificialflow.connector.webhook"

func main() {
	baseURL := common.EnvOr("ARTIFICIALFLOW_BASE_URL", "http://localhost:9100/api")
	token := os.Getenv("ARTIFICIALFLOW_TOKEN")
	allowed := common.ParseAllowlist(os.Getenv("WEBHOOK_CONNECTOR_ALLOWED_HOSTS"))
	if err := common.ValidateAllowlistRequired(allowed, "WEBHOOK_CONNECTOR_ALLOW_ANY_HOST", "WEBHOOK_CONNECTOR_ALLOWED_HOSTS"); err != nil {
		log.Fatal(err)
	}

	runtime, err := common.NewRuntime(common.RuntimeConfig{
		BaseURL: baseURL, Token: token, JobType: jobType,
		WorkerName: common.EnvOr("WORKER_NAME", "webhook-connector"),
		MaxJobs:    5, LockDuration: 60 * time.Second,
		Execute: func(ctx context.Context, inputs map[string]any) (map[string]any, error) {
			return executeWebhook(ctx, inputs, allowed)
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("webhook connector listening for %s", jobType)
	if err := runtime.Run(ctx); err != nil && err != context.Canceled {
		log.Fatal(err)
	}
}

func executeWebhook(ctx context.Context, vars map[string]any, allowed map[string]struct{}) (map[string]any, error) {
	urlStr := common.StringVar(vars, "webhookUrl")
	if urlStr == "" {
		urlStr = common.StringVar(vars, "url")
	}
	if urlStr == "" {
		return nil, fmt.Errorf("webhookUrl (or url) is required")
	}
	if _, err := common.AssertURLHostAllowed(urlStr, allowed, "WEBHOOK_CONNECTOR_ALLOWED_HOSTS"); err != nil {
		return nil, err
	}

	payload := vars["payload"]
	if payload == nil {
		payload = vars["body"]
	}
	payload, err := common.CoerceJSONValue(payload)
	if err != nil {
		return nil, err
	}
	var bodyReader io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	} else {
		bodyReader = bytes.NewReader([]byte("{}"))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := common.StringVar(vars, "webhookToken"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	httpClient := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: common.CheckRedirectAllowlist(allowed),
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	ok := resp.StatusCode >= 200 && resp.StatusCode < 300
	result := map[string]any{
		"webhookStatus": resp.StatusCode,
		"webhookBody":   string(raw),
		"webhookOk":     ok,
	}
	if !ok && common.FailOnNon2xxDefault(vars, "WEBHOOK_CONNECTOR_FAIL_ON_NON_2XX") {
		return result, fmt.Errorf("webhook status %d (failOnNon2xx)", resp.StatusCode)
	}
	return result, nil
}
