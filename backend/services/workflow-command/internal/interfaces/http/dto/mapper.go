package dto

import (
	"fmt"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"strings"
)

// ToWorkflowResponse maps internal workflow model to API response
func ToWorkflowResponse(w *model.WorkflowDefinition) WorkflowDefinitionResponse {
	return WorkflowDefinitionResponse{
		ID:                  fmt.Sprintf("%d", w.ID),
		ProcessDefinitionID: w.ProcessDefinitionID,
		Name:                w.Name,
		Version:             w.Version,
		ResourceName:        w.ResourceName,
		DeploymentID:        w.DeploymentID,
		TenantID:            w.TenantID,
		ResourceChecksum:    w.ResourceChecksum,
		BPMNXML:             w.BPMNXML,
		Steps:               w.Steps, // Direct copy for now
		CreatedAt:           w.CreatedAt,
	}
}

// ToWorkflowInstanceResponse maps internal instance model to API response
func ToWorkflowInstanceResponse(i *model.WorkflowInstance) WorkflowInstanceResponse {
	return WorkflowInstanceResponse{
		ID:                i.ID,
		WorkflowID:        i.WorkflowID,
		Status:            string(i.Status),
		ParentInstanceID:  i.ParentInstanceID,
		ParentExecutionID: i.ParentExecutionID,
		Context:           i.Context,
		CreatedAt:         i.CreatedAt,
		UpdatedAt:         i.UpdatedAt,
		Executions:        ToExecutionResponses(i.Executions, nil),
	}
}

func ToWorkflowInstanceResponseWithTasks(i *model.WorkflowInstance, tasks []UserTaskResponse) WorkflowInstanceResponse {
	response := ToWorkflowInstanceResponse(i)
	response.Executions = ToExecutionResponses(i.Executions, tasks)
	return response
}

func ToExecutionResponses(executions []model.Execution, tasks []UserTaskResponse) []ExecutionResponse {
	taskByExecutionID := make(map[string]UserTaskResponse, len(tasks))
	for _, task := range tasks {
		taskByExecutionID[task.ExecutionID] = task
	}

	responses := make([]ExecutionResponse, len(executions))
	for idx, execution := range executions {
		response := ExecutionResponse{
			ID:                 execution.ID,
			StepID:             execution.StepID,
			Status:             execution.Status,
			ParentID:           execution.ParentID,
			StartTime:          execution.StartTime,
			ElementInstanceKey: fmt.Sprintf("%d", execution.ElementInstanceKey),
		}
		if task, ok := taskByExecutionID[execution.ID]; ok {
			response.Task = &task
		}
		responses[idx] = response
	}
	return responses
}

// ToJobResponse maps internal job model to API response
func ToJobResponse(j model.Job) JobResponse {
	return JobResponse{
		Key:                  fmt.Sprintf("%d", j.Key),
		Type:                 j.Type,
		ProcessInstanceKey:   fmt.Sprintf("%d", j.ProcessInstanceKey),
		ElementInstanceKey:   fmt.Sprintf("%d", j.ElementInstanceKey),
		ProcessDefinitionKey: fmt.Sprintf("%d", j.ProcessDefinitionKey),
		ElementID:            j.ElementID,
		Worker:               j.Worker,
		Retries:              j.Retries,
		State:                string(j.State),
		Assignee:             j.Assignee,
		CandidateUsers:       j.CandidateUsers,
		CandidateGroups:      j.CandidateGroups,
		LockExpirationTime:   j.LockExpirationTime,
		DueDate:              j.DueDate,
		CreatedAt:            j.CreatedAt,
		UpdatedAt:            j.UpdatedAt,
	}
}

func ToJobResponses(jobs []model.Job) []JobResponse {
	responses := make([]JobResponse, len(jobs))
	for i, j := range jobs {
		responses[i] = ToJobResponse(j)
	}
	return responses
}

// ToUserTaskResponse reports task facts only. Whether a particular end user
// may act on the task is decided by the client application.
func ToUserTaskResponse(j model.Job) UserTaskResponse {
	claimed := strings.TrimSpace(j.Worker)
	open := j.State != model.JobStateCompleted
	return UserTaskResponse{
		Key:                  fmt.Sprintf("%d", j.Key),
		ProcessInstanceKey:   fmt.Sprintf("%d", j.ProcessInstanceKey),
		ProcessDefinitionKey: fmt.Sprintf("%d", j.ProcessDefinitionKey),
		ElementID:            j.ElementID,
		ExecutionID:          fmt.Sprintf("%d", j.ElementInstanceKey),
		State:                string(j.State),
		Assignee:             j.Assignee,
		CandidateUsers:       splitAssignmentList(j.CandidateUsers),
		CandidateGroups:      splitAssignmentList(j.CandidateGroups),
		ClaimedBy:            claimed,
		CanClaim:             open && claimed == "",
		CanComplete:          open && claimed != "",
		DueDate:              j.DueDate,
		CreatedAt:            j.CreatedAt,
		UpdatedAt:            j.UpdatedAt,
	}
}

func splitAssignmentList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
