package migration

import (
	"strings"
	"testing"
)

func TestParseOrdersMigrationsAndComputesChecksums(t *testing.T) {
	migrations, err := parse(map[string][]byte{
		"0002_expand_jobs.sql": []byte("ALTER TABLE job ADD COLUMN result jsonb;"),
		"0001_baseline.sql":    []byte("CREATE TABLE job (key bigint PRIMARY KEY);"),
	})
	if err != nil {
		t.Fatalf("parse migrations: %v", err)
	}
	if migrations[0].Version != 1 || migrations[1].Version != 2 {
		t.Fatalf("migrations not ordered: %#v", migrations)
	}
	if len(migrations[0].Checksum) != 64 {
		t.Fatalf("checksum length = %d, want 64", len(migrations[0].Checksum))
	}
}

func TestParseRejectsOrderingGap(t *testing.T) {
	_, err := parse(map[string][]byte{
		"0001_baseline.sql": []byte("SELECT 1;"),
		"0003_expand.sql":   []byte("SELECT 3;"),
	})
	if err == nil || !strings.Contains(err.Error(), "contiguous") {
		t.Fatalf("expected contiguous ordering error, got %v", err)
	}
}

func TestParseMarksNonTransactionalMigration(t *testing.T) {
	migrations, err := parse(map[string][]byte{
		"0001_concurrent_index.sql": []byte("-- artificialflow:no-transaction\nCREATE INDEX CONCURRENTLY idx_job_state ON job (state);"),
	})
	if err != nil {
		t.Fatalf("parse migration: %v", err)
	}
	if !migrations[0].NoTransaction {
		t.Fatal("non-transactional migration directive was ignored")
	}
}

func TestBaselineKeepsDebeziumTableNames(t *testing.T) {
	migrations, err := Load()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	baseline := migrations[0].SQL
	for _, table := range []string{
		"process",
		"process_instance",
		"element_instance",
		"variable",
		"job",
		"incident",
		"timer",
		"message_subscription",
	} {
		if !strings.Contains(baseline, "CREATE TABLE IF NOT EXISTS "+table+" (") {
			t.Errorf("baseline is missing Debezium table %q", table)
		}
	}
}

func TestOutboxScalabilityMigrationHasTargetedPartialIndexes(t *testing.T) {
	migrations, err := Load()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if len(migrations) < 4 || migrations[3].Version != 4 {
		t.Fatalf("migration 0004 not loaded: %#v", migrations)
	}
	if !migrations[3].NoTransaction {
		t.Fatal("migration 0004 must run outside a transaction for concurrent index creation")
	}
	sql := migrations[3].SQL
	for _, fragment := range []string{
		"idx_outbox_pending_immediate",
		"status = 'PENDING' AND next_attempt IS NULL",
		"idx_outbox_pending_retry_due",
		"status = 'PENDING' AND next_attempt IS NOT NULL",
		"idx_outbox_processing_expired",
		"status = 'PROCESSING' AND claim_expires_at IS NOT NULL",
		"idx_outbox_processing_legacy",
		"status = 'PROCESSING' AND claim_expires_at IS NULL",
		"idx_outbox_published_cleanup",
		"status = 'PUBLISHED'",
		"DROP INDEX CONCURRENTLY IF EXISTS idx_outbox_claim_hot",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("migration 0004 missing %q", fragment)
		}
	}
}

func TestInstanceLookupMigrationHasTenantScopedIndexes(t *testing.T) {
	migrations, err := Load()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if len(migrations) < 5 || migrations[4].Version != 5 {
		t.Fatalf("migration 0005 not loaded: %#v", migrations)
	}
	if !migrations[4].NoTransaction {
		t.Fatal("migration 0005 must run outside a transaction for concurrent index creation")
	}
	sql := migrations[4].SQL
	for _, fragment := range []string{
		"idx_variable_instance_hot",
		"variable (tenant_id, process_instance_key)",
		"idx_job_instance_hot",
		"job (tenant_id, process_instance_key, state)",
		"idx_incident_instance_hot",
		"idx_timer_instance_hot",
		"idx_message_subscription_instance_hot",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("migration 0005 missing %q", fragment)
		}
	}
}

func TestActiveElementScanMigrationMatchesRepositoryStates(t *testing.T) {
	migrations, err := Load()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if len(migrations) < 6 || migrations[5].Version != 6 {
		t.Fatalf("migration 0006 not loaded: %#v", migrations)
	}
	if !migrations[5].NoTransaction {
		t.Fatal("migration 0006 must run outside a transaction for concurrent index creation")
	}
	// The planner only uses the partial index when the query's state list
	// matches this predicate, so it must track listActiveElementInstances.
	for _, fragment := range []string{
		"idx_element_active_scan",
		"element_instance (tenant_id, bpmn_element_type)",
		"WHERE state IN ('ACTIVATED', 'ACTIVATING', 'COMPLETING')",
	} {
		if !strings.Contains(migrations[5].SQL, fragment) {
			t.Errorf("migration 0006 missing %q", fragment)
		}
	}
}

func TestReconcileRejectsChecksumDrift(t *testing.T) {
	expected := []Migration{{Version: 1, Name: "baseline", Checksum: "expected"}}
	applied := []appliedMigration{{Version: 1, Name: "baseline", Checksum: "changed"}}

	_, err := reconcile(expected, applied, true)
	if err == nil || !strings.Contains(err.Error(), "checksum drift") {
		t.Fatalf("expected checksum drift, got %v", err)
	}
}

func TestReconcileSkipsAlreadyAppliedMigrations(t *testing.T) {
	expected := []Migration{
		{Version: 1, Name: "baseline", Checksum: "one"},
		{Version: 2, Name: "expand", Checksum: "two"},
	}
	applied := []appliedMigration{{Version: 1, Name: "baseline", Checksum: "one"}}

	pending, err := reconcile(expected, applied, false)
	if err != nil {
		t.Fatalf("reconcile migrations: %v", err)
	}
	if len(pending) != 1 || pending[0].Version != 2 {
		t.Fatalf("pending = %#v, want only version 2", pending)
	}
}

func TestSchemaValidationRequiresCurrentVersion(t *testing.T) {
	expected := []Migration{
		{Version: 1, Name: "baseline", Checksum: "one"},
		{Version: 2, Name: "expand", Checksum: "two"},
	}
	applied := []appliedMigration{{Version: 1, Name: "baseline", Checksum: "one"}}

	_, err := reconcile(expected, applied, true)
	if err == nil || !strings.Contains(err.Error(), "required migration 0002_expand") {
		t.Fatalf("expected required migration validation error, got %v", err)
	}
}

func TestSchemaValidationAllowsNewerExpandCompatibleVersion(t *testing.T) {
	expected := []Migration{{Version: 1, Name: "baseline", Checksum: "one"}}
	applied := []appliedMigration{
		{Version: 1, Name: "baseline", Checksum: "one"},
		{Version: 2, Name: "future_expand", Checksum: "future"},
	}

	if _, err := reconcile(expected, applied, true); err != nil {
		t.Fatalf("newer expand-compatible schema rejected: %v", err)
	}
}

func TestSchemaFingerprintAllowsTrulyEmptyDatabase(t *testing.T) {
	if err := validateSchemaFingerprint(map[string]map[string]string{}, true); err != nil {
		t.Fatalf("empty database rejected: %v", err)
	}
}

func TestSchemaFingerprintRejectsUnknownLegacySchema(t *testing.T) {
	actual := map[string]map[string]string{
		"workflow_instances": {"id": "int8"},
	}

	err := validateSchemaFingerprint(actual, true)
	if err == nil || !strings.Contains(err.Error(), "unknown existing schema tables: workflow_instances") {
		t.Fatalf("expected unknown legacy schema rejection, got %v", err)
	}
}

func TestSchemaFingerprintRejectsPartialLegacySchema(t *testing.T) {
	actual := map[string]map[string]string{
		"process": {"key": "int8", "bpmn_process_id": "text"},
	}

	err := validateSchemaFingerprint(actual, true)
	if err == nil || !strings.Contains(err.Error(), "missing table process_instance") {
		t.Fatalf("expected partial legacy schema rejection, got %v", err)
	}
}

func TestSchemaFingerprintRejectsRequiredColumnTypeDrift(t *testing.T) {
	actual := cloneRequiredSchema()
	actual["job"]["key"] = "text"

	err := validateSchemaFingerprint(actual, false)
	if err == nil || !strings.Contains(err.Error(), "column job.key has type text, want int8") {
		t.Fatalf("expected type drift rejection, got %v", err)
	}
}

func TestSchemaFingerprintAllowsAdditiveNewerSchema(t *testing.T) {
	actual := cloneRequiredSchema()
	actual["job"]["future_result"] = "jsonb"
	actual["future_expand_table"] = map[string]string{"id": "int8"}

	if err := validateSchemaFingerprint(actual, false); err != nil {
		t.Fatalf("additive newer schema rejected: %v", err)
	}
}

func cloneRequiredSchema() map[string]map[string]string {
	clone := make(map[string]map[string]string, len(requiredSchema))
	for table, columns := range requiredSchema {
		clone[table] = make(map[string]string, len(columns))
		for column, dataType := range columns {
			clone[table][column] = dataType
		}
	}
	return clone
}
