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
