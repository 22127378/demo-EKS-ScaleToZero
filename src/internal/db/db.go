// Package db holds what the API and the worker share: the connection pool to Aurora PostgreSQL,
// the schema, and the screening update.
package db

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB connects to Postgres lazily on first use: a pod must start and answer health checks even
// when the database is unreachable at that moment.
type DB struct {
	mu   sync.Mutex
	pool *pgxpool.Pool
}

// New returns nil when no database is configured (DB_HOST unset).
func New() *DB {
	if os.Getenv("DB_HOST") == "" {
		return nil
	}
	return &DB{}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// connect builds the pool and makes sure the schema exists. Caller holds d.mu.
func (d *DB) connect(ctx context.Context) error {
	creds := newCredProvider(credentials, credTTL)
	user, pass, err := creds.get(ctx)
	if err != nil {
		return err
	}
	dsn := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(user, pass),
		Host:   os.Getenv("DB_HOST") + ":" + firstNonEmpty(os.Getenv("DB_PORT"), "5432"),
		Path:   "/" + firstNonEmpty(os.Getenv("DB_NAME"), "postgres"),
		// "require" encrypts without verifying the certificate: enough for the demo. Use
		// "disable" for a local Postgres without TLS.
		RawQuery: "sslmode=" + firstNonEmpty(os.Getenv("DB_SSLMODE"), "require") + "&connect_timeout=5",
	}
	cfg, err := pgxpool.ParseConfig(dsn.String())
	if err != nil {
		return fmt.Errorf("parse db config: %w", err)
	}
	// Aurora stays on (no auto-pause), so a couple of warm connections are kept per pod.
	// Every new connection asks for the password again, so a password rotated by RDS is picked up
	// without restarting the pod.
	cfg.BeforeConnect = func(ctx context.Context, cc *pgx.ConnConfig) error {
		u, p, err := creds.get(ctx)
		if err != nil {
			return err
		}
		cc.User, cc.Password = u, p
		return nil
	}
	cfg.MaxConns = int32(envInt("DB_MAX_CONNS", 10))
	cfg.MinConns = int32(envInt("DB_MIN_CONNS", 1))
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.MaxConnLifetime = 30 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("create pool: %w", err)
	}
	if err := ensureSchema(ctx, pool); err != nil {
		pool.Close()
		return err
	}
	d.pool = pool
	slog.Info("database connected", "host", os.Getenv("DB_HOST"))
	return nil
}

// Pool returns the connection pool, connecting on first use.
func (d *DB) Pool(ctx context.Context) (*pgxpool.Pool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pool == nil {
		if err := d.connect(ctx); err != nil {
			return nil, err
		}
	}
	return d.pool, nil
}

// Visit increments and returns the visit counter in one statement, so concurrent pods never lose
// an update.
func (d *DB) Visit(ctx context.Context) (int64, error) {
	pool, err := d.Pool(ctx)
	if err != nil {
		return 0, err
	}
	var n int64
	err = pool.QueryRow(ctx,
		`INSERT INTO visits (id, count) VALUES (1, 1)
		 ON CONFLICT (id) DO UPDATE SET count = visits.count + 1 RETURNING count`).Scan(&n)
	return n, err
}
