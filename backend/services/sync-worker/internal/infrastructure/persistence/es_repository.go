package persistence

import (
	"context"

	"github.com/artificialflow/artificialflow/backend/libs/elasticsearch"
	"github.com/artificialflow/artificialflow/backend/services/sync-worker/internal/domain/repository"
)

type ESRepository struct {
	client *elasticsearch.Repository
}

// Ensure implementation
var _ repository.SyncRepository = &ESRepository{}

func NewESRepository(client *elasticsearch.Repository) *ESRepository {
	return &ESRepository{client: client}
}

func (r *ESRepository) Upsert(ctx context.Context, index string, id string, doc map[string]any) error {
	return r.client.Upsert(ctx, index, id, doc)
}

func (r *ESRepository) Delete(ctx context.Context, index string, id string) error {
	return r.client.Delete(ctx, index, id)
}

func (r *ESRepository) UpdateWithScript(ctx context.Context, index string, id string, script string, params map[string]any) error {
	return r.client.UpdateWithScript(ctx, index, id, script, params)
}

func (r *ESRepository) UpsertWithScript(ctx context.Context, index string, id string, script string, params, upsert map[string]any) error {
	return r.client.UpsertWithScript(ctx, index, id, script, params, upsert)
}

func (r *ESRepository) Bulk(ctx context.Context, ops []repository.WriteOp) ([]repository.BulkItemResult, error) {
	if len(ops) == 0 {
		return nil, nil
	}
	esOps := make([]elasticsearch.WriteOp, len(ops))
	for i, op := range ops {
		esOps[i] = elasticsearch.WriteOp{
			Kind:   elasticsearch.WriteKind(op.Kind),
			Index:  op.Index,
			ID:     op.ID,
			Doc:    op.Doc,
			Script: op.Script,
			Params: op.Params,
			Upsert: op.Upsert,
		}
	}
	items, err := r.client.Bulk(ctx, esOps)
	if err != nil {
		return nil, err
	}
	out := make([]repository.BulkItemResult, len(items))
	for i, item := range items {
		out[i] = repository.BulkItemResult{Status: item.Status, Error: item.Error}
	}
	return out, nil
}
