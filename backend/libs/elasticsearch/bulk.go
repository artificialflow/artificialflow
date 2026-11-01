package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// WriteKind identifies a single projection write in a bulk request.
type WriteKind int

const (
	WriteIndex WriteKind = iota + 1
	WriteDelete
	WriteUpdateScript
	WriteUpsertScript
)

// WriteOp is one Elasticsearch mutation encoded into NDJSON bulk format.
type WriteOp struct {
	Kind   WriteKind
	Index  string
	ID     string
	Doc    map[string]any
	Script string
	Params map[string]any
	Upsert map[string]any
}

// BulkItemResult is the per-operation outcome from a bulk response.
type BulkItemResult struct {
	Status int
	Error  error
}

const bulkRetryOnConflict = 5

// Bulk applies write operations in order via the Elasticsearch _bulk API.
// A non-nil transport error means the request failed as a whole and items is nil.
func (r *Repository) Bulk(ctx context.Context, ops []WriteOp) ([]BulkItemResult, error) {
	if len(ops) == 0 {
		return nil, nil
	}
	body, err := encodeBulkBody(ops)
	if err != nil {
		return nil, err
	}

	req := esapi.BulkRequest{Body: bytes.NewReader(body)}
	res, err := req.Do(ctx, r.client.Raw())
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch bulk read body: %w", err)
	}
	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch bulk failed: status=%s body=%s", res.Status(), truncateForErr(raw))
	}
	return parseBulkResponse(raw, len(ops))
}

func encodeBulkBody(ops []WriteOp) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)

	for i, op := range ops {
		if op.Index == "" || op.ID == "" {
			return nil, fmt.Errorf("bulk op %d: index and id are required", i)
		}
		meta := map[string]any{}
		switch op.Kind {
		case WriteIndex:
			meta["index"] = map[string]any{"_index": op.Index, "_id": op.ID}
			if err := enc.Encode(meta); err != nil {
				return nil, err
			}
			if op.Doc == nil {
				op.Doc = map[string]any{}
			}
			if err := enc.Encode(op.Doc); err != nil {
				return nil, err
			}
		case WriteDelete:
			meta["delete"] = map[string]any{"_index": op.Index, "_id": op.ID}
			if err := enc.Encode(meta); err != nil {
				return nil, err
			}
		case WriteUpdateScript, WriteUpsertScript:
			meta["update"] = map[string]any{
				"_index":            op.Index,
				"_id":               op.ID,
				"retry_on_conflict": bulkRetryOnConflict,
			}
			if err := enc.Encode(meta); err != nil {
				return nil, err
			}
			body := map[string]any{
				"script": map[string]any{
					"source": op.Script,
					"lang":   "painless",
					"params": op.Params,
				},
			}
			if op.Kind == WriteUpsertScript {
				if op.Upsert == nil {
					return nil, fmt.Errorf("bulk op %d: upsert document is required", i)
				}
				body["upsert"] = op.Upsert
			}
			if err := enc.Encode(body); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("bulk op %d: unsupported write kind %d", i, op.Kind)
		}
	}
	return buf.Bytes(), nil
}

type bulkResponse struct {
	Errors bool             `json:"errors"`
	Items  []map[string]any `json:"items"`
}

func parseBulkResponse(raw []byte, opCount int) ([]BulkItemResult, error) {
	var parsed bulkResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("elasticsearch bulk decode: %w", err)
	}
	if len(parsed.Items) != opCount {
		return nil, fmt.Errorf("elasticsearch bulk item count mismatch: got %d want %d", len(parsed.Items), opCount)
	}

	out := make([]BulkItemResult, opCount)
	for i, item := range parsed.Items {
		action, status, errPayload, err := decodeBulkItem(item)
		if err != nil {
			return nil, fmt.Errorf("elasticsearch bulk item %d: %w", i, err)
		}
		result := BulkItemResult{Status: status}
		if errPayload != nil {
			// Delete 404 is idempotent success, matching Delete().
			if action == "delete" && status == 404 {
				out[i] = result
				continue
			}
			result.Error = fmt.Errorf("elasticsearch bulk %s failed: status=%d error=%s", action, status, errPayload)
		} else if status >= 400 {
			if action == "delete" && status == 404 {
				out[i] = result
				continue
			}
			result.Error = fmt.Errorf("elasticsearch bulk %s failed: status=%d", action, status)
		}
		out[i] = result
	}
	return out, nil
}

func decodeBulkItem(item map[string]any) (action string, status int, errPayload any, err error) {
	for key, value := range item {
		action = key
		body, ok := value.(map[string]any)
		if !ok {
			return "", 0, nil, fmt.Errorf("unexpected %s payload type %T", key, value)
		}
		switch v := body["status"].(type) {
		case float64:
			status = int(v)
		case int:
			status = v
		case json.Number:
			n, convErr := v.Int64()
			if convErr != nil {
				return "", 0, nil, fmt.Errorf("status: %w", convErr)
			}
			status = int(n)
		default:
			return "", 0, nil, fmt.Errorf("missing status in %s result", key)
		}
		if e, exists := body["error"]; exists && e != nil {
			errPayload = e
		}
		return action, status, errPayload, nil
	}
	return "", 0, nil, fmt.Errorf("empty bulk item")
}

func truncateForErr(raw []byte) string {
	const max = 512
	if len(raw) <= max {
		return string(raw)
	}
	return string(raw[:max]) + "..."
}
