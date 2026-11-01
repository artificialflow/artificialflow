package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	ledgerTable            = "artificialflow_schema_migrations"
	advisoryLock           = int64(0x4152464C4F575341) // "ARFLOWSA"
	noTransactionDirective = "-- artificialflow:no-transaction"
	advisoryLockPoll       = 250 * time.Millisecond
)

var (
	//go:embed sql/*.sql
	migrationFiles embed.FS
	filenameRE     = regexp.MustCompile(`^([0-9]{4})_([a-z0-9_]+)\.sql$`)
)

type Migration struct {
	Version       int64
	Name          string
	SQL           string
	Checksum      string
	NoTransaction bool
}

type appliedMigration struct {
	Version  int64
	Name     string
	Checksum string
}

var requiredSchema = map[string]map[string]string{
	"process": {
		"key": "int8", "bpmn_process_id": "text", "version": "int8", "resource_name": "text",
		"deployment_key": "int8", "resource": "bytea", "resource_checksum": "text",
		"tenant_id": "text", "created_at": "timestamptz",
	},
	"process_instance": {
		"key": "int8", "id": "text", "process_definition_key": "int8", "version": "int8",
		"parent_process_instance_key": "int8", "parent_element_instance_key": "int8",
		"state": "text", "tenant_id": "text", "created_at": "timestamptz", "end_time": "timestamptz",
	},
	"element_instance": {
		"key": "int8", "id": "text", "process_instance_key": "int8",
		"process_definition_key": "int8", "element_id": "text", "bpmn_element_type": "text",
		"flow_scope_key": "int8", "state": "text", "created_at": "timestamptz",
		"end_time": "timestamptz", "tenant_id": "text",
	},
	"variable": {
		"scope_key": "int8", "process_instance_key": "int8", "name": "text", "value": "jsonb",
		"created_at": "timestamptz", "updated_at": "timestamptz", "tenant_id": "text",
	},
	"job": {
		"key": "int8", "id": "text", "type": "text", "process_instance_key": "int8",
		"element_instance_key": "int8", "process_definition_key": "int8", "element_id": "text",
		"worker": "text", "retries": "int8", "state": "text", "assignee": "text",
		"candidate_users": "text", "candidate_groups": "text", "lock_expiration_time": "timestamptz",
		"due_date": "timestamptz", "breached_at": "timestamptz", "created_at": "timestamptz",
		"updated_at": "timestamptz", "tenant_id": "text", "claim_owner": "varchar",
		"claim_expires_at": "timestamptz", "claim_generation": "int8",
	},
	"incident": {
		"key": "int8", "id": "text", "process_instance_key": "int8",
		"element_instance_key": "int8", "job_key": "int8", "error_type": "text",
		"error_message": "text", "state": "text", "created_at": "timestamptz",
		"resolved_at": "timestamptz", "tenant_id": "text",
	},
	"timer": {
		"key": "int8", "id": "text", "element_instance_key": "int8",
		"process_instance_key": "int8", "element_id": "text", "due_date": "timestamptz",
		"state": "text", "repeat_count": "int8", "created_at": "timestamptz", "tenant_id": "text",
		"claim_owner": "varchar", "claim_expires_at": "timestamptz", "claim_generation": "int8",
	},
	"message_subscription": {
		"key": "int8", "id": "text", "element_instance_key": "int8",
		"process_instance_key": "int8", "element_id": "text", "message_name": "text",
		"correlation_key": "text", "state": "text", "created_at": "timestamptz", "tenant_id": "text",
	},
	"idempotency_record": {
		"key": "varchar", "operation": "varchar", "created_at": "timestamptz", "tenant_id": "text",
	},
	"outbox_message": {
		"id": "varchar", "event_type": "varchar", "payload": "bytea", "status": "varchar",
		"attempts": "int8", "last_error": "text", "next_attempt": "timestamptz",
		"processing_started_at": "timestamptz", "created_at": "timestamptz",
		"published_at": "timestamptz",
		"tenant_id":    "text", "aggregate_type": "varchar", "aggregate_id": "varchar",
		"schema_version": "int4", "occurred_at": "timestamptz", "trace_context": "jsonb",
		"claim_owner": "varchar", "claim_expires_at": "timestamptz", "claim_generation": "int8",
	},
	"runtime_scheduler_lease": {
		"id": "varchar", "holder": "varchar", "expires_at": "timestamptz",
		"updated_at": "timestamptz",
	},
	"decision_definition": {
		"key": "int8", "decision_id": "varchar", "name": "varchar", "version": "int8",
		"resource": "bytea", "created_at": "timestamptz", "updated_at": "timestamptz",
		"tenant_id": "text",
	},
	"runtime_work_item": {
		"tenant_id": "text", "work_type": "varchar", "work_key": "varchar",
		"available_at": "timestamptz", "claim_owner": "varchar", "claimed_at": "timestamptz",
		"claim_expires_at": "timestamptz", "claim_generation": "int8",
		"payload": "jsonb", "updated_at": "timestamptz",
	},
	"process_instance_migration": {
		"tenant_id": "text", "operation_id": "varchar", "instance_key": "int8",
		"source_definition_key": "int8", "target_definition_key": "int8",
		"source_version": "int8", "target_version": "int8", "dry_run": "bool",
		"status": "varchar", "reason_code": "varchar", "mapping": "jsonb",
		"actor": "varchar", "created_at": "timestamptz", "completed_at": "timestamptz",
	},
	"process_instance_migration_audit": {
		"tenant_id": "text", "event_id": "varchar", "operation_id": "varchar",
		"instance_key": "int8", "source_definition_key": "int8", "target_definition_key": "int8",
		"mapping": "jsonb", "actor": "varchar", "occurred_at": "timestamptz",
	},
}

func Load() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "sql")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	sources := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, err := migrationFiles.ReadFile(filepath.ToSlash(filepath.Join("sql", entry.Name())))
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		sources[entry.Name()] = body
	}
	return parse(sources)
}

func parse(sources map[string][]byte) ([]Migration, error) {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)

	migrations := make([]Migration, 0, len(names))
	var previous int64
	for _, filename := range names {
		match := filenameRE.FindStringSubmatch(filename)
		if match == nil {
			return nil, fmt.Errorf("invalid migration filename %q", filename)
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse migration version %q: %w", filename, err)
		}
		if len(migrations) > 0 && version != previous+1 {
			return nil, fmt.Errorf("migrations must be contiguous: version %04d follows %04d", version, previous)
		}
		body := sources[filename]
		sum := sha256.Sum256(body)
		migrations = append(migrations, Migration{
			Version:       version,
			Name:          match[2],
			SQL:           string(body),
			Checksum:      hex.EncodeToString(sum[:]),
			NoTransaction: strings.Contains(string(body), noTransactionDirective),
		})
		previous = version
	}
	if len(migrations) == 0 {
		return nil, errors.New("no schema migrations embedded")
	}
	return migrations, nil
}

func Apply(ctx context.Context, db *sql.DB) error {
	migrations, err := Load()
	if err != nil {
		return err
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve migration connection: %w", err)
	}
	defer conn.Close()

	if err := acquireAdvisoryLock(ctx, conn); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLock)
	}()

	ledgerPresent, err := tableExists(ctx, conn, ledgerTable)
	if err != nil {
		return err
	}
	if !ledgerPresent {
		fingerprint, err := readSchemaFingerprint(ctx, conn)
		if err != nil {
			return err
		}
		if err := validateLegacySchemaFingerprint(fingerprint, true); err != nil {
			return fmt.Errorf("refusing legacy schema adoption: %w", err)
		}
	}
	if err := ensureLedger(ctx, conn); err != nil {
		return err
	}
	applied, err := readApplied(ctx, conn)
	if err != nil {
		return err
	}
	if ledgerPresent && len(applied) == 0 {
		fingerprint, err := readSchemaFingerprint(ctx, conn)
		if err != nil {
			return err
		}
		if err := validateLegacySchemaFingerprint(fingerprint, true); err != nil {
			return fmt.Errorf("refusing legacy schema adoption: %w", err)
		}
	}
	pending, err := reconcile(migrations, applied, false)
	if err != nil {
		return err
	}

	for _, item := range pending {
		if item.NoTransaction {
			if err := applyNonTransactionalMigration(ctx, conn, item); err != nil {
				return err
			}
			continue
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %04d: %w", item.Version, err)
		}
		if _, err = tx.ExecContext(ctx, item.SQL); err == nil {
			_, err = tx.ExecContext(ctx,
				`INSERT INTO `+ledgerTable+` (version, name, checksum) VALUES ($1, $2, $3)`,
				item.Version, item.Name, item.Checksum,
			)
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %04d_%s: %w", item.Version, item.Name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %04d_%s: %w", item.Version, item.Name, err)
		}
	}
	return validateDatabaseFingerprint(ctx, conn)
}

// A blocking pg_advisory_lock keeps the waiter's statement open, and
// CREATE INDEX CONCURRENTLY in the holder waits for every open transaction,
// which Postgres reports as a deadlock. Waiters therefore poll.
func acquireAdvisoryLock(ctx context.Context, conn *sql.Conn) error {
	for {
		var locked bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLock).Scan(&locked); err != nil {
			return fmt.Errorf("acquire schema migration advisory lock: %w", err)
		}
		if locked {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("acquire schema migration advisory lock: %w", ctx.Err())
		case <-time.After(advisoryLockPoll):
		}
	}
}

func applyNonTransactionalMigration(ctx context.Context, conn *sql.Conn, item Migration) error {
	for _, statement := range strings.Split(item.SQL, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply non-transactional migration %04d_%s: %w", item.Version, item.Name, err)
		}
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO `+ledgerTable+` (version, name, checksum) VALUES ($1, $2, $3)`,
		item.Version, item.Name, item.Checksum,
	); err != nil {
		return fmt.Errorf("record non-transactional migration %04d_%s: %w", item.Version, item.Name, err)
	}
	return nil
}

func Validate(ctx context.Context, db *sql.DB) error {
	migrations, err := Load()
	if err != nil {
		return err
	}
	applied, err := readApplied(ctx, db)
	if err != nil {
		if isMissingLedger(err) {
			return fmt.Errorf("schema is not initialized; command and runtime apply migrations at startup, or run workflow-migrate")
		}
		return err
	}
	_, err = reconcile(migrations, applied, true)
	if err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}
	return validateDatabaseFingerprint(ctx, db)
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type rowQueryer interface {
	queryer
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func ensureLedger(ctx context.Context, db execer) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+ledgerTable+` (
		version bigint PRIMARY KEY,
		name text NOT NULL,
		checksum char(64) NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("create schema migration ledger: %w", err)
	}
	return nil
}

func tableExists(ctx context.Context, db rowQueryer, table string) (bool, error) {
	var exists bool
	if err := db.QueryRowContext(ctx,
		`SELECT to_regclass(current_schema() || '.' || $1) IS NOT NULL`, table,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("inspect schema table %s: %w", table, err)
	}
	return exists, nil
}

func readSchemaFingerprint(ctx context.Context, db queryer) (map[string]map[string]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT table_name, column_name, udt_name
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		ORDER BY table_name, ordinal_position`)
	if err != nil {
		return nil, fmt.Errorf("read schema fingerprint: %w", err)
	}
	defer rows.Close()

	fingerprint := make(map[string]map[string]string)
	for rows.Next() {
		var table, column, dataType string
		if err := rows.Scan(&table, &column, &dataType); err != nil {
			return nil, fmt.Errorf("scan schema fingerprint: %w", err)
		}
		if fingerprint[table] == nil {
			fingerprint[table] = make(map[string]string)
		}
		fingerprint[table][column] = dataType
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema fingerprint: %w", err)
	}
	return fingerprint, nil
}

func validateDatabaseFingerprint(ctx context.Context, db queryer) error {
	fingerprint, err := readSchemaFingerprint(ctx, db)
	if err != nil {
		return err
	}
	if err := validateSchemaFingerprint(fingerprint, false); err != nil {
		return fmt.Errorf("schema fingerprint validation failed: %w", err)
	}
	return nil
}

func validateSchemaFingerprint(actual map[string]map[string]string, allowEmpty bool) error {
	return validateSchemaFingerprintAgainst(actual, allowEmpty, requiredSchema)
}

func validateLegacySchemaFingerprint(actual map[string]map[string]string, allowEmpty bool) error {
	legacy := make(map[string]map[string]string, len(requiredSchema))
	for table, columns := range requiredSchema {
		legacy[table] = make(map[string]string, len(columns))
		for column, dataType := range columns {
			legacy[table][column] = dataType
		}
	}
	for _, table := range []string{"runtime_work_item", "process_instance_migration", "process_instance_migration_audit"} {
		delete(legacy, table)
	}
	for _, table := range []string{
		"process_instance", "element_instance", "variable", "incident",
		"message_subscription", "idempotency_record", "decision_definition",
	} {
		delete(legacy[table], "tenant_id")
	}
	for _, table := range []string{"job", "timer"} {
		delete(legacy[table], "tenant_id")
		delete(legacy[table], "claim_owner")
		delete(legacy[table], "claim_expires_at")
		delete(legacy[table], "claim_generation")
	}
	for _, column := range []string{
		"tenant_id", "aggregate_type", "aggregate_id", "schema_version", "occurred_at",
		"trace_context", "claim_owner", "claim_expires_at", "claim_generation",
	} {
		delete(legacy["outbox_message"], column)
	}
	return validateSchemaFingerprintAgainst(actual, allowEmpty, legacy)
}

func validateSchemaFingerprintAgainst(actual map[string]map[string]string, allowEmpty bool, required map[string]map[string]string) error {
	recognized := 0
	for table := range required {
		if actual[table] != nil {
			recognized++
		}
	}
	if allowEmpty && recognized == 0 {
		var unknownTables []string
		for table := range actual {
			if table != ledgerTable {
				unknownTables = append(unknownTables, table)
			}
		}
		sort.Strings(unknownTables)
		if len(unknownTables) == 0 {
			return nil
		}
		return fmt.Errorf("unknown existing schema tables: %s", strings.Join(unknownTables, ", "))
	}

	var mismatches []string
	for table, requiredColumns := range required {
		actualColumns := actual[table]
		if actualColumns == nil {
			mismatches = append(mismatches, "missing table "+table)
			continue
		}
		for column, requiredType := range requiredColumns {
			actualType, ok := actualColumns[column]
			if !ok {
				mismatches = append(mismatches, fmt.Sprintf("missing column %s.%s", table, column))
				continue
			}
			if actualType != requiredType {
				mismatches = append(mismatches,
					fmt.Sprintf("column %s.%s has type %s, want %s", table, column, actualType, requiredType))
			}
		}
	}
	sort.Strings(mismatches)
	if len(mismatches) > 0 {
		return fmt.Errorf("%s", strings.Join(mismatches, "; "))
	}
	return nil
}

func readApplied(ctx context.Context, db queryer) ([]appliedMigration, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT version, name, checksum FROM `+ledgerTable+` ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read schema migration ledger: %w", err)
	}
	defer rows.Close()

	var applied []appliedMigration
	for rows.Next() {
		var item appliedMigration
		if err := rows.Scan(&item.Version, &item.Name, &item.Checksum); err != nil {
			return nil, fmt.Errorf("scan schema migration ledger: %w", err)
		}
		item.Checksum = strings.TrimSpace(item.Checksum)
		applied = append(applied, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema migration ledger: %w", err)
	}
	return applied, nil
}

func reconcile(expected []Migration, applied []appliedMigration, requireCurrent bool) ([]Migration, error) {
	known := make(map[int64]Migration, len(expected))
	for index, item := range expected {
		if index > 0 && item.Version != expected[index-1].Version+1 {
			return nil, fmt.Errorf("expected migrations are out of order at version %04d", item.Version)
		}
		known[item.Version] = item
	}

	seen := make(map[int64]bool, len(applied))
	for index, item := range applied {
		if index > 0 && item.Version <= applied[index-1].Version {
			return nil, fmt.Errorf("applied migrations are out of order at version %04d", item.Version)
		}
		migration, ok := known[item.Version]
		if !ok {
			if len(expected) > 0 && item.Version > expected[len(expected)-1].Version {
				continue // A newer expand-compatible schema may be used by an older binary.
			}
			return nil, fmt.Errorf("unknown applied migration version %04d", item.Version)
		}
		if item.Name != migration.Name {
			return nil, fmt.Errorf("migration %04d name drift: database=%q binary=%q", item.Version, item.Name, migration.Name)
		}
		if item.Checksum != migration.Checksum {
			return nil, fmt.Errorf("migration %04d checksum drift", item.Version)
		}
		seen[item.Version] = true
	}

	var pending []Migration
	for _, item := range expected {
		if !seen[item.Version] {
			pending = append(pending, item)
		}
	}
	if requireCurrent && len(pending) > 0 {
		return nil, fmt.Errorf("required migration %04d_%s is not applied", pending[0].Version, pending[0].Name)
	}
	return pending, nil
}

func isMissingLedger(err error) bool {
	return strings.Contains(err.Error(), `relation "`+ledgerTable+`" does not exist`) ||
		strings.Contains(err.Error(), "SQLSTATE 42P01")
}
