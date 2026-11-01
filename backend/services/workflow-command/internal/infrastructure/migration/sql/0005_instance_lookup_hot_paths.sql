-- artificialflow:no-transaction
-- Runtime completion, cancellation, and migration repeatedly load child rows
-- by tenant and process instance. Without these access paths PostgreSQL falls
-- back to parallel table scans as history grows.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_variable_instance_hot
    ON variable (tenant_id, process_instance_key);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_job_instance_hot
    ON job (tenant_id, process_instance_key, state);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_incident_instance_hot
    ON incident (tenant_id, process_instance_key, state);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_timer_instance_hot
    ON timer (tenant_id, process_instance_key, state);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_message_subscription_instance_hot
    ON message_subscription (tenant_id, process_instance_key, state);
