package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"demo/internal/db"
)

// pgStore implements store on top of the shared connection pool.
type pgStore struct{ d *db.DB }

func (s pgStore) pool(ctx context.Context) (*pgxpool.Pool, error) { return s.d.Pool(ctx) }

// mapErr turns driver errors into the domain errors the handlers understand.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505": // unique_violation
			return errConflict
		case "23503": // foreign_key_violation
			return errInvalidRef
		}
	}
	return err
}

// likePattern builds an ILIKE pattern that matches q anywhere, with LIKE wildcards escaped.
// An empty q yields an empty pattern, which the queries treat as "no filter".
func likePattern(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
}

const (
	applicantCols   = `id, name, email, phone, skills, created_at, updated_at`
	positionCols    = `id, title, department, location, status, created_at, updated_at`
	applicationCols = `id, applicant_id, position_id, status, screening_status, screening_score,
		screening_requested_at, screening_done_at, created_at, updated_at`
)

func scanApplicant(row pgx.Row) (applicant, error) {
	var a applicant
	err := row.Scan(&a.ID, &a.Name, &a.Email, &a.Phone, &a.Skills, &a.CreatedAt, &a.UpdatedAt)
	if a.Skills == nil {
		a.Skills = []string{}
	}
	return a, mapErr(err)
}

func scanPosition(row pgx.Row) (position, error) {
	var p position
	err := row.Scan(&p.ID, &p.Title, &p.Department, &p.Location, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	return p, mapErr(err)
}

func scanApplication(row pgx.Row) (application, error) {
	var a application
	err := row.Scan(&a.ID, &a.ApplicantID, &a.PositionID, &a.Status, &a.ScreeningStatus, &a.ScreeningScore,
		&a.ScreeningRequestedAt, &a.ScreeningDoneAt, &a.CreatedAt, &a.UpdatedAt)
	return a, mapErr(err)
}

// collect runs a query and scans every row with scan.
func collect[T any](ctx context.Context, p *pgxpool.Pool, scan func(pgx.Row) (T, error), sql string, args ...any) ([]T, error) {
	rows, err := p.Query(ctx, sql, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, mapErr(rows.Err())
}

func deleteByID(ctx context.Context, p *pgxpool.Pool, table string, id int64) error {
	tag, err := p.Exec(ctx, `DELETE FROM `+table+` WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

// ---- applicants --------------------------------------------------------------------------

func (s pgStore) CreateApplicant(ctx context.Context, in applicantInput) (applicant, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return applicant{}, err
	}
	return scanApplicant(p.QueryRow(ctx,
		`INSERT INTO applicants (name, email, phone, skills) VALUES ($1, $2, $3, $4) RETURNING `+applicantCols,
		in.Name, in.Email, in.Phone, in.Skills))
}

func (s pgStore) GetApplicant(ctx context.Context, id int64) (applicant, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return applicant{}, err
	}
	return scanApplicant(p.QueryRow(ctx, `SELECT `+applicantCols+` FROM applicants WHERE id = $1`, id))
}

const applicantWhere = `($1 = '' OR name ILIKE $1 OR email ILIKE $1 OR EXISTS (SELECT 1 FROM unnest(skills) k WHERE k ILIKE $1))`

func (s pgStore) ListApplicants(ctx context.Context, f applicantFilter) ([]applicant, int, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return nil, 0, err
	}
	pat := likePattern(f.Q)
	var total int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM applicants WHERE `+applicantWhere, pat).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}
	items, err := collect(ctx, p, scanApplicant,
		`SELECT `+applicantCols+` FROM applicants WHERE `+applicantWhere+` ORDER BY id LIMIT $2 OFFSET $3`, pat, f.Limit, f.Offset)
	return items, total, err
}

func (s pgStore) UpdateApplicant(ctx context.Context, id int64, in applicantInput) (applicant, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return applicant{}, err
	}
	return scanApplicant(p.QueryRow(ctx,
		`UPDATE applicants SET name = $2, email = $3, phone = $4, skills = $5, updated_at = now()
		 WHERE id = $1 RETURNING `+applicantCols, id, in.Name, in.Email, in.Phone, in.Skills))
}

func (s pgStore) DeleteApplicant(ctx context.Context, id int64) error {
	p, err := s.pool(ctx)
	if err != nil {
		return err
	}
	return deleteByID(ctx, p, "applicants", id)
}

// ---- positions ---------------------------------------------------------------------------

func (s pgStore) CreatePosition(ctx context.Context, in positionInput) (position, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return position{}, err
	}
	return scanPosition(p.QueryRow(ctx,
		`INSERT INTO positions (title, department, location, status) VALUES ($1, $2, $3, $4) RETURNING `+positionCols,
		in.Title, in.Department, in.Location, in.Status))
}

func (s pgStore) GetPosition(ctx context.Context, id int64) (position, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return position{}, err
	}
	return scanPosition(p.QueryRow(ctx, `SELECT `+positionCols+` FROM positions WHERE id = $1`, id))
}

const positionWhere = `($1 = '' OR department = $1) AND ($2 = '' OR status = $2)`

func (s pgStore) ListPositions(ctx context.Context, f positionFilter) ([]position, int, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM positions WHERE `+positionWhere, f.Department, f.Status).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}
	items, err := collect(ctx, p, scanPosition,
		`SELECT `+positionCols+` FROM positions WHERE `+positionWhere+` ORDER BY id LIMIT $3 OFFSET $4`,
		f.Department, f.Status, f.Limit, f.Offset)
	return items, total, err
}

func (s pgStore) UpdatePosition(ctx context.Context, id int64, in positionInput) (position, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return position{}, err
	}
	return scanPosition(p.QueryRow(ctx,
		`UPDATE positions SET title = $2, department = $3, location = $4, status = $5, updated_at = now()
		 WHERE id = $1 RETURNING `+positionCols, id, in.Title, in.Department, in.Location, in.Status))
}

func (s pgStore) DeletePosition(ctx context.Context, id int64) error {
	p, err := s.pool(ctx)
	if err != nil {
		return err
	}
	return deleteByID(ctx, p, "positions", id)
}

func (s pgStore) ClosePosition(ctx context.Context, id int64) (position, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return position{}, err
	}
	return scanPosition(p.QueryRow(ctx,
		`UPDATE positions SET status = 'closed', updated_at = now() WHERE id = $1 RETURNING `+positionCols, id))
}

// ---- applications ------------------------------------------------------------------------

func (s pgStore) CreateApplication(ctx context.Context, in applicationInput) (application, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return application{}, err
	}
	var status string
	if err := p.QueryRow(ctx, `SELECT status FROM positions WHERE id = $1`, in.PositionID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return application{}, errInvalidRef
		}
		return application{}, mapErr(err)
	}
	if status != "open" {
		return application{}, fmt.Errorf("%w: the position is closed", errBadState)
	}
	return scanApplication(p.QueryRow(ctx,
		`INSERT INTO applications (applicant_id, position_id) VALUES ($1, $2) RETURNING `+applicationCols,
		in.ApplicantID, in.PositionID))
}

func (s pgStore) GetApplication(ctx context.Context, id int64) (application, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return application{}, err
	}
	return scanApplication(p.QueryRow(ctx, `SELECT `+applicationCols+` FROM applications WHERE id = $1`, id))
}

const applicationWhere = `($1 = '' OR status = $1) AND ($2::bigint = 0 OR applicant_id = $2) AND ($3::bigint = 0 OR position_id = $3)`

func (s pgStore) ListApplications(ctx context.Context, f applicationFilter) ([]application, int, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM applications WHERE `+applicationWhere,
		f.Status, f.ApplicantID, f.PositionID).Scan(&total); err != nil {
		return nil, 0, mapErr(err)
	}
	items, err := collect(ctx, p, scanApplication,
		`SELECT `+applicationCols+` FROM applications WHERE `+applicationWhere+` ORDER BY id LIMIT $4 OFFSET $5`,
		f.Status, f.ApplicantID, f.PositionID, f.Limit, f.Offset)
	return items, total, err
}

func (s pgStore) SetApplicationStatus(ctx context.Context, id int64, to string) (application, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return application{}, err
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		return application{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var from string
	if err := tx.QueryRow(ctx, `SELECT status FROM applications WHERE id = $1 FOR UPDATE`, id).Scan(&from); err != nil {
		return application{}, mapErr(err)
	}
	if err := checkTransition(from, to); err != nil {
		return application{}, err
	}
	a, err := scanApplication(tx.QueryRow(ctx,
		`UPDATE applications SET status = $2, updated_at = now() WHERE id = $1 RETURNING `+applicationCols, id, to))
	if err != nil {
		return application{}, err
	}
	return a, mapErr(tx.Commit(ctx))
}

func (s pgStore) DeleteApplication(ctx context.Context, id int64) error {
	p, err := s.pool(ctx)
	if err != nil {
		return err
	}
	return deleteByID(ctx, p, "applications", id)
}

func (s pgStore) RequestScreening(ctx context.Context, id int64) (application, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return application{}, err
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		return application{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status, screening string
	if err := tx.QueryRow(ctx, `SELECT status, screening_status FROM applications WHERE id = $1 FOR UPDATE`, id).Scan(&status, &screening); err != nil {
		return application{}, mapErr(err)
	}
	if status != "applied" && status != "screening" {
		return application{}, fmt.Errorf("%w: only applied or screening applications can be screened (status is %q)", errBadState, status)
	}
	if screening == "queued" {
		return application{}, fmt.Errorf("%w: a screening is already queued", errConflict)
	}
	a, err := scanApplication(tx.QueryRow(ctx,
		`UPDATE applications SET screening_status = 'queued', screening_requested_at = now(),
		   screening_score = NULL, screening_done_at = NULL, updated_at = now()
		 WHERE id = $1 RETURNING `+applicationCols, id))
	if err != nil {
		return application{}, err
	}
	return a, mapErr(tx.Commit(ctx))
}

func (s pgStore) CancelScreening(ctx context.Context, id int64) error {
	p, err := s.pool(ctx)
	if err != nil {
		return err
	}
	_, err = p.Exec(ctx, `UPDATE applications SET screening_status = 'none', screening_requested_at = NULL
		WHERE id = $1 AND screening_status = 'queued'`, id)
	return mapErr(err)
}

// ---- reports, search, seed ---------------------------------------------------------------

func (s pgStore) Summary(ctx context.Context) (summary, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return summary{}, err
	}
	var m summary
	err = p.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM applicants),
		(SELECT count(*) FROM positions WHERE status = 'open'),
		(SELECT count(*) FROM positions WHERE status = 'closed'),
		(SELECT count(*) FROM applications),
		(SELECT count(*) FROM applications WHERE screening_status = 'queued'),
		(SELECT count(*) FROM applications WHERE screening_status = 'done')`).
		Scan(&m.Applicants, &m.PositionsOpen, &m.PositionsClosed, &m.Applications, &m.ScreeningQueued, &m.ScreeningDone)
	return m, mapErr(err)
}

func (s pgStore) CountByStatus(ctx context.Context) (map[string]int, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := p.Query(ctx, `SELECT status, count(*) FROM applications GROUP BY status`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make(map[string]int, len(applicationStatuses))
	for _, st := range applicationStatuses {
		out[st] = 0 // every status is reported, even with no applications
	}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, mapErr(err)
		}
		out[st] = n
	}
	return out, mapErr(rows.Err())
}

func (s pgStore) TopPositions(ctx context.Context, limit int) ([]topPosition, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return nil, err
	}
	return collect(ctx, p, func(r pgx.Row) (topPosition, error) {
		var t topPosition
		err := r.Scan(&t.PositionID, &t.Title, &t.Applications)
		return t, mapErr(err)
	}, `SELECT p.id, p.title, count(a.id) AS n FROM positions p LEFT JOIN applications a ON a.position_id = p.id
		GROUP BY p.id ORDER BY n DESC, p.id LIMIT $1`, limit)
}

func (s pgStore) Search(ctx context.Context, q string, limit int) (searchResult, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return searchResult{}, err
	}
	pat := likePattern(q)
	as, err := collect(ctx, p, scanApplicant,
		`SELECT `+applicantCols+` FROM applicants WHERE `+applicantWhere+` ORDER BY id LIMIT $2`, pat, limit)
	if err != nil {
		return searchResult{}, err
	}
	ps, err := collect(ctx, p, scanPosition,
		`SELECT `+positionCols+` FROM positions WHERE title ILIKE $1 OR department ILIKE $1 ORDER BY id LIMIT $2`, pat, limit)
	return searchResult{Applicants: as, Positions: ps}, err
}

// Seed creates demo data in one transaction. Every row is tagged with a batch id, so repeated
// seeding never collides on the unique email.
func (s pgStore) Seed(ctx context.Context, req seedRequest) (seedResult, error) {
	p, err := s.pool(ctx)
	if err != nil {
		return seedResult{}, err
	}
	tx, err := p.Begin(ctx)
	if err != nil {
		return seedResult{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// A string: the SQL uses it as text, and the driver does not convert integers to text.
	batch := strconv.FormatInt(time.Now().UnixNano(), 10)

	ids := func(sql string, args ...any) ([]int64, error) {
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return nil, mapErr(err)
		}
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, mapErr(err)
			}
			out = append(out, id)
		}
		return out, mapErr(rows.Err())
	}
	aids, err := ids(`INSERT INTO applicants (name, email, skills)
		SELECT 'Seed Applicant ' || g, 'seed-' || $1::text || '-' || g || '@example.test',
		       ARRAY[(ARRAY['go','kubernetes','sql','react','aws','python'])[1 + g % 6],
		             (ARRAY['go','kubernetes','sql','react','aws','python'])[1 + (g * 7) % 6]]
		FROM generate_series(1, $2::int) g RETURNING id`, batch, req.Applicants)
	if err != nil {
		return seedResult{}, err
	}
	pids, err := ids(`INSERT INTO positions (title, department, location)
		SELECT 'Seed Position ' || g || ' (' || $1::text || ')',
		       (ARRAY['Engineering','Product','Design','Sales','Support'])[1 + g % 5],
		       (ARRAY['Hanoi','Ho Chi Minh City','Remote'])[1 + g % 3]
		FROM generate_series(1, $2::int) g RETURNING id`, batch, req.Positions)
	if err != nil {
		return seedResult{}, err
	}

	// Pair applicants and positions so that no pair repeats (a pair must be unique).
	n := min(req.Applications, len(aids)*len(pids))
	as, ps := make([]int64, 0, n), make([]int64, 0, n)
	for k := 0; k < n; k++ {
		as = append(as, aids[k%len(aids)])
		ps = append(ps, pids[(k/len(aids)+k%len(aids))%len(pids)])
	}
	apps := 0
	if n > 0 {
		tag, err := tx.Exec(ctx, `INSERT INTO applications (applicant_id, position_id)
			SELECT a, p FROM unnest($1::bigint[], $2::bigint[]) AS t(a, p) ON CONFLICT DO NOTHING`, as, ps)
		if err != nil {
			return seedResult{}, mapErr(err)
		}
		apps = int(tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return seedResult{}, mapErr(err)
	}
	return seedResult{Applicants: len(aids), Positions: len(pids), Applications: apps}, nil
}

func (s pgStore) Ping(ctx context.Context) error {
	p, err := s.pool(ctx)
	if err != nil {
		return err
	}
	return p.Ping(ctx)
}
