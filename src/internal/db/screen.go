package db

import (
	"context"
	"fmt"
)

// ScreeningScore is the deterministic score the demo "screening" gives an application, so tests
// and demos can predict it. A real screener would score the applicant's profile.
func ScreeningScore(applicationID int64) int {
	return 50 + int(applicationID*37%51)
}

// Screen completes a queued screening: it stores the score, marks the screening done and moves a
// freshly applied application to "screening". It reports whether a queued screening was found,
// so a repeated or stale message changes nothing.
func (d *DB) Screen(ctx context.Context, applicationID int64) (bool, error) {
	pool, err := d.Pool(ctx)
	if err != nil {
		return false, err
	}
	tag, err := pool.Exec(ctx, `
		UPDATE applications
		SET screening_status = 'done',
		    screening_score = $2,
		    screening_done_at = now(),
		    status = CASE WHEN status = 'applied' THEN 'screening' ELSE status END,
		    updated_at = now()
		WHERE id = $1 AND screening_status = 'queued'`, applicationID, ScreeningScore(applicationID))
	if err != nil {
		return false, fmt.Errorf("screen application %d: %w", applicationID, err)
	}
	return tag.RowsAffected() == 1, nil
}

// FailScreening ends a queued screening as 'failed' and keeps the reason, so the application does not stay
// 'queued' forever when the worker gave up. It reports whether a queued screening was found. A new
// POST /applications/{id}/screen starts it again.
func (d *DB) FailScreening(ctx context.Context, applicationID int64, reason string) (bool, error) {
	pool, err := d.Pool(ctx)
	if err != nil {
		return false, err
	}
	if len(reason) > 500 {
		reason = reason[:500]
	}
	tag, err := pool.Exec(ctx, `
		UPDATE applications
		SET screening_status = 'failed',
		    screening_error = $2,
		    screening_done_at = now(),
		    updated_at = now()
		WHERE id = $1 AND screening_status = 'queued'`, applicationID, reason)
	if err != nil {
		return false, fmt.Errorf("fail screening %d: %w", applicationID, err)
	}
	return tag.RowsAffected() == 1, nil
}
