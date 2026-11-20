package platform

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ConnectDB membuka pool Postgres dan menunggu sampai database siap (maks 60 detik).
func ConnectDB(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 8
	var pool *pgxpool.Pool
	for i := 0; i < 60; i++ {
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return pool, nil
			}
			pool.Close()
		}
		slog.Warn("menunggu postgres", "err", err)
		time.Sleep(time.Second)
	}
	return nil, fmt.Errorf("postgres tidak siap: %w", err)
}

// Migrate menjalankan DDL di bawah advisory lock, sehingga dua replica yang start
// bersamaan tidak saling bertabrakan saat CREATE TABLE IF NOT EXISTS.
func Migrate(ctx context.Context, pool *pgxpool.Pool, stmts ...string) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(5005)"); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock(5005)") //nolint:errcheck
	for _, s := range append([]string{outboxDDL, processedDDL}, stmts...) {
		if _, err := conn.Exec(ctx, s); err != nil {
			return fmt.Errorf("migrasi gagal: %w\n%s", err, s)
		}
	}
	return nil
}

// InTx menjalankan fn di dalam satu transaksi.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
