package application

import (
	"context"
	"fmt"
	pb "github.com/artificialflow/artificialflow/backend/api/v1/go"
	"github.com/artificialflow/artificialflow/backend/libs/id"
	"github.com/artificialflow/artificialflow/backend/libs/model"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// proceedToken moves a token from its current step to the next
func (e *Engine) proceedToken(ctx context.Context, instance *model.WorkflowInstance, execID string, wf *model.WorkflowDefinition) error {
	// Find execution index
	execIdx := -1
	for i, ex := range instance.Executions {
		if ex.ID == execID {
			execIdx = i
			break
		}
	}
	if execIdx == -1 {
		return fmt.Errorf("execution %s not found", execID)
	}

	currentStepID := instance.Executions[execIdx].StepID

	// Find current step def
	currentStepDef := findStep(wf.Steps, currentStepID)
	if currentStepDef == nil {
		return fmt.Errorf("step %s definition not found", currentStepID)
	}

	// Apply Output Mappings (Local -> Global/Parent)
	if err := e.applyOutputMappings(ctx, instance, currentStepDef, execID); err != nil {
		// Log warning but maybe don't fail the whole transition?
		// For strict BPMN, failure here should probably raise an incident or error.
		return fmt.Errorf("failed to apply output mappings: %v", err)
	}

	// Determine Outgoing Transitions
	if len(currentStepDef.Outgoing) == 0 {
		// End of path
		instance.Executions[execIdx].Status = "COMPLETED"
		processInstanceKey, err := strconv.ParseInt(instance.ID, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid process instance id %q: %w", instance.ID, err)
		}

		// Engine: Complete current element instance (End Event or similar)
		if key := instance.Executions[execIdx].ElementInstanceKey; key != 0 {
			el := &model.ElementInstance{
				Key:     key,
				State:   "COMPLETED",
				EndTime: time.Now(),
			}
			if err := e.repo.UpdateElementInstance(ctx, el); err != nil {
				// log error?
			}

			// Publish ElementInstanceCompleted
			if err := e.eventPublisher.Publish(ctx, &pb.ElementInstanceCompleted{
				Key:                key,
				ProcessInstanceKey: processInstanceKey,
				ElementId:          currentStepID,
				EndTime:            timestamppb.New(el.EndTime),
			}, "ElementInstanceCompleted"); err != nil {
				fmt.Printf("failed to publish ElementInstanceCompleted: %v\n", err)
			}
		}

		if err := e.completeInstanceIfNoActiveExecutions(ctx, instance, wf); err != nil {
			return err
		}

		// Check for SubProcess completion (bubbling up)
		if err := e.checkSubProcessCompletion(ctx, instance, execID, wf); err != nil {
			return err
		}

		// Check for Call Activity completion (bubbling up to parent instance)
		if instance.ParentInstanceID != "" {
			if err := e.notifyParentProcess(ctx, instance); err != nil {
				return err
			}
		}

		return nil
	}

	// Handle Gateway Logic & Transitions (Split)
	targetIDs, err := e.determineNextStepIDs(ctx, instance, currentStepDef)
	if err != nil {
		return err
	}

	// Exit Logic for Multi-Instance Parallel
	// If current step was a Multi-Instance Parallel step, we act as a Merge point.
	if currentStepDef.LoopType == "PARALLEL" {
		// 1. Mark current child execution COMPLETED
		instance.Executions[execIdx].Status = "COMPLETED"
		if key := instance.Executions[execIdx].ElementInstanceKey; key != 0 {
			el := &model.ElementInstance{
				Key:     key,
				State:   "COMPLETED",
				EndTime: time.Now(),
			}
			e.repo.UpdateElementInstance(ctx, el)
		}

		// 2. Check if all siblings (same ParentID) are COMPLETED
		parentID := instance.Executions[execIdx].ParentID
		allCompleted := true
		for _, ex := range instance.Executions {
			if ex.ParentID == parentID && ex.Status != "COMPLETED" {
				allCompleted = false
				break
			}
		}

		if !allCompleted {
			return nil // Wait for others
		}

		// 3. All children done. Complete the Parent Scope execution.
		var parentExec *model.Execution
		var parentIdx int
		for i := range instance.Executions {
			if instance.Executions[i].ID == parentID {
				parentExec = &instance.Executions[i]
				parentIdx = i
				break
			}
		}

		if parentExec != nil {
			parentExec.Status = "COMPLETED"
			if key := parentExec.ElementInstanceKey; key != 0 {
				el := &model.ElementInstance{
					Key:     key,
					State:   "COMPLETED",
					EndTime: time.Now(),
				}
				e.repo.UpdateElementInstance(ctx, el)
			}
			// Use the Parent Execution to proceed to the *Next* steps
			// This effectively merges the flow back to one token.
			// We continue below, but using parentExec properties for "source" context if needed?
			// Actually, we just spawn next tokens from here.
			// But we need to use the parent's ParentID for the new tokens.
			instance.Executions[execIdx] = *parentExec // Swap context to parent for the loop below?
			// No, simpler: just set execIdx to parentIdx and proceed.
			execIdx = parentIdx
		} else {
			// No parent found? detailed error or fallback
			return fmt.Errorf("multi-instance parent execution %s not found", parentID)
		}
	} else if currentStepDef.LoopType == "SEQUENTIAL" {
		// Exit Logic for Multi-Instance Sequential
		// 1. Mark current child COMPLETED
		instance.Executions[execIdx].Status = "COMPLETED"
		if key := instance.Executions[execIdx].ElementInstanceKey; key != 0 {
			el := &model.ElementInstance{
				Key:     key,
				State:   "COMPLETED",
				EndTime: time.Now(),
			}
			e.repo.UpdateElementInstance(ctx, el)
		}

		// 2. Get Parent Scope
		parentID := instance.Executions[execIdx].ParentID
		var parentExec *model.Execution
		var parentIdx int
		for i := range instance.Executions {
			if instance.Executions[i].ID == parentID {
				parentExec = &instance.Executions[i]
				parentIdx = i
				break
			}
		}
		if parentExec == nil {
			return fmt.Errorf("multi-instance parent execution %s not found", parentID)
		}

		// 3. Check Loop State
		// We need to fetch the current loop index from variables
		// We assume variables were persisted. We need to query them using the Scope Key.
		// For simplicity, we use the value in instance.Context if available, but it might be stale?
		// Better to re-fetch specific variable or trust instance.Context has been updated?
		// Given we don't have easy access to repo.GetVariable here without extra calls,
		// let's try to read from instance.Context.
		// Note: The loop index variable should be named specifically to avoid collision.
		loopIdxVar := fmt.Sprintf("_loop_index_%s", parentID)
		loopTotalVar := fmt.Sprintf("_loop_total_%s", parentID)

		var currentIndex int
		var totalItems int

		if val, ok := instance.Context[loopIdxVar]; ok {
			if f, ok := val.(float64); ok {
				currentIndex = int(f)
			} else if i, ok := val.(int); ok {
				currentIndex = i
			}
		}
		if val, ok := instance.Context[loopTotalVar]; ok {
			if f, ok := val.(float64); ok {
				totalItems = int(f)
			} else if i, ok := val.(int); ok {
				totalItems = i
			}
		}

		// Increment
		currentIndex++

		if currentIndex < totalItems {
			// 4. Trigger Next Iteration
			// Update Index Variable
			if err := e.persistVariables(ctx, instance.ID, parentExec.ElementInstanceKey, map[string]any{
				loopIdxVar: currentIndex,
			}); err != nil {
				return err
			}
			// Update in-memory context for immediate use
			instance.Context[loopIdxVar] = currentIndex

			// Parse Process Instance Key locally as it is not yet defined in this scope
			piKey, _ := strconv.ParseInt(instance.ID, 10, 64)

			// Spawn Child
			childExec := model.Execution{
				ID:        generateRuntimeID(),
				StepID:    currentStepID,
				Status:    "ACTIVE",
				ParentID:  parentID,
				StartTime: time.Now(),
			}
			childKey := generateKey(childExec.ID)
			childExec.ElementInstanceKey = childKey

			// Get Collection for Loop Element
			var collection []any
			if colVar, ok := instance.Context[currentStepDef.LoopCollection]; ok {
				if slice, ok := colVar.([]any); ok {
					collection = slice
				} else if slice, ok := colVar.([]interface{}); ok {
					collection = slice
				}
			}

			// Inject Loop Element
			if currentIndex < len(collection) {
				// We can inject the item into the context if LoopElement is defined
				// BUT we should avoid polluting global context.
				// Ideally we create a LOCAL variable for the child scope.
				// However, our child execution creation logic below (autoAdvance) doesn't easily take local variables yet.
				// We can persist it now.
				if currentStepDef.LoopElement != "" {
					item := collection[currentIndex]
					if err := e.persistVariables(ctx, instance.ID, childKey, map[string]any{
						currentStepDef.LoopElement: item,
					}); err != nil {
						return err
					}
				}
			}

			elChild := &model.ElementInstance{
				Key:                  childKey,
				ProcessInstanceKey:   piKey,
				ProcessDefinitionKey: wf.ID,
				ElementID:            currentStepID,
				BpmnElementType:      string(currentStepDef.Type),
				FlowScopeKey:         parentExec.ElementInstanceKey,
				State:                "ACTIVATED",
				CreatedAt:            time.Now(),
			}
			e.repo.CreateElementInstance(ctx, elChild)
			instance.Executions = append(instance.Executions, childExec)

			// Advance Child
			return e.autoAdvance(ctx, instance, childExec.ID, wf)

		} else {
			// 5. All Done - Complete Scope
			parentExec.Status = "COMPLETED"
			if key := parentExec.ElementInstanceKey; key != 0 {
				el := &model.ElementInstance{
					Key:     key,
					State:   "COMPLETED",
					EndTime: time.Now(),
				}
				e.repo.UpdateElementInstance(ctx, el)
			}
			execIdx = parentIdx
		}

	} else {
		// Standard Step Completion
		instance.Executions[execIdx].Status = "COMPLETED"
		if key := instance.Executions[execIdx].ElementInstanceKey; key != 0 {
			el := &model.ElementInstance{
				Key:     key,
				State:   "COMPLETED",
				EndTime: time.Now(),
			}
			if err := e.repo.UpdateElementInstance(ctx, el); err != nil {
				return err
			}
			// Cancel pending boundary timers attached to this activity instance.
			if err := e.cancelCreatedTimersForElementInstance(ctx, key, 0); err != nil {
				return err
			}
		}
	}

	// Process Instance Key
	piKey, _ := strconv.ParseInt(instance.ID, 10, 64)

	// Always fork/spawn new tokens for next steps.
	for _, targetID := range targetIDs {
		// Check for Multi-Instance Entry
		targetStep := findStep(wf.Steps, targetID)

		loopType := ""
		var collection []any

		if targetStep != nil {
			loopType = targetStep.LoopType
			if loopType == "PARALLEL" || loopType == "SEQUENTIAL" {
				// Try to get collection
				if colVar, ok := instance.Context[targetStep.LoopCollection]; ok {
					if slice, ok := colVar.([]any); ok {
						collection = slice
					} else if slice, ok := colVar.([]interface{}); ok {
						collection = slice
					}
				}
			}
		}

		if (loopType == "PARALLEL" || loopType == "SEQUENTIAL") && len(collection) > 0 {
			// Multi-Instance Entry Logic

			// 1. Create Parent Scope Execution
			scopeExec := model.Execution{
				ID:        generateRuntimeID(),
				StepID:    targetID,
				Status:    "ACTIVE", // Scope is Active while children run
				ParentID:  instance.Executions[execIdx].ParentID,
				StartTime: time.Now(),
			}
			scopeKey := generateKey(scopeExec.ID)
			scopeExec.ElementInstanceKey = scopeKey

			// Create Scope Element Instance (Virtual)
			elScope := &model.ElementInstance{
				Key:                  scopeKey,
				ID:                   id.GenerateUUIDv7(),
				ProcessInstanceKey:   piKey,
				ProcessDefinitionKey: wf.ID,
				ElementID:            targetID,
				BpmnElementType:      string(targetStep.Type) + "_MI_SCOPE",
				FlowScopeKey:         e.getFlowScopeKey(instance, scopeExec.ParentID),
				State:                "ACTIVATED",
				CreatedAt:            time.Now(),
			}
			e.repo.CreateElementInstance(ctx, elScope)
			instance.Executions = append(instance.Executions, scopeExec)

			if loopType == "PARALLEL" {
				// 2. Spawn All Child Executions (Parallel)
				for _, item := range collection {
					childExec := model.Execution{
						ID:        generateRuntimeID(),
						StepID:    targetID,
						Status:    "ACTIVE",
						ParentID:  scopeExec.ID, // Parent is the Scope
						StartTime: time.Now(),
					}
					childKey := generateKey(childExec.ID)
					childExec.ElementInstanceKey = childKey

					elChild := &model.ElementInstance{
						Key:                  childKey,
						ID:                   id.GenerateUUIDv7(),
						ProcessInstanceKey:   piKey,
						ProcessDefinitionKey: wf.ID,
						ElementID:            targetID,
						BpmnElementType:      string(targetStep.Type),
						FlowScopeKey:         scopeKey,
						State:                "ACTIVATED",
						CreatedAt:            time.Now(),
					}
					e.repo.CreateElementInstance(ctx, elChild)

					// Inject Loop Element per child (scoped by element instance key)
					if targetStep.LoopElement != "" {
						if err := e.persistVariables(ctx, instance.ID, childKey, map[string]any{
							targetStep.LoopElement: item,
						}); err != nil {
							return err
						}
					}

					instance.Executions = append(instance.Executions, childExec)

					// Auto advance child
					if err := e.autoAdvance(ctx, instance, childExec.ID, wf); err != nil {
						return err
					}
				}
			} else { // SEQUENTIAL
				// 2. Spawn First Child Execution (Sequential)
				currentIndex := 0
				totalItems := len(collection)

				// Persist Loop State
				loopIdxVar := fmt.Sprintf("_loop_index_%s", scopeExec.ID)
				loopTotalVar := fmt.Sprintf("_loop_total_%s", scopeExec.ID)

				if err := e.persistVariables(ctx, instance.ID, scopeKey, map[string]any{
					loopIdxVar:   currentIndex,
					loopTotalVar: totalItems,
				}); err != nil {
					return err
				}
				// Update in-memory context
				instance.Context[loopIdxVar] = currentIndex
				instance.Context[loopTotalVar] = totalItems

				childExec := model.Execution{
					ID:        generateRuntimeID(),
					StepID:    targetID,
					Status:    "ACTIVE",
					ParentID:  scopeExec.ID,
					StartTime: time.Now(),
				}
				childKey := generateKey(childExec.ID)
				childExec.ElementInstanceKey = childKey

				// Inject Loop Element
				if targetStep.LoopElement != "" {
					item := collection[0]
					if err := e.persistVariables(ctx, instance.ID, childKey, map[string]any{
						targetStep.LoopElement: item,
					}); err != nil {
						return err
					}
				}

				elChild := &model.ElementInstance{
					Key:                  childKey,
					ID:                   id.GenerateUUIDv7(),
					ProcessInstanceKey:   piKey,
					ProcessDefinitionKey: wf.ID,
					ElementID:            targetID,
					BpmnElementType:      string(targetStep.Type),
					FlowScopeKey:         scopeKey,
					State:                "ACTIVATED",
					CreatedAt:            time.Now(),
				}
				e.repo.CreateElementInstance(ctx, elChild)
				instance.Executions = append(instance.Executions, childExec)

				if err := e.autoAdvance(ctx, instance, childExec.ID, wf); err != nil {
					return err
				}
			}

		} else {
			// Standard Single Token Creation
			newExec := model.Execution{
				ID:        generateRuntimeID(),
				StepID:    targetID,
				Status:    "ACTIVE",
				ParentID:  instance.Executions[execIdx].ParentID, // Inherit parent (Scope) instead of chaining
				StartTime: time.Now(),
			}
			// ... (rest of standard creation logic)

			// Engine: Create new Element Instance
			newKey := generateKey(newExec.ID)
			newExec.ElementInstanceKey = newKey

			nextStepType := "TASK"
			for _, s := range wf.Steps {
				if s.ID == targetID {
					nextStepType = string(s.Type)
					break
				}
			}

			el := &model.ElementInstance{
				Key:                  newKey,
				ID:                   id.GenerateUUIDv7(),
				ProcessInstanceKey:   piKey,
				ProcessDefinitionKey: wf.ID,
				ElementID:            targetID,
				BpmnElementType:      nextStepType,
				FlowScopeKey:         e.getFlowScopeKey(instance, newExec.ParentID),
				State:                "ACTIVATED",
				CreatedAt:            time.Now(),
			}
			if err := e.repo.CreateElementInstance(ctx, el); err != nil {
				// log error?
			}

			// --- Timer Creation Logic ---
			targetStep := findStep(wf.Steps, targetID)

			if targetStep != nil {
				// 1. Intermediate Timer Catch Event
				if targetStep.Type == model.StepTypeIntermediateTimerCatchEvent {
					if sched, err := resolveTimerSchedule(targetStep.Properties, time.Now()); err == nil {
						timer := &model.Timer{
							Key:                generateKey(newExec.ID + "_timer"),
							ID:                 id.GenerateUUIDv7(),
							ElementInstanceKey: newKey,
							ProcessInstanceKey: piKey,
							ElementID:          targetStep.ID,
							DueDate:            sched.DueDate,
							RepeatCount:        sched.RepeatCount,
							State:              "CREATED",
							CreatedAt:          time.Now(),
						}
						e.repo.CreateTimer(ctx, timer)
					}
				}

				// 2. Boundary Timers
				for _, refID := range targetStep.BoundaryEventRefs {
					var boundaryStep *model.StepDefinition
					for _, s := range wf.Steps {
						if s.ID == refID {
							boundaryStep = &s
							break
						}
					}
					if boundaryStep != nil && boundaryStep.Type == model.StepTypeBoundaryEvent {
						if sched, err := resolveTimerSchedule(boundaryStep.Properties, time.Now()); err == nil {
							timer := &model.Timer{
								Key:                generateKey(fmt.Sprintf("%s_boundary_timer_%s", newExec.ID, boundaryStep.ID)),
								ID:                 id.GenerateUUIDv7(),
								ElementInstanceKey: newKey, // Linked to the attached task instance
								ProcessInstanceKey: piKey,
								ElementID:          boundaryStep.ID,
								DueDate:            sched.DueDate,
								RepeatCount:        sched.RepeatCount,
								State:              "CREATED",
								CreatedAt:          time.Now(),
							}
							e.repo.CreateTimer(ctx, timer)
						}
					}
				}

				// --- Message Subscription Logic ---
				// 1. Intermediate Message Catch Event or Receive Task
				if targetStep.Type == model.StepTypeIntermediateCatchEvent || targetStep.Type == model.StepTypeReceiveTask {
					if msgRef, ok := targetStep.Properties["message_ref"].(string); ok && msgRef != "" {
						correlationKey := ""
						if ckProp, ok := targetStep.Properties["correlation_key"].(string); ok && ckProp != "" {
							if val, exists := instance.Context[ckProp]; exists {
								correlationKey = fmt.Sprintf("%v", val)
							} else {
								// Literal correlation key when no process variable matches.
								correlationKey = ckProp
							}
						}

						sub := &model.MessageSubscription{
							Key:                generateKey(newExec.ID + "_msg_sub"),
							ID:                 id.GenerateUUIDv7(),
							ElementInstanceKey: newKey,
							ProcessInstanceKey: piKey,
							ElementID:          targetStep.ID,
							MessageName:        msgRef,
							CorrelationKey:     correlationKey,
							State:              "OPEN",
							CreatedAt:          time.Now(),
						}
						e.repo.CreateMessageSubscription(ctx, sub)
					}
				}

				// 2. Boundary Message Events
				for _, refID := range targetStep.BoundaryEventRefs {
					// Find boundary step
					var boundaryStep *model.StepDefinition
					for _, s := range wf.Steps {
						if s.ID == refID {
							boundaryStep = &s
							break
						}
					}

					if boundaryStep != nil && boundaryStep.Type == model.StepTypeBoundaryEvent {
						if msgRef, ok := boundaryStep.Properties["message_ref"].(string); ok && msgRef != "" {
							correlationKey := ""
							if ckProp, ok := boundaryStep.Properties["correlation_key"].(string); ok && ckProp != "" {
								if val, exists := instance.Context[ckProp]; exists {
									correlationKey = fmt.Sprintf("%v", val)
								} else {
									correlationKey = ckProp
								}
							}

							sub := &model.MessageSubscription{
								Key:                generateKey(fmt.Sprintf("%s_boundary_msg_%s", newExec.ID, boundaryStep.ID)),
								ID:                 id.GenerateUUIDv7(),
								ElementInstanceKey: newKey, // Linked to the attached task instance
								ProcessInstanceKey: piKey,
								ElementID:          boundaryStep.ID,
								MessageName:        msgRef,
								CorrelationKey:     correlationKey,
								State:              "OPEN",
								CreatedAt:          time.Now(),
							}
							e.repo.CreateMessageSubscription(ctx, sub)
						}
					}
				}
				// -----------------------------
			}
			// -----------------------------

			instance.Executions = append(instance.Executions, newExec)

			// Recursively proceed if the next step is automatic (Start, Gateway, End)
			if err := e.autoAdvance(ctx, instance, newExec.ID, wf); err != nil {
				return err
			}
		}
	}

	return e.completeInstanceIfNoActiveExecutions(ctx, instance, wf)
}

func (e *Engine) completeInstanceIfNoActiveExecutions(ctx context.Context, instance *model.WorkflowInstance, wf *model.WorkflowDefinition) error {
	if instance.Status == model.StatusCompleted {
		return nil
	}
	for _, ex := range instance.Executions {
		if ex.Status == "ACTIVE" {
			return nil
		}
	}

	processInstanceKey, err := strconv.ParseInt(instance.ID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid process instance id %q: %w", instance.ID, err)
	}

	instance.Status = model.StatusCompleted
	pi := &model.ProcessInstance{
		Key:     processInstanceKey,
		State:   "COMPLETED",
		EndTime: time.Now(),
	}
	if err := e.repo.UpdateProcessInstance(ctx, pi); err != nil {
		return err
	}

	if err := e.eventPublisher.Publish(ctx, &pb.ProcessInstanceCompleted{
		Key:                  processInstanceKey,
		ProcessDefinitionKey: wf.ID,
		EndTime:              timestamppb.New(pi.EndTime),
	}, "ProcessInstanceCompleted"); err != nil {
		fmt.Printf("failed to publish ProcessInstanceCompleted: %v\n", err)
	}

	return nil
}

func (e *Engine) getFlowScopeKey(instance *model.WorkflowInstance, parentID string) int64 {
	if parentID == "" {
		k, _ := strconv.ParseInt(instance.ID, 10, 64)
		return k
	}
	for _, ex := range instance.Executions {
		if ex.ID == parentID {
			if ex.ElementInstanceKey != 0 {
				return ex.ElementInstanceKey
			}
			break
		}
	}
	k, _ := strconv.ParseInt(instance.ID, 10, 64)
	return k
}

func (e *Engine) autoAdvance(ctx context.Context, instance *model.WorkflowInstance, execID string, wf *model.WorkflowDefinition) error {

	// Find execution
	var exec *model.Execution
	for i := range instance.Executions {
		if instance.Executions[i].ID == execID {
			exec = &instance.Executions[i]
			break
		}
	}
	if exec == nil {
		return nil
	}

	// Find Step
	step := findStep(wf.Steps, exec.StepID)
	if step == nil {
		return fmt.Errorf("step not found")
	}

	// Check for registered executor
	executor, ok := e.stepExecutors[step.Type]
	if !ok {
		// If no executor registered, it's likely a wait state (UserTask, ManualTask, etc.) or unknown.
		// Just return nil to wait for external completion.
		return nil
	}

	// Apply Input Mappings (Global/Parent -> Local)
	if err := e.applyInputMappings(ctx, instance, step, execID); err != nil {
		return fmt.Errorf("failed to apply input mappings: %v", err)
	}

	// Execute Step Strategy
	err := executor.Execute(ctx, e, instance, step, execID, wf)
	if err != nil {
		handled, handlerErr := e.handleBoundaryError(ctx, instance, step, execID, wf, err)
		if handled {
			return handlerErr
		}

		// Not handled by boundary event. Raise Incident.
		// log error?

		incidentKey := generateKey(fmt.Sprintf("%s_incident_%d", exec.ID, time.Now().UnixNano()))
		incident := &model.Incident{
			Key:                incidentKey,
			ID:                 id.GenerateUUIDv7(),
			ProcessInstanceKey: generateKey(instance.ID),
			ElementInstanceKey: exec.ElementInstanceKey,
			ErrorType:          "EXECUTION_ERROR",
			ErrorMessage:       err.Error(),
			State:              "CREATED",
			CreatedAt:          time.Now(),
		}
		if createErr := e.repo.CreateIncident(ctx, incident); createErr != nil {
			// log error?
			// Return original error if incident creation fails
			return err
		}

		// Return nil to "swallow" the error and halt execution at this step.
		// The execution remains in its current state (likely ACTIVE), allowing for inspection/resolution.
		return nil
	}

	return nil
}

// Helper to recursively find a step definition
func findStep(steps []model.StepDefinition, id string) *model.StepDefinition {
	for i := range steps {
		if steps[i].ID == id {
			return &steps[i]
		}
		if len(steps[i].SubSteps) > 0 {
			if found := findStep(steps[i].SubSteps, id); found != nil {
				return found
			}
		}
	}
	return nil
}
