package application

import (
	"context"
	"fmt"
	"github.com/artificialflow/artificialflow/backend/libs/logger"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"strings"
	"time"
)

// checkGatewayJoin checks if a gateway step is ready to proceed (Join logic)
// Returns: ready (bool), indices of executions to consume (merge), error
func (e *Engine) checkGatewayJoin(instance *model.WorkflowInstance, step *model.StepDefinition, wf *model.WorkflowDefinition) (bool, []int, error) {
	// Find all active executions at this step
	var atGatewayIndices []int
	for i := range instance.Executions {
		if instance.Executions[i].Status == "ACTIVE" && instance.Executions[i].StepID == step.ID {
			atGatewayIndices = append(atGatewayIndices, i)
		}
	}

	// Parallel Gateway: Wait for ALL incoming paths
	if step.Type == model.StepTypeGatewayParallel {
		if len(atGatewayIndices) >= len(step.Incoming) {
			return true, atGatewayIndices, nil
		}
		return false, nil, nil
	}

	// Inclusive Gateway: Wait for all "upstream active" paths
	if step.Type == model.StepTypeGatewayInclusive {
		// If we have tokens for ALL incoming, we are definitely ready
		if len(atGatewayIndices) >= len(step.Incoming) {
			return true, atGatewayIndices, nil
		}

		// Otherwise, check if any OTHER token in the instance can reach this gateway
		canOthersReach := false
		for i := range instance.Executions {
			ex := &instance.Executions[i]
			// Ignore executions already at the gateway
			if ex.Status == "ACTIVE" && ex.StepID != step.ID {
				// Check reachability
				if e.canReach(ex.StepID, step.ID, wf) {
					canOthersReach = true
					break
				}
			}
		}

		if !canOthersReach {
			// No other active token can reach this gateway, so we are ready
			return true, atGatewayIndices, nil
		}
		return false, nil, nil
	}

	// Exclusive Gateway: Pass through (no join logic usually, acts as XOR merge)
	// Just proceed (ready=true), consume only self (which is handled by caller logic usually, but here we just return empty others)
	return true, []int{}, nil
}

// determineNextStepIDs calculates which steps to proceed to based on the current step type and conditions
func (e *Engine) determineNextStepIDs(ctx context.Context, instance *model.WorkflowInstance, step *model.StepDefinition) ([]string, error) {
	var targetIDs []string

	// Parallel Gateway or Event-Based Gateway: Take ALL paths
	if step.Type == model.StepTypeGatewayParallel || step.Type == model.StepTypeGatewayEventBased {
		for _, t := range step.Outgoing {
			targetIDs = append(targetIDs, t.TargetRef)
		}
		return targetIDs, nil
	}

	// Inclusive Gateway: Take ALL matching paths
	if step.Type == model.StepTypeGatewayInclusive {
		matched := false
		for _, t := range step.Outgoing {
			// Skip default flow in condition evaluation, we'll add it later if nothing matches?
			// Actually BPMN says: evaluating conditions... if true, take it.
			// "Default" is taken ONLY if no other path is taken.
			// However, for Inclusive, we can take multiple. The Default is taken if NO OTHER path is valid.

			// If this transition is the default flow, skip explicit evaluation here (it's the fallback)
			if step.DefaultFlow != "" && step.DefaultFlow == t.ID {
				continue
			}

			if t.Condition == "" || e.evaluateCondition(ctx, t.Condition, instance.Context) {
				targetIDs = append(targetIDs, t.TargetRef)
				matched = true
			}
		}

		// If no paths matched, and we have a default flow, take it.
		if !matched && step.DefaultFlow != "" {
			// Find target of default flow
			for _, t := range step.Outgoing {
				if t.ID == step.DefaultFlow {
					targetIDs = append(targetIDs, t.TargetRef)
					break
				}
			}
		}

		if len(targetIDs) == 0 {
			return nil, fmt.Errorf("no matching path for inclusive gateway %s", step.ID)
		}
		return targetIDs, nil
	}

	// Exclusive Gateway or Normal Step: Take FIRST matching path
	for _, t := range step.Outgoing {
		// Skip default flow for now
		if step.DefaultFlow != "" && step.DefaultFlow == t.ID {
			continue
		}

		if t.Condition == "" || e.evaluateCondition(ctx, t.Condition, instance.Context) {
			targetIDs = append(targetIDs, t.TargetRef)
			// For Exclusive, we stop at first match
			return targetIDs, nil
		}
	}

	// If we are here, no conditional path matched. Check default.
	if step.DefaultFlow != "" {
		for _, t := range step.Outgoing {
			if t.ID == step.DefaultFlow {
				targetIDs = append(targetIDs, t.TargetRef)
				return targetIDs, nil
			}
		}
	}

	if len(targetIDs) == 0 {
		return nil, fmt.Errorf("no outgoing path found for step %s", step.ID)
	}

	return targetIDs, nil
}

// canReach checks if there is a path from startStepID to targetStepID (BFS)
func (e *Engine) canReach(startStepID, targetStepID string, wf *model.WorkflowDefinition) bool {
	if startStepID == targetStepID {
		return true
	}

	visited := make(map[string]bool)
	queue := []string{startStepID}
	visited[startStepID] = true

	// Build map for quick lookup (recursive)
	stepMap := make(map[string]*model.StepDefinition)
	var indexSteps func([]model.StepDefinition)
	indexSteps = func(steps []model.StepDefinition) {
		for i := range steps {
			stepMap[steps[i].ID] = &steps[i]
			if len(steps[i].SubSteps) > 0 {
				indexSteps(steps[i].SubSteps)
			}
		}
	}
	indexSteps(wf.Steps)

	for len(queue) > 0 {
		currID := queue[0]
		queue = queue[1:]

		if currID == targetStepID {
			return true
		}

		step, ok := stepMap[currID]
		if !ok {
			continue
		}

		for _, t := range step.Outgoing {
			if !visited[t.TargetRef] {
				visited[t.TargetRef] = true
				queue = append(queue, t.TargetRef)
			}
		}
	}
	return false
}

// evaluateCondition checks if a condition is met based on the context.
// Uses goja for JavaScript evaluation.
func (e *Engine) evaluateCondition(ctx context.Context, condition string, context map[string]any) bool {
	if condition == "" {
		return true // Empty condition usually means "always true" or handled elsewhere, but here we expect condition to be present if calling this.
		// Actually, standard BPMN: if condition empty on conditional flow, it's true? No, usually implies default or unconditional.
		// But let's stick to safe default.
	}
	condition = normalizeConditionForEvaluation(condition)
	if condition == "true" {
		return true
	}
	if condition == "false" {
		return false
	}

	_, val, err := evaluateJavaScriptExpression(condition, context, defaultGatewayConditionTimeout, expressionKindCondition)
	if err != nil {
		logger.Default().Warn(ctx, "Condition evaluation failed", map[string]any{
			"condition": condition,
			"error":     err.Error(),
		})
		return false
	}

	if val == nil {
		return false
	}

	// Export to Go value and check boolean
	export := val.Export()
	if b, ok := export.(bool); ok {
		return b
	}

	// Handle non-boolean results (truthy/falsy)
	// goja Value.ToBoolean() handles this
	return val.ToBoolean()
}

func normalizeConditionForEvaluation(condition string) string {
	trimmed := strings.TrimSpace(condition)
	if rewritten, ok := rewriteSingleEqualsComparison(trimmed); ok {
		return rewritten
	}
	return trimmed
}

func rewriteSingleEqualsComparison(expr string) (string, bool) {
	inSingle, inDouble := false, false
	escaped := false
	plainEqIdx := -1

	for i := 0; i < len(expr); i++ {
		ch := expr[i]

		if escaped {
			escaped = false
			continue
		}

		if (inSingle || inDouble) && ch == '\\' {
			escaped = true
			continue
		}

		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}

		if inSingle || inDouble || ch != '=' {
			continue
		}

		prev := byte(0)
		if i > 0 {
			prev = expr[i-1]
		}
		next := byte(0)
		if i+1 < len(expr) {
			next = expr[i+1]
		}

		// Keep existing operators intact (==, ===, !=, <=, >=, =>).
		if prev == '=' || next == '=' || prev == '!' || prev == '<' || prev == '>' || next == '>' {
			continue
		}

		if plainEqIdx != -1 {
			return expr, false
		}
		plainEqIdx = i
	}

	if plainEqIdx == -1 {
		return expr, false
	}

	return expr[:plainEqIdx] + "==" + expr[plainEqIdx+1:], true
}

// cancelEventGatewaySiblings checks if the triggered step is part of an Event-Based Gateway configuration
// and cancels all other concurrent events (siblings) waiting at that gateway.
func (e *Engine) cancelEventGatewaySiblings(ctx context.Context, instance *model.WorkflowInstance, triggeredStep *model.StepDefinition, parentExecutionID string, wf *model.WorkflowDefinition) error {
	// 1. Identify current step
	currentStep := triggeredStep
	if currentStep == nil {
		return nil
	}

	// 2. Check Incoming for EventBasedGateway
	// We need to find if any incoming flow comes from an EventBasedGateway
	var gatewayID string
	for _, sourceID := range currentStep.Incoming {
		// Find source step definition
		for _, s := range wf.Steps {
			if s.ID == sourceID {
				if s.Type == model.StepTypeGatewayEventBased {
					gatewayID = s.ID
				}
				break
			}
		}
		if gatewayID != "" {
			break
		}
	}

	if gatewayID == "" {
		return nil // Not triggered from an Event Based Gateway
	}

	// 3. Find the Gateway definition to get all its outgoing paths (siblings)
	var gatewayStep *model.StepDefinition
	for _, s := range wf.Steps {
		if s.ID == gatewayID {
			gatewayStep = &s
			break
		}
	}

	if gatewayStep == nil {
		return nil
	}

	// Identify sibling step IDs (target refs of the gateway, excluding current one)
	siblingStepIDs := make(map[string]bool)
	for _, t := range gatewayStep.Outgoing {
		if t.TargetRef != currentStep.ID {
			siblingStepIDs[t.TargetRef] = true
		}
	}

	if len(siblingStepIDs) == 0 {
		return nil
	}

	// 4. Terminate active executions at sibling steps
	for i := range instance.Executions {
		ex := &instance.Executions[i]
		// Check if execution is ACTIVE and is at one of the sibling steps
		// Also ensure it shares the same ParentID (scope) to be safe
		if ex.Status == "ACTIVE" && siblingStepIDs[ex.StepID] {
			if ex.ParentID == parentExecutionID { // Same scope
				ex.Status = "TERMINATED"

				// Engine: Terminate element instance
				if ex.ElementInstanceKey != 0 {
					el := &model.ElementInstance{
						Key:     ex.ElementInstanceKey,
						State:   "TERMINATED",
						EndTime: time.Now(),
					}
					if err := e.repo.UpdateElementInstance(ctx, el); err != nil {
						return err
					}
					if err := e.cancelCreatedTimersForElementInstance(ctx, ex.ElementInstanceKey, 0); err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}
