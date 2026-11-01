-- Baseline the v1.1.0 command schema. Keep the singular table names stable:
-- they are part of the configured Debezium capture contract.
CREATE TABLE IF NOT EXISTS process (
    key bigint PRIMARY KEY,
    bpmn_process_id text,
    version bigint,
    resource_name text,
    deployment_key bigint,
    resource bytea,
    resource_checksum text,
    tenant_id text,
    created_at timestamptz
);

CREATE TABLE IF NOT EXISTS process_instance (
    key bigint PRIMARY KEY,
    id text,
    process_definition_key bigint,
    version bigint,
    parent_process_instance_key bigint,
    parent_element_instance_key bigint,
    state text,
    created_at timestamptz,
    end_time timestamptz
);

CREATE TABLE IF NOT EXISTS element_instance (
    key bigint PRIMARY KEY,
    id text,
    process_instance_key bigint,
    process_definition_key bigint,
    element_id text,
    bpmn_element_type text,
    flow_scope_key bigint,
    state text,
    created_at timestamptz,
    end_time timestamptz
);

CREATE TABLE IF NOT EXISTS variable (
    scope_key bigint PRIMARY KEY,
    process_instance_key bigint,
    name text,
    value jsonb,
    created_at timestamptz,
    updated_at timestamptz
);

CREATE TABLE IF NOT EXISTS job (
    key bigint PRIMARY KEY,
    id text,
    type text,
    process_instance_key bigint,
    element_instance_key bigint,
    process_definition_key bigint,
    element_id text,
    worker text,
    retries bigint,
    state text,
    assignee text,
    candidate_users text,
    candidate_groups text,
    lock_expiration_time timestamptz,
    due_date timestamptz,
    breached_at timestamptz,
    created_at timestamptz,
    updated_at timestamptz
);

CREATE TABLE IF NOT EXISTS incident (
    key bigint PRIMARY KEY,
    id text,
    process_instance_key bigint,
    element_instance_key bigint,
    job_key bigint,
    error_type text,
    error_message text,
    state text,
    created_at timestamptz,
    resolved_at timestamptz
);

CREATE TABLE IF NOT EXISTS timer (
    key bigint PRIMARY KEY,
    id text,
    element_instance_key bigint,
    process_instance_key bigint,
    element_id text,
    due_date timestamptz,
    state text,
    repeat_count bigint,
    created_at timestamptz
);

CREATE TABLE IF NOT EXISTS message_subscription (
    key bigint PRIMARY KEY,
    id text,
    element_instance_key bigint,
    process_instance_key bigint,
    element_id text,
    message_name text,
    correlation_key text,
    state text,
    created_at timestamptz
);

CREATE TABLE IF NOT EXISTS idempotency_record (
    key varchar(128) NOT NULL,
    operation varchar(64) NOT NULL,
    created_at timestamptz,
    PRIMARY KEY (key, operation)
);

CREATE TABLE IF NOT EXISTS outbox_message (
    id varchar(64) PRIMARY KEY,
    event_type varchar(128),
    payload bytea,
    status varchar(16),
    attempts bigint,
    last_error text,
    next_attempt timestamptz,
    processing_started_at timestamptz,
    created_at timestamptz,
    published_at timestamptz
);

CREATE TABLE IF NOT EXISTS runtime_scheduler_lease (
    id varchar(64) PRIMARY KEY,
    holder varchar(256) NOT NULL,
    expires_at timestamptz NOT NULL,
    updated_at timestamptz
);

CREATE TABLE IF NOT EXISTS decision_definition (
    key bigint PRIMARY KEY,
    decision_id varchar(128),
    name varchar(256),
    version bigint,
    resource bytea,
    created_at timestamptz,
    updated_at timestamptz
);

-- Expand-only repair clauses make the baseline safe for existing v1.1.0
-- installations created by an older startup AutoMigrate.
ALTER TABLE job ADD COLUMN IF NOT EXISTS assignee text;
ALTER TABLE job ADD COLUMN IF NOT EXISTS candidate_users text;
ALTER TABLE job ADD COLUMN IF NOT EXISTS candidate_groups text;
ALTER TABLE job ADD COLUMN IF NOT EXISTS due_date timestamptz;
ALTER TABLE job ADD COLUMN IF NOT EXISTS breached_at timestamptz;
ALTER TABLE outbox_message ADD COLUMN IF NOT EXISTS next_attempt timestamptz;
ALTER TABLE outbox_message ADD COLUMN IF NOT EXISTS processing_started_at timestamptz;
ALTER TABLE outbox_message ADD COLUMN IF NOT EXISTS published_at timestamptz;

CREATE INDEX IF NOT EXISTS idx_process_instance_id ON process_instance (id);
CREATE INDEX IF NOT EXISTS idx_element_instance_id ON element_instance (id);
CREATE INDEX IF NOT EXISTS idx_job_id ON job (id);
CREATE INDEX IF NOT EXISTS idx_incident_id ON incident (id);
CREATE INDEX IF NOT EXISTS idx_timer_id ON timer (id);
CREATE INDEX IF NOT EXISTS idx_message_subscription_id ON message_subscription (id);
CREATE INDEX IF NOT EXISTS idx_outbox_message_event_type ON outbox_message (event_type);
CREATE INDEX IF NOT EXISTS idx_outbox_message_status ON outbox_message (status);
CREATE INDEX IF NOT EXISTS idx_outbox_message_next_attempt ON outbox_message (next_attempt);
CREATE INDEX IF NOT EXISTS idx_outbox_message_processing_started_at ON outbox_message (processing_started_at);
CREATE INDEX IF NOT EXISTS idx_runtime_scheduler_lease_expires_at ON runtime_scheduler_lease (expires_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_decision_definition_decision_id ON decision_definition (decision_id);
