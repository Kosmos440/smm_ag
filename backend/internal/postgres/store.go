package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smmagent/internal/model"
	"smmagent/migrations"
)

const workerLockID int32 = 0x534d4d02

var ErrWorkerLocked = fmt.Errorf("another worker holds the database lock: %w", model.ErrConflict)

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("invalid PostgreSQL connection settings")
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) Ready(ctx context.Context) error {
	return migrations.Ready(ctx, s.pool)
}

func (s *Store) Migrate(ctx context.Context) error {
	return migrations.Apply(ctx, s.pool)
}

func (s *Store) AcquireWorker(ctx context.Context) (func(), error) {
	session, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var locked bool
	err = session.QueryRow(ctx, "select pg_try_advisory_lock($1, hashtext(current_schema()::text))", workerLockID).Scan(&locked)
	if err != nil {
		closeWorkerSession(session.Hijack())
		return nil, err
	}
	if !locked {
		session.Release()
		return nil, ErrWorkerLocked
	}
	return func() {
		closeWorkerSession(session.Hijack())
	}, nil
}

func closeWorkerSession(conn *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Closing the connection releases the session lock even if an explicit unlock
	// query would fail. A session with an uncertain lock is never returned to the pool.
	_ = conn.Close(ctx)
}
