package migrations

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationLockID int32 = 0x534d4d01

//go:embed *.sql
var files embed.FS

// Apply runs pending migration files in filename order and records them atomically.
func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanupCtx)
	}()
	if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1, hashtext(current_schema()::text))", migrationLockID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `create table if not exists schema_migrations (
		name text primary key,
		applied_at timestamptz not null default now()
	)`); err != nil {
		return err
	}
	for _, name := range names {
		var applied bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from schema_migrations where name = $1)", name).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		script, err := files.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(script), pgx.QueryExecModeSimpleProtocol); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, "insert into schema_migrations (name) values ($1)", name); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Ready checks connectivity as well as the schema needed by this binary.
func Ready(ctx context.Context, pool *pgxpool.Pool) error {
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return err
	}
	var applied int
	err = pool.QueryRow(ctx, "select count(*) from schema_migrations where name = any($1::text[])", names).Scan(&applied)
	if err != nil {
		return err
	}
	if applied != len(names) {
		return errors.New("pending PostgreSQL migrations")
	}
	return nil
}
