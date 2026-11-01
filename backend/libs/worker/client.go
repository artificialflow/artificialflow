package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"io"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	defaultHTTPTimeout     = 30 * time.Second
	defaultActivateTimeout = 5 * time.Second
	defaultLockDuration    = 30 * time.Second
	defaultMaxJobs         = 1

	// WorkerProtocolVersion defines the worker API contract version used by SDK clients.
	WorkerProtocolVersion = "v1"
	// HeaderWorkerProtocolVersion is sent by SDK clients to declare protocol compatibility.
	HeaderWorkerProtocolVersion = "X-Workflow-Worker-Protocol-Version"
	// HeaderEngineProtocolVersion is returned by the engine to expose selected protocol version.
	HeaderEngineProtocolVersion = "X-Workflow-Engine-Protocol-Version"
)

type ClientConfig struct {
	BaseURL     string
	HTTPClient  *http.Client
	BearerToken string
}

func (c *Client) GetCapabilities(ctx context.Context) (*CapabilitiesResponse, error) {
	var response CapabilitiesResponse
	if err := c.doJSONRequest(ctx, http.MethodGet, "/jobs/capabilities", nil, &response); err != nil {
		return nil, err
	}
	if response.Capabilities == nil {
		response.Capabilities = []string{}
	}
	return &response, nil
}

type Client struct {
	baseURL     string
	httpClient  *http.Client
	bearerToken string
}

func NewClient(cfg ClientConfig) (*Client, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout:   defaultHTTPTimeout,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		}
	}

	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		httpClient:  httpClient,
		bearerToken: cfg.BearerToken,
	}, nil
}

type ActivateJobsRequest struct {
	Type           string `json:"type"`
	Worker         string `json:"worker"`
	MaxJobs        int    `json:"maxJobs"`
	TimeoutMs      int    `json:"timeoutMs"`
	LockDurationMs int    `json:"lockDurationMs"`
}

type ActivateJobsResponse struct {
	Jobs []model.Job `json:"jobs"`
}

type CapabilitiesResponse struct {
	ProtocolVersion string   `json:"protocolVersion"`
	Capabilities    []string `json:"capabilities"`
}

type CompleteJobRequest struct {
	Worker    string         `json:"worker"`
	Variables map[string]any `json:"variables"`
}

type FailJobRequest struct {
	Worker       string `json:"worker"`
	ErrorMessage string `json:"errorMessage"`
	Retries      *int   `json:"retries,omitempty"`
}

type ExtendJobLockRequest struct {
	Worker         string `json:"worker"`
	LockDurationMs int    `json:"lockDurationMs"`
}

func (c *Client) ActivateJobs(ctx context.Context, req ActivateJobsRequest) ([]model.Job, error) {
	var response ActivateJobsResponse
	if err := c.doJSONRequest(ctx, http.MethodPost, "/jobs/activate", req, &response); err != nil {
		return nil, err
	}
	if response.Jobs == nil {
		return []model.Job{}, nil
	}
	return response.Jobs, nil
}

func (c *Client) CompleteJob(ctx context.Context, jobKey int64, req CompleteJobRequest) error {
	path := fmt.Sprintf("/jobs/%d/complete", jobKey)
	return c.doJSONRequest(ctx, http.MethodPost, path, req, nil)
}

func (c *Client) FailJob(ctx context.Context, jobKey int64, req FailJobRequest) error {
	path := fmt.Sprintf("/jobs/%d/fail", jobKey)
	return c.doJSONRequest(ctx, http.MethodPost, path, req, nil)
}

func (c *Client) ExtendJobLock(ctx context.Context, jobKey int64, req ExtendJobLockRequest) error {
	path := fmt.Sprintf("/jobs/%d/extend-lock", jobKey)
	return c.doJSONRequest(ctx, http.MethodPost, path, req, nil)
}

func (c *Client) doJSONRequest(ctx context.Context, method, path string, reqBody any, respBody any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		body, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(HeaderWorkerProtocolVersion, WorkerProtocolVersion)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearerToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		detail := strings.TrimSpace(string(msg))
		if detail == "" {
			return fmt.Errorf("%s %s returned status %d", method, path, resp.StatusCode)
		}
		return fmt.Errorf("%s %s returned status %d: %s", method, path, resp.StatusCode, detail)
	}

	if respBody == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(respBody); err != nil && err != io.EOF {
		return err
	}

	return nil
}
