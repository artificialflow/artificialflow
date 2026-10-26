# ADR-0004: Bounded process-instance migration

- Status: Accepted
- Date: 2026-08-06

## Context

Each deployment creates a new process version. Running instances remain pinned
to their original definition, and `1.x` does not silently migrate tokens.
Unbounded remapping of arbitrary active state would make timers, jobs,
subscriptions, incidents, and history ambiguous.

## Decision

Drain remains the default and existing `1.x` behavior. Instance migration is an
explicit, synchronous administrative command for one instance, with an
optional stable operation ID, dry-run result, and per-instance outcome. It
never changes how new instances choose a definition. Batch and automatic
migration are outside the supported `1.x` contract.

A migration plan pins source and target definition keys and maps stable BPMN
element IDs. The service accepts an instance only when it:

- belongs to the caller's tenant and exact source definition;
- is not terminal, already migrating, or claimed by another migration;
- is at a persisted wait state with no executing service task;
- has no active external-worker lock, in-flight connector effect, or unresolved
  incident;
- can map every supported active element, timer, message subscription, and
  created user task to an existing target element of the same BPMN type.

The dry run validates the instance without mutation. Apply locks and
revalidates the instance, writes the target definition and mapped state in one
transaction, and appends immutable operation and audit records. A failed
operation is unchanged. Reusing an operation ID is accepted only when the
instance, source, target, and mapping are identical.

Variables are preserved unless a reviewed deterministic transform is included
in the plan. Completed history is never rewritten. Existing job completions,
messages, and idempotency records remain correlated to their original
identities. Undo is a new validated migration plan or restore procedure, not
history deletion.

## Consequences

- Arbitrary running-state conversion is deliberately unsupported.
- API responses report a validated, applied, or rejected outcome with stable
  reason codes; operators can audit actor, tenant, mapping, and versions.
- Migration tests cover created human tasks, timers, messages, idempotent
  retry, incompatible mappings, concurrent source changes, and atomic rollback.
- No automatic migration is introduced into deployment or startup.
- The release support matrix is maintained in
  [INSTANCE_MIGRATION.md](../INSTANCE_MIGRATION.md).
