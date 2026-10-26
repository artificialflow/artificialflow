package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/id"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MigrateProcessInstance validates and optionally applies the deliberately
// bounded migration subset in one database transaction.
func (s *GormRepository) MigrateProcessInstance(ctx context.Context, request model.InstanceMigrationRequest) (model.InstanceMigrationResult, error) {
	result := model.InstanceMigrationResult{
		OperationID: request.OperationID, InstanceKey: strconv.FormatInt(request.InstanceKey, 10), DryRun: request.DryRun,
	}
	if result.OperationID == "" {
		result.OperationID = id.GenerateUUIDv7()
	}
	if request.InstanceKey == 0 || request.SourceDefinitionKey == 0 || request.TargetDefinitionKey == 0 {
		return rejectedMigration(result, "INVALID_REQUEST", "instance, source, and target keys are required"), nil
	}
	if len(request.ElementMapping) == 0 {
		return rejectedMigration(result, "INCOMPLETE_MAPPING", "element mapping is required"), nil
	}
	requestedMapping, err := json.Marshal(request.ElementMapping)
	if err != nil {
		return result, err
	}

	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if !request.DryRun && request.OperationID != "" {
			var existing model.ProcessInstanceMigration
			err := tx.Where("operation_id = ?", request.OperationID).First(&existing).Error
			if err == nil {
				if existing.InstanceKey != request.InstanceKey ||
					existing.SourceDefinitionKey != request.SourceDefinitionKey ||
					existing.TargetDefinitionKey != request.TargetDefinitionKey ||
					!bytes.Equal(existing.Mapping, requestedMapping) {
					result = rejectedMigration(result, "OPERATION_CONFLICT", "operation ID belongs to another migration request")
					return nil
				}
				result.Status = existing.Status
				result.SourceVersion = existing.SourceVersion
				result.TargetVersion = existing.TargetVersion
				return nil
			}
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		var instance model.ProcessInstance
		query := tx.Where("key = ?", request.InstanceKey)
		if !request.DryRun && tx.Dialector.Name() != "sqlite" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := query.First(&instance).Error; err != nil {
			result = rejectedMigration(result, "INSTANCE_NOT_FOUND", "instance not found")
			return nil
		}
		if instance.ProcessDefinitionKey != request.SourceDefinitionKey || instance.State != "ACTIVE" {
			result = rejectedMigration(result, "SOURCE_STATE_CHANGED", "source definition or state changed")
			return nil
		}

		var source, target model.Process
		if err := tx.Where("key = ?", request.SourceDefinitionKey).First(&source).Error; err != nil {
			result = rejectedMigration(result, "SOURCE_NOT_FOUND", "source definition not found")
			return nil
		}
		if err := tx.Where("key = ?", request.TargetDefinitionKey).First(&target).Error; err != nil {
			result = rejectedMigration(result, "TARGET_NOT_FOUND", "target definition not found")
			return nil
		}
		result.SourceVersion, result.TargetVersion = source.Version, target.Version
		if source.TenantID != target.TenantID || source.TenantID != instance.TenantID || source.BpmnProcessID != target.BpmnProcessID {
			result = rejectedMigration(result, "LINEAGE_MISMATCH", "source and target must share tenant and process lineage")
			return nil
		}
		if target.Version <= source.Version {
			result = rejectedMigration(result, "TARGET_NOT_NEWER", "target version must be newer")
			return nil
		}
		sourceElements, reason, err := inspectMigrationResource(source.Resource)
		if err != nil {
			result = rejectedMigration(result, "INVALID_SOURCE_DEFINITION", "source definition cannot be inspected")
			return nil
		}
		if reason != "" {
			result = rejectedMigration(result, reason, "definition uses an unsupported runtime construct")
			return nil
		}
		targetElements, reason, err := inspectMigrationResource(target.Resource)
		if err != nil {
			result = rejectedMigration(result, "INVALID_TARGET_DEFINITION", "target definition cannot be inspected")
			return nil
		}
		if reason != "" {
			result = rejectedMigration(result, reason, "definition uses an unsupported runtime construct")
			return nil
		}
		for sourceID, targetID := range request.ElementMapping {
			sourceType, exists := sourceElements[sourceID]
			if !exists {
				result = rejectedMigration(result, "SOURCE_ELEMENT_NOT_FOUND", "mapped source element does not exist")
				return nil
			}
			targetType, exists := targetElements[targetID]
			if !exists {
				result = rejectedMigration(result, "TARGET_ELEMENT_NOT_FOUND", "mapped target element does not exist")
				return nil
			}
			if sourceType != targetType {
				result = rejectedMigration(result, "INCOMPATIBLE_ELEMENT_TYPE", "mapped elements must have the same BPMN type")
				return nil
			}
		}

		var elements []model.ElementInstance
		if err := tx.Where("process_instance_key = ? AND state IN ?", request.InstanceKey, []string{"ACTIVATED", "ACTIVATING", "COMPLETING"}).Find(&elements).Error; err != nil {
			return err
		}
		for _, element := range elements {
			if element.State != model.ElementInstanceStateActivated {
				result = rejectedMigration(result, "UNSTABLE_ACTIVE_STATE", "activating or completing elements cannot be migrated")
				return nil
			}
		}
		var jobs []model.Job
		if err := tx.Where("process_instance_key = ? AND state IN ?", request.InstanceKey, []string{"CREATED", "ACTIVATED"}).Find(&jobs).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			if job.State == "ACTIVATED" || (job.Type != "" && job.Type != canonicalUserTaskJobType) {
				result = rejectedMigration(result, "ACTIVE_EXTERNAL_WORK", "active external work cannot be migrated")
				return nil
			}
		}
		var incidents int64
		if err := tx.Model(&model.Incident{}).Where("process_instance_key = ? AND state = 'CREATED'", request.InstanceKey).Count(&incidents).Error; err != nil {
			return err
		}
		if incidents > 0 {
			result = rejectedMigration(result, "UNRESOLVED_INCIDENT", "unresolved incidents must be resolved first")
			return nil
		}
		var timers []model.Timer
		if err := tx.Where("process_instance_key = ? AND state = 'CREATED'", request.InstanceKey).Find(&timers).Error; err != nil {
			return err
		}
		var subscriptions []model.MessageSubscription
		if err := tx.Where("process_instance_key = ? AND state = 'OPEN'", request.InstanceKey).Find(&subscriptions).Error; err != nil {
			return err
		}
		for _, elementID := range migrationElementIDs(elements, jobs, timers, subscriptions) {
			targetID, ok := request.ElementMapping[elementID]
			if !ok || strings.TrimSpace(targetID) == "" {
				result = rejectedMigration(result, "INCOMPLETE_MAPPING", "every active element, job, timer, and subscription requires a mapping")
				return nil
			}
		}

		result.Status = model.MigrationStatusValidated
		if request.DryRun {
			return nil
		}
		tenant := auth.TenantIDFromContext(ctx)
		update := tx.Model(&model.ProcessInstance{}).
			Where("key = ? AND process_definition_key = ? AND version = ? AND state = 'ACTIVE'", request.InstanceKey, source.Key, source.Version).
			Updates(map[string]any{"process_definition_key": target.Key, "version": target.Version})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			result = rejectedMigration(result, "SOURCE_STATE_CHANGED", "concurrent source transition won")
			return gorm.ErrInvalidTransaction
		}
		for from, to := range request.ElementMapping {
			if err := tx.Model(&model.ElementInstance{}).
				Where("process_instance_key = ? AND element_id = ? AND state = ?", request.InstanceKey, from, model.ElementInstanceStateActivated).
				Updates(map[string]any{"element_id": to, "process_definition_key": target.Key}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.Job{}).
				Where("process_instance_key = ? AND element_id = ? AND state = ?", request.InstanceKey, from, model.JobStateCreated).
				Updates(map[string]any{"element_id": to, "process_definition_key": target.Key}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.Timer{}).
				Where("process_instance_key = ? AND element_id = ? AND state = ?", request.InstanceKey, from, model.TimerStateCreated).
				Update("element_id", to).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.MessageSubscription{}).
				Where("process_instance_key = ? AND element_id = ? AND state = ?", request.InstanceKey, from, model.MessageSubscriptionStateOpen).
				Update("element_id", to).Error; err != nil {
				return err
			}
		}
		mapping := requestedMapping
		now := time.Now().UTC()
		operation := model.ProcessInstanceMigration{
			TenantID: tenant, OperationID: result.OperationID, InstanceKey: request.InstanceKey,
			SourceDefinitionKey: source.Key, TargetDefinitionKey: target.Key,
			SourceVersion: source.Version, TargetVersion: target.Version, DryRun: false,
			Status: model.MigrationStatusApplied, Mapping: mapping, Actor: request.Actor, CreatedAt: now, CompletedAt: &now,
		}
		if err := tx.Create(&operation).Error; err != nil {
			return err
		}
		audit := map[string]any{
			"tenant_id": tenant, "event_id": id.GenerateUUIDv7(), "operation_id": result.OperationID,
			"instance_key": request.InstanceKey, "source_definition_key": source.Key,
			"target_definition_key": target.Key, "mapping": mapping,
			"actor": request.Actor, "occurred_at": now,
		}
		if err := tx.Table("process_instance_migration_audit").Create(audit).Error; err != nil {
			return err
		}
		result.Status = model.MigrationStatusApplied
		return nil
	})
	if err != nil {
		if result.ReasonCode == "SOURCE_STATE_CHANGED" {
			return result, nil
		}
		return result, err
	}
	return result, nil
}

func inspectMigrationResource(resource []byte) (map[string]string, string, error) {
	allowed := map[string]bool{
		"startEvent": true, "endEvent": true, "task": true, "userTask": true,
		"serviceTask": true, "receiveTask": true, "manualTask": true, "scriptTask": true,
		"businessRuleTask": true, "sendTask": true, "intermediateCatchEvent": true,
		"intermediateThrowEvent": true, "exclusiveGateway": true, "parallelGateway": true,
		"inclusiveGateway": true, "eventBasedGateway": true, "boundaryEvent": true,
	}
	decoder := xml.NewDecoder(bytes.NewReader(resource))
	result := map[string]string{}
	for {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return result, "", nil
			}
			return nil, "", err
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		attributes := xmlAttributes(start.Attr)
		switch start.Name.Local {
		case "multiInstanceLoopCharacteristics":
			return nil, "UNSUPPORTED_MULTI_INSTANCE", nil
		case "transaction":
			return nil, "UNSUPPORTED_TRANSACTION", nil
		case "adHocSubProcess":
			return nil, "UNSUPPORTED_AD_HOC_SUBPROCESS", nil
		case "compensateEventDefinition":
			return nil, "UNSUPPORTED_COMPENSATION", nil
		case "subProcess":
			if strings.EqualFold(attributes["triggeredByEvent"], "true") {
				return nil, "UNSUPPORTED_EVENT_SUBPROCESS", nil
			}
			return nil, "UNSUPPORTED_NESTED_SCOPE", nil
		case "callActivity":
			return nil, "UNSUPPORTED_NESTED_SCOPE", nil
		}
		if strings.EqualFold(attributes["isForCompensation"], "true") {
			return nil, "UNSUPPORTED_COMPENSATION", nil
		}
		if !allowed[start.Name.Local] {
			continue
		}
		for _, attribute := range start.Attr {
			if attribute.Name.Local == "id" && strings.TrimSpace(attribute.Value) != "" {
				result[strings.TrimSpace(attribute.Value)] = start.Name.Local
			}
		}
	}
}

func xmlAttributes(attributes []xml.Attr) map[string]string {
	result := make(map[string]string, len(attributes))
	for _, attribute := range attributes {
		result[attribute.Name.Local] = strings.TrimSpace(attribute.Value)
	}
	return result
}

func rejectedMigration(result model.InstanceMigrationResult, code, message string) model.InstanceMigrationResult {
	result.Status = model.MigrationStatusRejected
	result.ReasonCode = model.MigrationReasonCode(code)
	result.Message = message
	return result
}

func migrationElementIDs(elements []model.ElementInstance, jobs []model.Job, timers []model.Timer, subscriptions []model.MessageSubscription) []string {
	seen := map[string]struct{}{}
	for _, id := range appendElementIDs(elements, jobs, timers, subscriptions) {
		if id != "" {
			seen[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out
}

func appendElementIDs(elements []model.ElementInstance, jobs []model.Job, timers []model.Timer, subscriptions []model.MessageSubscription) []string {
	out := make([]string, 0, len(elements)+len(jobs)+len(timers)+len(subscriptions))
	for _, row := range elements {
		out = append(out, row.ElementID)
	}
	for _, row := range jobs {
		out = append(out, row.ElementID)
	}
	for _, row := range timers {
		out = append(out, row.ElementID)
	}
	for _, row := range subscriptions {
		out = append(out, row.ElementID)
	}
	return out
}
