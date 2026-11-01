-- artificialflow:no-transaction
-- Split claimable outbox classes into disjoint partial indexes so PostgreSQL
-- does not scan retained published history or evaluate one large OR predicate.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_outbox_pending_immediate
    ON outbox_message (created_at)
    INCLUDE (id, tenant_id)
    WHERE status = 'PENDING' AND next_attempt IS NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_outbox_pending_retry_due
    ON outbox_message (next_attempt, created_at)
    INCLUDE (id, tenant_id)
    WHERE status = 'PENDING' AND next_attempt IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_outbox_processing_expired
    ON outbox_message (claim_expires_at, created_at)
    INCLUDE (id, tenant_id)
    WHERE status = 'PROCESSING' AND claim_expires_at IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_outbox_processing_legacy
    ON outbox_message (processing_started_at, created_at)
    INCLUDE (id, tenant_id)
    WHERE status = 'PROCESSING' AND claim_expires_at IS NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_outbox_published_cleanup
    ON outbox_message (published_at)
    INCLUDE (id, tenant_id)
    WHERE status = 'PUBLISHED';

-- The broad composite index is superseded only after all targeted indexes exist.
-- Older single-purpose indexes remain available to older binaries.
DROP INDEX CONCURRENTLY IF EXISTS idx_outbox_claim_hot;
