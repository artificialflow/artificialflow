package application

import (
	"context"
	"fmt"

	"github.com/artificialflow/artificialflow/backend/libs/model"
)

type instanceMigrationRepository interface {
	MigrateProcessInstance(context.Context, model.InstanceMigrationRequest) (model.InstanceMigrationResult, error)
}

func (e *Engine) MigrateProcessInstance(ctx context.Context, request model.InstanceMigrationRequest) (model.InstanceMigrationResult, error) {
	repo, ok := e.repo.(instanceMigrationRepository)
	if !ok {
		return model.InstanceMigrationResult{}, fmt.Errorf("process-instance migration is unavailable")
	}
	return repo.MigrateProcessInstance(ctx, request)
}
