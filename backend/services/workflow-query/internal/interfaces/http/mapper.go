package http

import (
	"strconv"
	"strings"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/model"
)

type InstanceResponse struct {
	ID                string         `json:"id"`
	WorkflowID        string         `json:"workflow_id"`
	ParentInstanceID  string         `json:"parent_instance_id,omitempty"`
	ParentExecutionID string         `json:"parent_execution_id,omitempty"`
	Status            string         `json:"status"`
	Context           map[string]any `json:"context"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

type InstanceSearchResponse struct {
	Instances []InstanceResponse `json:"instances"`
	Total     int64              `json:"total"`
}

type WorkflowResponse struct {
	ID                  string    `json:"id"`
	ProcessDefinitionID string    `json:"process_definition_id"`
	Name                string    `json:"name"`
	Version             int       `json:"version"`
	ResourceName        string    `json:"resource_name"`
	DeploymentID        string    `json:"deployment_id"`
	TenantID            string    `json:"tenant_id"`
	ResourceChecksum    string    `json:"resource_checksum"`
	CreatedAt           time.Time `json:"created_at"`
}

type WorkflowSearchResponse struct {
	Workflows []WorkflowResponse `json:"workflows"`
	Total     int64              `json:"total"`
}

func mapInstanceResponse(instance model.ProcessInstance) InstanceResponse {
	updatedAt := instance.CreatedAt
	if !instance.EndTime.IsZero() {
		updatedAt = instance.EndTime
	}

	response := InstanceResponse{
		ID:         strconv.FormatInt(instance.Key, 10),
		WorkflowID: strconv.FormatInt(instance.ProcessDefinitionKey, 10),
		Status:     mapInstanceStatus(string(instance.State)),
		Context:    instance.Context,
		CreatedAt:  instance.CreatedAt,
		UpdatedAt:  updatedAt,
	}
	if response.Context == nil {
		response.Context = map[string]any{}
	}
	if instance.ParentProcessInstanceKey != 0 {
		response.ParentInstanceID = strconv.FormatInt(instance.ParentProcessInstanceKey, 10)
	}
	if instance.ParentElementInstanceKey != 0 {
		response.ParentExecutionID = strconv.FormatInt(instance.ParentElementInstanceKey, 10)
	}

	return response
}

func mapWorkflowResponse(workflow model.Process) WorkflowResponse {
	name := workflow.BpmnProcessID
	if strings.TrimSpace(name) == "" {
		name = workflow.ResourceName
	}

	return WorkflowResponse{
		ID:                  strconv.FormatInt(workflow.Key, 10),
		ProcessDefinitionID: workflow.BpmnProcessID,
		Name:                name,
		Version:             workflow.Version,
		ResourceName:        workflow.ResourceName,
		DeploymentID:        strconv.FormatInt(workflow.DeploymentKey, 10),
		TenantID:            workflow.TenantID,
		ResourceChecksum:    workflow.ResourceChecksum,
		CreatedAt:           workflow.CreatedAt,
	}
}

func mapInstanceStatus(state string) string {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "ACTIVE", "ACTIVATED", "ACTIVATING", "COMPLETING":
		return "RUNNING"
	case "COMPLETED":
		return "COMPLETED"
	case "CANCELED", "TERMINATED", "FAILED":
		return "FAILED"
	default:
		return "PENDING"
	}
}
