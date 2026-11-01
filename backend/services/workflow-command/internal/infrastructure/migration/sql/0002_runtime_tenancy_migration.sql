-- Expand the v1.1 schema for tenant isolation, multi-active fenced runtime
-- work, immutable outbox envelopes, and bounded process-instance migration.
-- All changes are additive and preserve the reserved v1.x default tenant.

ALTER TABLE process ALTER COLUMN tenant_id SET DEFAULT 'default';
UPDATE process SET tenant_id = 'default' WHERE tenant_id IS NULL OR btrim(tenant_id) = '';
ALTER TABLE process ALTER COLUMN tenant_id SET NOT NULL;

ALTER TABLE process_instance ADD COLUMN IF NOT EXISTS tenant_id text;
ALTER TABLE element_instance ADD COLUMN IF NOT EXISTS tenant_id text;
ALTER TABLE variable ADD COLUMN IF NOT EXISTS tenant_id text;
ALTER TABLE job ADD COLUMN IF NOT EXISTS tenant_id text;
ALTER TABLE incident ADD COLUMN IF NOT EXISTS tenant_id text;
ALTER TABLE timer ADD COLUMN IF NOT EXISTS tenant_id text;
ALTER TABLE message_subscription ADD COLUMN IF NOT EXISTS tenant_id text;
ALTER TABLE idempotency_record ADD COLUMN IF NOT EXISTS tenant_id text;
ALTER TABLE outbox_message ADD COLUMN IF NOT EXISTS tenant_id text;
ALTER TABLE decision_definition ADD COLUMN IF NOT EXISTS tenant_id text;

DO $$
DECLARE table_name text;
BEGIN
  FOREACH table_name IN ARRAY ARRAY[
    'process_instance', 'element_instance', 'variable', 'job', 'incident',
    'timer', 'message_subscription', 'idempotency_record', 'outbox_message',
    'decision_definition'
  ] LOOP
    EXECUTE format('UPDATE %I SET tenant_id = %L WHERE tenant_id IS NULL OR btrim(tenant_id) = %L', table_name, 'default', '');
    EXECUTE format('ALTER TABLE %I ALTER COLUMN tenant_id SET DEFAULT %L', table_name, 'default');
    EXECUTE format('ALTER TABLE %I ALTER COLUMN tenant_id SET NOT NULL', table_name);
  END LOOP;
END $$;

-- Immutable delivery envelope. These values are created with the state
-- transition and are reused verbatim on every broker retry.
ALTER TABLE outbox_message ADD COLUMN IF NOT EXISTS aggregate_type varchar(64);
ALTER TABLE outbox_message ADD COLUMN IF NOT EXISTS aggregate_id varchar(256);
ALTER TABLE outbox_message ADD COLUMN IF NOT EXISTS schema_version integer;
ALTER TABLE outbox_message ADD COLUMN IF NOT EXISTS occurred_at timestamptz;
ALTER TABLE outbox_message ADD COLUMN IF NOT EXISTS trace_context jsonb;
UPDATE outbox_message
SET aggregate_type = COALESCE(aggregate_type, 'legacy'),
    aggregate_id = COALESCE(aggregate_id, id),
    schema_version = COALESCE(schema_version, 1),
    occurred_at = COALESCE(occurred_at, created_at, transaction_timestamp()),
    trace_context = COALESCE(trace_context, '{}'::jsonb);
ALTER TABLE outbox_message ALTER COLUMN aggregate_type SET NOT NULL;
ALTER TABLE outbox_message ALTER COLUMN aggregate_id SET NOT NULL;
ALTER TABLE outbox_message ALTER COLUMN schema_version SET NOT NULL;
ALTER TABLE outbox_message ALTER COLUMN occurred_at SET NOT NULL;
ALTER TABLE outbox_message ALTER COLUMN trace_context SET NOT NULL;

-- Per-row leases use PostgreSQL time and a generation that increases on every
-- ownership transition. Completion must compare owner and generation.
ALTER TABLE timer ADD COLUMN IF NOT EXISTS claim_owner varchar(256);
ALTER TABLE timer ADD COLUMN IF NOT EXISTS claim_expires_at timestamptz;
ALTER TABLE timer ADD COLUMN IF NOT EXISTS claim_generation bigint NOT NULL DEFAULT 0;
ALTER TABLE job ADD COLUMN IF NOT EXISTS claim_owner varchar(256);
ALTER TABLE job ADD COLUMN IF NOT EXISTS claim_expires_at timestamptz;
ALTER TABLE job ADD COLUMN IF NOT EXISTS claim_generation bigint NOT NULL DEFAULT 0;
ALTER TABLE outbox_message ADD COLUMN IF NOT EXISTS claim_owner varchar(256);
ALTER TABLE outbox_message ADD COLUMN IF NOT EXISTS claim_expires_at timestamptz;
ALTER TABLE outbox_message ADD COLUMN IF NOT EXISTS claim_generation bigint NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS runtime_work_item (
    tenant_id text NOT NULL DEFAULT 'default',
    work_type varchar(64) NOT NULL,
    work_key varchar(256) NOT NULL,
    available_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    claim_owner varchar(256),
    claimed_at timestamptz,
    claim_expires_at timestamptz,
    claim_generation bigint NOT NULL DEFAULT 0,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (tenant_id, work_type, work_key)
);

-- A stable item per tenant serializes each scan/maintenance category while
-- timers and SLA rows are divided independently across active replicas.
INSERT INTO runtime_work_item (tenant_id, work_type, work_key)
SELECT tenants.tenant_id, work_type, shard::text
FROM (
  SELECT 'default'::text AS tenant_id
  UNION SELECT tenant_id FROM process
  UNION SELECT tenant_id FROM process_instance
  UNION SELECT tenant_id FROM element_instance
  UNION SELECT tenant_id FROM variable
  UNION SELECT tenant_id FROM job
  UNION SELECT tenant_id FROM incident
  UNION SELECT tenant_id FROM timer
  UNION SELECT tenant_id FROM message_subscription
  UNION SELECT tenant_id FROM idempotency_record
  UNION SELECT tenant_id FROM outbox_message
  UNION SELECT tenant_id FROM decision_definition
) tenants
CROSS JOIN unnest(ARRAY['CONDITIONAL_SCAN', 'TIMER_START_SCAN', 'IDEMPOTENCY_MAINTENANCE']) work_type
CROSS JOIN generate_series(0, 0) shard
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS process_instance_migration (
    tenant_id text NOT NULL DEFAULT 'default',
    operation_id varchar(64) NOT NULL,
    instance_key bigint NOT NULL,
    source_definition_key bigint NOT NULL,
    target_definition_key bigint NOT NULL,
    source_version bigint NOT NULL,
    target_version bigint NOT NULL,
    dry_run boolean NOT NULL,
    status varchar(32) NOT NULL,
    reason_code varchar(64),
    mapping jsonb NOT NULL,
    actor varchar(256) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    completed_at timestamptz,
    PRIMARY KEY (tenant_id, operation_id)
);

CREATE TABLE IF NOT EXISTS process_instance_migration_audit (
    tenant_id text NOT NULL DEFAULT 'default',
    event_id varchar(64) NOT NULL,
    operation_id varchar(64) NOT NULL,
    instance_key bigint NOT NULL,
    source_definition_key bigint NOT NULL,
    target_definition_key bigint NOT NULL,
    mapping jsonb NOT NULL,
    actor varchar(256) NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (tenant_id, event_id)
);

-- Replace legacy global uniqueness where the business identity is tenant-owned.
DROP INDEX IF EXISTS idx_decision_definition_decision_id;
ALTER TABLE variable DROP CONSTRAINT IF EXISTS variable_pkey;
ALTER TABLE variable
  ADD CONSTRAINT variable_pkey PRIMARY KEY (tenant_id, scope_key);
ALTER TABLE idempotency_record DROP CONSTRAINT IF EXISTS idempotency_record_pkey;
ALTER TABLE idempotency_record
  ADD CONSTRAINT idempotency_record_pkey PRIMARY KEY (tenant_id, key, operation);
CREATE UNIQUE INDEX IF NOT EXISTS uq_process_tenant_bpmn_version
  ON process (tenant_id, bpmn_process_id, version);
CREATE UNIQUE INDEX IF NOT EXISTS uq_decision_tenant_id
  ON decision_definition (tenant_id, decision_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_idempotency_tenant_key_operation
  ON idempotency_record (tenant_id, key, operation);
CREATE UNIQUE INDEX IF NOT EXISTS uq_outbox_tenant_event
  ON outbox_message (tenant_id, id);

-- Composite partial indexes for runtime and correlation hot paths.
CREATE INDEX IF NOT EXISTS idx_timer_claim_hot
  ON timer (tenant_id, due_date, key)
  WHERE state = 'CREATED';
CREATE INDEX IF NOT EXISTS idx_job_sla_claim_hot
  ON job (tenant_id, due_date, key)
  WHERE state IN ('CREATED', 'ACTIVATED') AND breached_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_job_activation_hot
  ON job (tenant_id, type, state, lock_expiration_time, created_at);
CREATE INDEX IF NOT EXISTS idx_message_correlation_hot
  ON message_subscription (tenant_id, message_name, correlation_key, key)
  WHERE state = 'OPEN';
CREATE INDEX IF NOT EXISTS idx_element_active_hot
  ON element_instance (tenant_id, process_instance_key, state, bpmn_element_type);
CREATE INDEX IF NOT EXISTS idx_outbox_claim_hot
  ON outbox_message (tenant_id, status, next_attempt, claim_expires_at, created_at);
CREATE INDEX IF NOT EXISTS idx_runtime_work_claim_hot
  ON runtime_work_item (work_type, available_at, claim_expires_at, tenant_id, work_key);
CREATE INDEX IF NOT EXISTS idx_process_instance_tenant_state
  ON process_instance (tenant_id, state, created_at);
CREATE INDEX IF NOT EXISTS idx_migration_instance
  ON process_instance_migration (tenant_id, instance_key, created_at DESC);
