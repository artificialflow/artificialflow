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
	"strings"
	"syscall"
	"time"

	"github.com/artificialflow/artificialflow/connectors/internal/common"
)

const jobType = "io.artificialflow.connector.http"

func main() {
	baseURL := common.EnvOr("ARTIFICIALFLOW_BASE_URL", "http://localhost:9100/api")
	token := os.Getenv("ARTIFICIALFLOW_TOKEN")
	allowed := common.ParseAllowlist(os.Getenv("HTTP_CONNECTOR_ALLOWED_HOSTS"))
	if err := common.ValidateAllowlistRequired(allowed, "HTTP_CONNECTOR_ALLOW_ANY_HOST", "HTTP_CONNECTOR_ALLOWED_HOSTS"); err != nil {
		log.Fatal(err)
	}

	runtime, err := common.NewRuntime(common.RuntimeConfig{
		BaseURL: baseURL, Token: token, JobType: jobType,
		WorkerName: common.EnvOr("WORKER_NAME", "http-connector"),
		MaxJobs:    5, LockDuration: 60 * time.Second,
		Execute: func(ctx context.Context, inputs map[string]any) (map[string]any, error) {
			return executeHTTP(ctx, inputs, allowed)
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("HTTP connector listening for %s", jobType)
	if err := runtime.Run(ctx); err != nil && err != context.Canceled {
		log.Fatal(err)
	}
}

func executeHTTP(ctx context.Context, vars map[string]any, allowed map[string]struct{}) (map[string]any, error) {
	urlStr := common.StringVar(vars, "url")
	if urlStr == "" {
		return nil, fmt.Errorf("instance variable url is required")
	}
	if _, err := common.AssertURLHostAllowed(urlStr, allowed, "HTTP_CONNECTOR_ALLOWED_HOSTS"); err != nil {
		return nil, err
	}

	method := common.StringVar(vars, "method")
	if method == "" {
		method = http.MethodPost
	}

	timeoutMs := 10000
	switch v := vars["timeoutMs"].(type) {
	case float64:
		if v > 0 {
			timeoutMs = int(v)
		}
	case int:
		if v > 0 {
			timeoutMs = v
		}
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err == nil && n > 0 {
			timeoutMs = n
		}
	}

	bodyVal, err := common.CoerceJSONValue(vars["body"])
	if err != nil {
		return nil, err
	}
	var bodyReader io.Reader
	if bodyVal != nil {
		b, err := json.Marshal(bodyVal)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), urlStr, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if err := common.ApplyHeaders(req, vars["headers"]); err != nil {
		return nil, err
	}

	httpClient := &http.Client{
		Timeout:       time.Duration(timeoutMs) * time.Millisecond,
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
		"httpStatus": resp.StatusCode,
		"httpBody":   string(raw),
		"httpOk":     ok,
	}
	if !ok && common.FailOnNon2xxDefault(vars, "HTTP_CONNECTOR_FAIL_ON_NON_2XX") {
		return result, fmt.Errorf("HTTP status %d (failOnNon2xx)", resp.StatusCode)
	}
	return result, nil
}
