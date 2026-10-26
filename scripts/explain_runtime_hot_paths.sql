-- Run after schema migration and representative ANALYZE statistics:
--   psql "$PG_DSN" -v ON_ERROR_STOP=1 -f scripts/explain_runtime_hot_paths.sql
-- JSON plans provide machine-readable evidence for release artifacts.
SET enable_seqscan = off;

EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT key FROM timer
WHERE tenant_id = 'default' AND state = 'CREATED'
  AND due_date <= transaction_timestamp()
  AND (claim_expires_at IS NULL OR claim_expires_at <= transaction_timestamp())
ORDER BY due_date, key LIMIT 100;

EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT key FROM job
WHERE tenant_id = 'default' AND state IN ('CREATED', 'ACTIVATED')
  AND due_date <= transaction_timestamp() AND breached_at IS NULL
  AND (claim_expires_at IS NULL OR claim_expires_at <= transaction_timestamp())
ORDER BY due_date, key LIMIT 100;

EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT key FROM message_subscription
WHERE tenant_id = 'default' AND state = 'OPEN'
  AND message_name = 'evidence' AND correlation_key = 'evidence'
ORDER BY key LIMIT 100;

EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT key FROM element_instance
WHERE tenant_id = 'default' AND state IN ('ACTIVATED', 'ACTIVATING', 'COMPLETING')
  AND bpmn_element_type IN ('INTERMEDIATE_CATCH_EVENT', 'BOUNDARY_EVENT');

EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
SELECT id FROM outbox_message
WHERE status = 'PENDING'
  AND (next_attempt IS NULL OR next_attempt <= transaction_timestamp())
ORDER BY created_at LIMIT 100;
