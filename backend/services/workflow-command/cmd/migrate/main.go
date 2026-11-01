package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/artificialflow/artificialflow/backend/services/workflow-command/internal/infrastructure/migration"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "schema migration failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		return fmt.Errorf("PG_DSN is required")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	if len(os.Args) > 1 {
		if len(os.Args) != 2 || os.Args[1] != "validate" {
			return fmt.Errorf("usage: workflow-migrate [validate]")
		}
		if err := migration.Validate(ctx, db); err != nil {
			return err
		}
		fmt.Println("schema validation passed")
		return nil
	}
	if err := migration.Apply(ctx, db); err != nil {
		return err
	}
	fmt.Println("schema migrations are current")
	return nil
}
