package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store menyimpan data bersama semua node di satu PostgreSQL.
// Tabel kunjungan mencatat siapa (node mana) yang memproses setiap request tulis.
type Store struct{ db *pgxpool.Pool }

const skema = `CREATE TABLE IF NOT EXISTS kunjungan (
	id          BIGSERIAL PRIMARY KEY,
	node        TEXT NOT NULL,
	request_id  TEXT NOT NULL DEFAULT '',
	waktu_node  TIMESTAMPTZ NOT NULL,           -- jam node (bisa bergeser)
	waktu_db    TIMESTAMPTZ NOT NULL DEFAULT now() -- jam database
)`

// Buka menghubungkan ke database dengan retry sampai ctx habis, lalu membuat tabel.
func Buka(ctx context.Context, url string) (*Store, error) {
	var lastErr error
	for {
		pool, err := pgxpool.New(ctx, url)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				if _, err = pool.Exec(ctx, skema); err == nil {
					return &Store{db: pool}, nil
				}
			}
			pool.Close()
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, lastErr
		case <-time.After(time.Second):
		}
	}
}

// Catat menulis satu baris dan mengembalikan id baru.
func (s *Store) Catat(ctx context.Context, node, reqID string, waktuNode time.Time) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx,
		`INSERT INTO kunjungan(node, request_id, waktu_node) VALUES ($1,$2,$3) RETURNING id`,
		node, reqID, waktuNode).Scan(&id)
	return id, err
}

// Total menghitung jumlah baris (data bersama yang sama bagi semua node).
func (s *Store) Total(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM kunjungan`).Scan(&n)
	return n, err
}

// PerNode menghitung baris yang ditulis oleh tiap node.
func (s *Store) PerNode(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.Query(ctx, `SELECT node, count(*) FROM kunjungan GROUP BY node`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hasil := map[string]int64{}
	for rows.Next() {
		var n string
		var c int64
		if err := rows.Scan(&n, &c); err != nil {
			return nil, err
		}
		hasil[n] = c
	}
	return hasil, rows.Err()
}

// UrutanTerbalik menghitung pasangan baris berurutan (menurut id) yang jam node-nya mundur.
// Dengan clock skew, "yang ditulis belakangan" bisa tampak terjadi lebih dulu.
func (s *Store) UrutanTerbalik(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM (
		SELECT waktu_node < lag(waktu_node) OVER (ORDER BY id) AS mundur FROM kunjungan
	) t WHERE mundur`).Scan(&n)
	return n, err
}

// Kosongkan menghapus semua data demo.
func (s *Store) Kosongkan(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `TRUNCATE kunjungan RESTART IDENTITY`)
	return err
}
