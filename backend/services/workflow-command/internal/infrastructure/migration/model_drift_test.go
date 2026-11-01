package migration

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artificialflow/artificialflow/backend/libs/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// The GORM models are the source of truth for the schema. Every table they map
// must be created by the embedded migrations with exactly the model's columns.
var schemaModels = []any{
	&model.Process{}, &model.ProcessInstance{}, &model.ElementInstance{},
	&model.Variable{}, &model.Job{}, &model.Incident{}, &model.Timer{},
	&model.MessageSubscription{}, &model.IdempotencyRecord{}, &model.OutboxMessage{},
	&model.RuntimeSchedulerLease{}, &model.DecisionDefinition{}, &model.RuntimeWorkItem{},
	&model.ProcessInstanceMigration{},
}

// Written with raw SQL only, so no GORM model maps them.
var tablesWithoutModel = map[string]bool{"process_instance_migration_audit": true}

type modelTable struct {
	name    string
	columns map[string]*schema.Field
}

func parseModels(t *testing.T) []modelTable {
	t.Helper()
	cache := &sync.Map{}
	tables := make([]modelTable, 0, len(schemaModels))
	for _, m := range schemaModels {
		s, err := schema.Parse(m, cache, schema.NamingStrategy{})
		if err != nil {
			t.Fatalf("parse model %T: %v", m, err)
		}
		columns := make(map[string]*schema.Field, len(s.DBNames))
		for _, name := range s.DBNames {
			columns[name] = s.FieldsByDBName[name]
		}
		tables = append(tables, modelTable{name: s.Table, columns: columns})
	}
	return tables
}

// compatibleTypes lists the Postgres udt names a model field may map to.
func compatibleTypes(field *schema.Field) []string {
	if explicit, ok := field.TagSettings["TYPE"]; ok {
		return []string{strings.ToLower(explicit)}
	}
	switch field.DataType {
	case schema.Bool:
		return []string{"bool"}
	case schema.Int, schema.Uint:
		return []string{"int2", "int4", "int8"}
	case schema.Float:
		return []string{"float4", "float8", "numeric"}
	case schema.String:
		return []string{"text", "varchar"}
	case schema.Time:
		return []string{"timestamptz", "timestamp"}
	case schema.Bytes:
		return []string{"bytea"}
	default:
		return nil
	}
}

func diffModelColumns(table modelTable, actual map[string]string) []string {
	var problems []string
	for column, field := range table.columns {
		actualType, ok := actual[column]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s.%s is in the model but not the schema", table.name, column))
			continue
		}
		allowed := compatibleTypes(field)
		if !containsString(allowed, actualType) {
			problems = append(problems, fmt.Sprintf("%s.%s has type %s, model allows %v", table.name, column, actualType, allowed))
		}
	}
	for column := range actual {
		if _, ok := table.columns[column]; !ok {
			problems = append(problems, fmt.Sprintf("%s.%s is in the schema but not the model", table.name, column))
		}
	}
	return problems
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestRequiredSchemaMatchesModels(t *testing.T) {
	var problems []string
	modeled := map[string]bool{}
	for _, table := range parseModels(t) {
		modeled[table.name] = true
		required, ok := requiredSchema[table.name]
		if !ok {
			problems = append(problems, "model table "+table.name+" is missing from requiredSchema")
			continue
		}
		problems = append(problems, diffModelColumns(table, required)...)
	}
	for table := range requiredSchema {
		if !modeled[table] && !tablesWithoutModel[table] {
			problems = append(problems, "requiredSchema table "+table+" has no model; add it to schemaModels or tablesWithoutModel")
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Fatalf("models and requiredSchema drifted:\n%s", strings.Join(problems, "\n"))
	}
}

func TestDiffModelColumnsReportsEachKindOfDrift(t *testing.T) {
	var lease modelTable
	for _, table := range parseModels(t) {
		if table.name == "runtime_scheduler_lease" {
			lease = table
		}
	}
	got := diffModelColumns(lease, map[string]string{
		"id": "varchar", "holder": "int8", "updated_at": "timestamptz", "extra": "text",
	})
	sort.Strings(got)
	want := []string{
		"runtime_scheduler_lease.expires_at is in the model but not the schema",
		"runtime_scheduler_lease.extra is in the schema but not the model",
		"runtime_scheduler_lease.holder has type int8, model allows [text varchar]",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("drift report:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestMigratedSchemaMatchesModels(t *testing.T) {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN is required to apply migrations to a scratch schema")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	scratch := fmt.Sprintf("drift_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE SCHEMA " + scratch).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Exec("DROP SCHEMA " + scratch + " CASCADE").Error })

	separator := " "
	if strings.Contains(dsn, "://") {
		separator = "&"
		if !strings.Contains(dsn, "?") {
			separator = "?"
		}
	}
	scoped, err := gorm.Open(postgres.Open(dsn+separator+"search_path="+scratch), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := scoped.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	if err := Apply(ctx, sqlDB); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	actual, err := readSchemaFingerprint(ctx, sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	for _, table := range parseModels(t) {
		columns, ok := actual[table.name]
		if !ok {
			problems = append(problems, "migrations do not create model table "+table.name)
			continue
		}
		problems = append(problems, diffModelColumns(table, columns)...)
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Fatalf("migrated schema and models drifted:\n%s", strings.Join(problems, "\n"))
	}
}
