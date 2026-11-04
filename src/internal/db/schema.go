package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS visits (
	id int PRIMARY KEY,
	count bigint NOT NULL
);

CREATE TABLE IF NOT EXISTS applicants (
	id bigserial PRIMARY KEY,
	name text NOT NULL,
	email text NOT NULL UNIQUE,
	phone text NOT NULL DEFAULT '',
	skills text[] NOT NULL DEFAULT '{}',
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS positions (
	id bigserial PRIMARY KEY,
	title text NOT NULL,
	department text NOT NULL,
	location text NOT NULL DEFAULT '',
	status text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS applications (
	id bigserial PRIMARY KEY,
	applicant_id bigint NOT NULL REFERENCES applicants(id) ON DELETE CASCADE,
	position_id bigint NOT NULL REFERENCES positions(id) ON DELETE CASCADE,
	status text NOT NULL DEFAULT 'applied',
	screening_status text NOT NULL DEFAULT 'none',
	screening_score int,
	screening_requested_at timestamptz,
	screening_done_at timestamptz,
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now(),
	UNIQUE (applicant_id, position_id)
);

CREATE INDEX IF NOT EXISTS applications_status_idx ON applications (status);
CREATE INDEX IF NOT EXISTS applications_position_idx ON applications (position_id);
CREATE INDEX IF NOT EXISTS positions_status_idx ON positions (status);
-- Serves the KEDA query that counts recent queued screenings. It only holds the few queued rows, so it stays tiny
-- however many applications exist.
CREATE INDEX IF NOT EXISTS applications_queued_idx ON applications (screening_requested_at) WHERE screening_status = 'queued';
`

// migrations change the schema after the first release. Each entry runs once, in order, and is recorded in
// schema_migrations. Never edit or reorder an entry that has shipped: add a new one at the end.
var migrations = []string{
	// 1: why a screening ended as 'failed' (screening_status gets that value; no constraint to change).
	`ALTER TABLE applications ADD COLUMN IF NOT EXISTS screening_error text`,
}

// schemaLockID is an arbitrary constant: pods that start together run the DDL one after another
// instead of colliding on "CREATE TABLE IF NOT EXISTS" (which is not safe to run concurrently).
const schemaLockID = 7001

func ensureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin schema tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, schemaLockID); err != nil {
		return fmt.Errorf("schema lock: %w", err)
	}
	if _, err := tx.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	for i, stmt := range migrations {
		version := i + 1
		tag, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`, version)
		if err != nil {
			return fmt.Errorf("record migration %d: %w", version, err)
		}
		if tag.RowsAffected() == 0 {
			continue // already applied
		}
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("migration %d: %w", version, err)
		}
	}
	return tx.Commit(ctx)
}
