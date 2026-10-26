package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/artificialflow/artificialflow/backend/libs/auth"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/domain/bpmn"
	"time"
)

var ErrBPMNValidation = errors.New("bpmn validation failed")

func (e *Engine) DeployWorkflow(ctx context.Context, name string, steps []model.StepDefinition) (*model.WorkflowDefinition, error) {
	var wf *model.WorkflowDefinition
	err := e.withTx(ctx, func(txEngine *Engine) error {
		var err error
		wf, err = txEngine.deployWorkflow(ctx, name, steps)
		return err
	})
	if err != nil {
		return nil, err
	}
	return wf, nil
}

func (e *Engine) deployWorkflow(ctx context.Context, name string, steps []model.StepDefinition) (*model.WorkflowDefinition, error) {
	// Generate logical ID if not provided (though for programmatic it's usually just passed as steps)
	processDefinitionID := generateRuntimeID()

	// Determine Version
	version := 1
	if latest, err := e.repo.GetProcessByBpmnProcessID(ctx, processDefinitionID); err == nil {
		version = latest.Version + 1
	}

	// Serialize steps to JSON for storage in Resource
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal steps: %v", err)
	}

	// Calculate Checksum
	hash := sha256.Sum256(stepsJSON)
	checksum := hex.EncodeToString(hash[:])

	// Engine: Create Process
	processKey := generateKey(processDefinitionID + "_v" + fmt.Sprintf("%d", version))
	resourceName := name
	if resourceName == "" {
		resourceName = "generated.bpmn"
	}
	process := &model.Process{
		Key:              processKey,
		BpmnProcessID:    processDefinitionID,
		Version:          version,
		ResourceName:     resourceName,
		DeploymentKey:    generateKey(generateRuntimeID()),
		Resource:         stepsJSON, // Store JSON in Resource for programmatic
		ResourceChecksum: checksum,
		TenantID:         auth.TenantIDFromContext(ctx),
		CreatedAt:        time.Now(),
	}

	if err := e.repo.CreateProcess(ctx, process); err != nil {
		return nil, err
	}

	return e.processToWorkflowDefinition(process)
}

func (e *Engine) DeployWorkflowFromBPMN(ctx context.Context, xmlData []byte) (*model.WorkflowDefinition, error) {
	var wf *model.WorkflowDefinition
	err := e.withTx(ctx, func(txEngine *Engine) error {
		var err error
		wf, err = txEngine.deployWorkflowFromBPMN(ctx, xmlData)
		return err
	})
	if err != nil {
		return nil, err
	}
	return wf, nil
}

func (e *Engine) deployWorkflowFromBPMN(ctx context.Context, xmlData []byte) (*model.WorkflowDefinition, error) {
	wf, err := bpmn.Parse(bytes.NewReader(xmlData))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBPMNValidation, err)
	}

	// Determine Version
	version := 1
	if latest, err := e.repo.GetProcessByBpmnProcessID(ctx, wf.ProcessDefinitionID); err == nil {
		version = latest.Version + 1
	}

	// Calculate Checksum
	hash := sha256.Sum256(xmlData)
	checksum := hex.EncodeToString(hash[:])

	// Engine: Create Process
	processKey := generateKey(wf.ProcessDefinitionID + "_v" + fmt.Sprintf("%d", version))
	process := &model.Process{
		Key:              processKey,
		BpmnProcessID:    wf.ProcessDefinitionID,
		Version:          version,
		ResourceName:     wf.Name, // Use name as resource name or default?
		DeploymentKey:    generateKey(generateRuntimeID()),
		Resource:         xmlData,
		ResourceChecksum: checksum,
		TenantID:         auth.TenantIDFromContext(ctx),
		CreatedAt:        time.Now(),
	}

	if process.ResourceName == "" {
		process.ResourceName = "process.bpmn"
	}

	if err := e.repo.CreateProcess(ctx, process); err != nil {
		return nil, err
	}

	// Return parsed definition for immediate use
	wf.ID = process.Key
	wf.BPMNXML = string(xmlData)
	wf.ResourceName = process.ResourceName
	wf.DeploymentID = fmt.Sprintf("%d", process.DeploymentKey)
	wf.TenantID = process.TenantID
	wf.ResourceChecksum = process.ResourceChecksum
	wf.CreatedAt = process.CreatedAt

	return wf, nil
}

// processToWorkflowDefinition converts a Engine Process model to the internal WorkflowDefinition
func (e *Engine) processToWorkflowDefinition(p *model.Process) (*model.WorkflowDefinition, error) {
	wf := &model.WorkflowDefinition{
		ID:                  p.Key,
		ProcessDefinitionID: p.BpmnProcessID,
		Name:                p.ResourceName,
		Version:             p.Version,
		TenantID:            p.TenantID,
		ResourceChecksum:    p.ResourceChecksum,
		CreatedAt:           p.CreatedAt,
		DeploymentID:        fmt.Sprintf("%d", p.DeploymentKey),
	}

	if len(p.Resource) > 0 {
		// Check if it's JSON (starts with [) or XML (starts with <)
		// Simple heuristic
		trimmed := string(p.Resource) // trim whitespace if needed?
		if len(trimmed) > 0 && (trimmed[0] == '[' || trimmed[0] == '{') {
			// Assume JSON steps
			if err := json.Unmarshal(p.Resource, &wf.Steps); err != nil {
				return nil, fmt.Errorf("failed to unmarshal steps from process resource: %v", err)
			}
		} else {
			// Assume BPMN XML
			parsedWf, err := bpmn.Parse(bytes.NewReader(p.Resource))
			if err != nil {
				return nil, fmt.Errorf("failed to parse bpmn from process resource: %v", err)
			}
			wf.ProcessDefinitionID = parsedWf.ProcessDefinitionID // Should match p.BpmnProcessID
			wf.Name = parsedWf.Name
			wf.Steps = parsedWf.Steps
		}
		wf.BPMNXML = string(p.Resource)
	}
	return wf, nil
}

// Helper to get definition by Key (ID)
func (e *Engine) getWorkflowDefinition(ctx context.Context, processKey int64) (*model.WorkflowDefinition, error) {
	p, err := e.repo.GetProcess(ctx, processKey)
	if err != nil {
		return nil, err
	}
	return e.processToWorkflowDefinition(p)
}

// Helper to get definition by BPMN Process ID
func (e *Engine) getWorkflowDefinitionByBpmnProcessID(ctx context.Context, bpmnProcessID string) (*model.WorkflowDefinition, error) {
	p, err := e.repo.GetProcessByBpmnProcessID(ctx, bpmnProcessID)
	if err != nil {
		return nil, err
	}
	return e.processToWorkflowDefinition(p)
}

// Helper to get a specific version of a definition by BPMN Process ID.
func (e *Engine) getWorkflowDefinitionByBpmnProcessIDAndVersion(ctx context.Context, bpmnProcessID string, version int) (*model.WorkflowDefinition, error) {
	lookup, ok := e.repo.(processByVersionLookup)
	if !ok {
		return nil, fmt.Errorf("versioned process lookup is not available")
	}
	p, err := lookup.GetProcessByBpmnProcessIDAndVersion(ctx, bpmnProcessID, version)
	if err != nil {
		return nil, err
	}
	return e.processToWorkflowDefinition(p)
}
