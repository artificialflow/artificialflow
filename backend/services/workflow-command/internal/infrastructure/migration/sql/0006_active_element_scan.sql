-- artificialflow:no-transaction
-- Conditional, signal, and escalation delivery list active elements across
-- all instances of a tenant. Completed history is almost all of the table, so
-- without a partial index each scheduler tick scans the whole table.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_element_active_scan
    ON element_instance (tenant_id, bpmn_element_type)
    WHERE state IN ('ACTIVATED', 'ACTIVATING', 'COMPLETING');
