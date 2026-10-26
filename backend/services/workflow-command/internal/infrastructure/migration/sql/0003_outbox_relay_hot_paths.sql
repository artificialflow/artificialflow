-- Keep relay scans proportional to claimable rows rather than retained history.
-- Published and terminally failed rows are intentionally absent from these
-- partial indexes.
CREATE INDEX IF NOT EXISTS idx_outbox_pending_created
    ON outbox_message (created_at)
    INCLUDE (next_attempt)
    WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_outbox_processing_created
    ON outbox_message (created_at)
    INCLUDE (processing_started_at)
    WHERE status = 'PROCESSING';
