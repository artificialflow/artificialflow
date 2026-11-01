package repository

import "context"

// WriteKind identifies a single projection write in a bulk request.
type WriteKind int

const (
	WriteIndex WriteKind = iota + 1
	WriteDelete
	WriteUpdateScript
	WriteUpsertScript
)

// WriteOp is one read-model mutation planned from an event or CDC message.
type WriteOp struct {
	Kind   WriteKind
	Index  string
	ID     string
	Doc    map[string]any
	Script string
	Params map[string]any
	Upsert map[string]any
}

// BulkItemResult is the per-operation outcome from a bulk write.
type BulkItemResult struct {
	Status int
	Error  error
}

// SyncRepository defines the interface for syncing data to the read model (Elasticsearch)
type SyncRepository interface {
	Upsert(ctx context.Context, index string, id string, doc map[string]any) error
	Delete(ctx context.Context, index string, id string) error
	UpdateWithScript(ctx context.Context, index string, id string, script string, params map[string]any) error
	UpsertWithScript(ctx context.Context, index string, id string, script string, params, upsert map[string]any) error
	Bulk(ctx context.Context, ops []WriteOp) ([]BulkItemResult, error)
}
