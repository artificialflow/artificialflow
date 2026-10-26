# Process-instance migration support

ArtificialFlow v1.2 exposes a deliberately bounded, synchronous administrative
migration for one active instance at a time. Always run the dry-run endpoint
with the complete mapping immediately before apply.

## Supported contract

- The source and target are explicit definition keys in the caller's tenant,
  share the same BPMN process ID, and the target has a higher version.
- The instance is active and remains pinned to the expected source version
  until the apply transaction obtains its row lock.
- Active flat-scope elements map explicitly to an existing target element of
  the same BPMN type.
- Created user-task jobs, created timers, and open message subscriptions are
  migrated when every referenced element ID is mapped.
- Variables and completed history are preserved. Active element instances,
  supported jobs, timers, and subscriptions are updated atomically.
- Apply writes the migration operation and immutable audit event in the same
  transaction. If either write fails, every state change rolls back.
- A caller-supplied `operationId` is idempotent only for an identical instance,
  source, target, and mapping. Reuse with changed input returns
  `OPERATION_CONFLICT`.

## Wait-state support matrix

Only the active rows listed below are rewritten. Rows in completed, triggered,
correlated, closed, canceled, terminated, failed, or otherwise historical
states remain attached to the source definition and retain their original
element IDs.

| Persisted runtime row | Supported active state | Apply behavior |
| :--- | :--- | :--- |
| Process instance | `ACTIVE` | Definition key and cached version move to the target. |
| Flat element instance | `ACTIVATED` | Element ID and definition key are mapped. This includes explicitly mapped boundary and intermediate catch-event waits. |
| User-task job (`artificialflow:userTask`) | `CREATED` | Element ID and definition key are mapped. Multiple created jobs and parallel active tokens are supported when every active element is mapped. |
| Timer | `CREATED` | Element ID is mapped; due date and claim data are preserved. Boundary and intermediate timers are supported. |
| Message subscription | `OPEN` | Element ID is mapped; message name and correlation key are preserved. Boundary and intermediate message waits are supported. |
| Variables | Any persisted value | Preserved without rewriting. |
| Completed/history rows | Any non-active state | Never rewritten. |

This matrix does not claim general BPMN-token transformation. Parallel tokens
are supported only as multiple independent, flat active rows with complete
same-type mappings.

## Explicitly unsupported

- Multi-instance bodies
- Nested subprocesses and call activities
- Event subprocesses
- Compensation handlers or compensation events
- Transaction and ad-hoc subprocesses
- Transitional `ACTIVATING` or `COMPLETING` element rows
- Activated external work or non-user-task jobs
- Unresolved incidents
- Same-version, older-version, cross-lineage, or cross-tenant targets
- Mapping between different BPMN element types
- Batch migration, automatic migration on deployment, and in-place reversal

Unsupported requests return `REJECTED` with a stable `reasonCode`; they are not
approximated. Drain and restart on the new definition remains the default for
workloads outside this contract.

## Undo and recovery

There is no in-place undo operation. Before apply, retain a database backup or
recovery point and the dry-run result, source/target definitions, complete
mapping, actor, and `operationId`. If the post-apply result is wrong, stop work
for that instance and restore the database to the pre-apply recovery point.
Do not attempt to reverse the mapping by migrating to the older definition:
targets must be newer and completed history intentionally remains on the
source. If a full restore is not acceptable, deploy a corrected newer version
and dry-run a new forward migration only when its state is covered by this
matrix.

## API sequence

1. `POST /api/instances/{id}/migration/dry-run`
2. Review `status`, versions, and any `reasonCode`.
3. `POST /api/instances/{id}/migration` with the same definition keys and
   mapping. Supply a stable `operationId` for safe transport retries.
4. Confirm `APPLIED` and retain the operation ID with release evidence.

The additive `/api/v1/...` aliases expose the same behavior.
