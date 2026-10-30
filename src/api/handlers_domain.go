package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

// registerDomain adds the recruitment endpoints (applicants, positions, applications, reports).
func (s *server) registerDomain(mux *http.ServeMux) {
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /version", s.version)

	mux.HandleFunc("POST /applicants", s.createApplicant)
	mux.HandleFunc("GET /applicants", s.listApplicants)
	mux.HandleFunc("GET /applicants/{id}", s.getApplicant)
	mux.HandleFunc("PUT /applicants/{id}", s.updateApplicant)
	mux.HandleFunc("DELETE /applicants/{id}", s.deleteApplicant)
	mux.HandleFunc("GET /applicants/{id}/applications", s.applicantApplications)

	mux.HandleFunc("POST /positions", s.createPosition)
	mux.HandleFunc("GET /positions", s.listPositions)
	mux.HandleFunc("GET /positions/{id}", s.getPosition)
	mux.HandleFunc("PUT /positions/{id}", s.updatePosition)
	mux.HandleFunc("DELETE /positions/{id}", s.deletePosition)
	mux.HandleFunc("POST /positions/{id}/close", s.closePosition)
	mux.HandleFunc("GET /positions/{id}/applications", s.positionApplications)

	mux.HandleFunc("POST /applications", s.createApplication)
	mux.HandleFunc("GET /applications", s.listApplications)
	mux.HandleFunc("GET /applications/{id}", s.getApplication)
	mux.HandleFunc("PATCH /applications/{id}", s.patchApplication)
	mux.HandleFunc("DELETE /applications/{id}", s.deleteApplication)
	mux.HandleFunc("POST /applications/{id}/screen", s.screenApplication)
	mux.HandleFunc("GET /applications/{id}/screening", s.getScreening)

	mux.HandleFunc("GET /stats/summary", s.statsSummary)
	mux.HandleFunc("GET /stats/applications-by-status", s.statsByStatus)
	mux.HandleFunc("GET /stats/top-positions", s.statsTopPositions)
	mux.HandleFunc("GET /search", s.search)
	mux.HandleFunc("POST /seed", s.seed)
}

// ---- helpers -----------------------------------------------------------------------------

const requestTimeout = 5 * time.Second

func (s *server) needStore(w http.ResponseWriter) (store, bool) {
	if s.st == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "database is not configured"})
		return nil, false
	}
	return s.st, true
}

// writeError maps domain errors to HTTP statuses. Unknown errors (typically the database being
// unreachable) become 503 and are logged, never echoed to the client.
func writeError(w http.ResponseWriter, err error) {
	var ve validationError
	var te transitionError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ve.msg, "field": ve.field})
	case errors.As(err, &te):
		writeJSON(w, http.StatusConflict, map[string]any{"error": te.Error(), "allowed": te.allowed})
	case errors.Is(err, errNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
	case errors.Is(err, errConflict):
		msg := "conflicts with an existing record"
		if e := err.Error(); strings.Contains(e, ": ") {
			msg = e[strings.Index(e, ": ")+2:]
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": msg})
	case errors.Is(err, errInvalidRef):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": errInvalidRef.Error()})
	case errors.Is(err, errBadState):
		msg := err.Error()
		if i := strings.Index(msg, ": "); i >= 0 {
			msg = msg[i+2:]
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": msg})
	default:
		slog.Error("request failed", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "database unavailable"})
	}
}

// readJSON decodes a small JSON body and rejects unknown fields and trailing data.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return validationError{"body", "a JSON body is required"}
		}
		return validationError{"body", "invalid JSON: " + err.Error()}
	}
	if dec.More() {
		return validationError{"body", "unexpected data after the JSON value"}
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, validationError{"id", "must be a positive integer"}
	}
	return id, nil
}

func intParam(r *http.Request, name string, def, min, max int) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		return 0, validationError{name, fmt.Sprintf("must be a number from %d to %d", min, max)}
	}
	return n, nil
}

func pageParams(r *http.Request) (limit, offset int, err error) {
	if limit, err = intParam(r, "limit", 20, 1, 100); err != nil {
		return
	}
	offset, err = intParam(r, "offset", 0, 0, 1_000_000)
	return
}

func writePage[T any](w http.ResponseWriter, items []T, total, limit, offset int) {
	writeJSON(w, http.StatusOK, page[T]{Items: items, Total: total, Limit: limit, Offset: offset})
}

// handle is the common wrapper: it checks the database, applies a timeout and turns a returned
// error into the right response, so each handler only contains its own logic.
func (s *server) handle(fn func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, ok := s.needStore(w)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		if err := fn(ctx, st, w, r); err != nil {
			writeError(w, err)
		}
	}
}

// ---- system ------------------------------------------------------------------------------

func (s *server) readyz(w http.ResponseWriter, r *http.Request) {
	if s.st == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "database": "not configured"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.st.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "database": "down"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "database": "up"})
}

func (s *server) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": version, "go": runtime.Version(), "arch": runtime.GOARCH})
}

// ---- applicants --------------------------------------------------------------------------

func (s *server) createApplicant(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		var in applicantInput
		if err := readJSON(w, r, &in); err != nil {
			return err
		}
		if err := in.validate(); err != nil {
			return err
		}
		a, err := st.CreateApplicant(ctx, in)
		if err != nil {
			return err
		}
		w.Header().Set("Location", fmt.Sprintf("/applicants/%d", a.ID))
		writeJSON(w, http.StatusCreated, a)
		return nil
	})(w, r)
}

func (s *server) listApplicants(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		limit, offset, err := pageParams(r)
		if err != nil {
			return err
		}
		items, total, err := st.ListApplicants(ctx, applicantFilter{Q: r.URL.Query().Get("q"), Limit: limit, Offset: offset})
		if err != nil {
			return err
		}
		writePage(w, items, total, limit, offset)
		return nil
	})(w, r)
}

func (s *server) getApplicant(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		a, err := st.GetApplicant(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, a)
		return nil
	})(w, r)
}

func (s *server) updateApplicant(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var in applicantInput
		if err := readJSON(w, r, &in); err != nil {
			return err
		}
		if err := in.validate(); err != nil {
			return err
		}
		a, err := st.UpdateApplicant(ctx, id, in)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, a)
		return nil
	})(w, r)
}

func (s *server) deleteApplicant(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := st.DeleteApplicant(ctx, id); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	})(w, r)
}

func (s *server) applicantApplications(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		limit, offset, err := pageParams(r)
		if err != nil {
			return err
		}
		if _, err := st.GetApplicant(ctx, id); err != nil {
			return err
		}
		items, total, err := st.ListApplications(ctx, applicationFilter{ApplicantID: id, Status: r.URL.Query().Get("status"), Limit: limit, Offset: offset})
		if err != nil {
			return err
		}
		writePage(w, items, total, limit, offset)
		return nil
	})(w, r)
}

// ---- positions ---------------------------------------------------------------------------

func (s *server) createPosition(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		var in positionInput
		if err := readJSON(w, r, &in); err != nil {
			return err
		}
		if err := in.validate(); err != nil {
			return err
		}
		p, err := st.CreatePosition(ctx, in)
		if err != nil {
			return err
		}
		w.Header().Set("Location", fmt.Sprintf("/positions/%d", p.ID))
		writeJSON(w, http.StatusCreated, p)
		return nil
	})(w, r)
}

func (s *server) listPositions(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		limit, offset, err := pageParams(r)
		if err != nil {
			return err
		}
		status := r.URL.Query().Get("status")
		if status != "" && status != "open" && status != "closed" {
			return validationError{"status", "must be open or closed"}
		}
		items, total, err := st.ListPositions(ctx, positionFilter{Department: r.URL.Query().Get("department"), Status: status, Limit: limit, Offset: offset})
		if err != nil {
			return err
		}
		writePage(w, items, total, limit, offset)
		return nil
	})(w, r)
}

func (s *server) getPosition(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		p, err := st.GetPosition(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, p)
		return nil
	})(w, r)
}

func (s *server) updatePosition(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var in positionInput
		if err := readJSON(w, r, &in); err != nil {
			return err
		}
		if err := in.validate(); err != nil {
			return err
		}
		p, err := st.UpdatePosition(ctx, id, in)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, p)
		return nil
	})(w, r)
}

func (s *server) deletePosition(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := st.DeletePosition(ctx, id); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	})(w, r)
}

func (s *server) closePosition(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		p, err := st.ClosePosition(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, p)
		return nil
	})(w, r)
}

func (s *server) positionApplications(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		limit, offset, err := pageParams(r)
		if err != nil {
			return err
		}
		if _, err := st.GetPosition(ctx, id); err != nil {
			return err
		}
		items, total, err := st.ListApplications(ctx, applicationFilter{PositionID: id, Status: r.URL.Query().Get("status"), Limit: limit, Offset: offset})
		if err != nil {
			return err
		}
		writePage(w, items, total, limit, offset)
		return nil
	})(w, r)
}

// ---- applications ------------------------------------------------------------------------

func (s *server) createApplication(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		var in applicationInput
		if err := readJSON(w, r, &in); err != nil {
			return err
		}
		if err := in.validate(); err != nil {
			return err
		}
		a, err := st.CreateApplication(ctx, in)
		if err != nil {
			return err
		}
		w.Header().Set("Location", fmt.Sprintf("/applications/%d", a.ID))
		writeJSON(w, http.StatusCreated, a)
		return nil
	})(w, r)
}

func (s *server) listApplications(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		limit, offset, err := pageParams(r)
		if err != nil {
			return err
		}
		status := r.URL.Query().Get("status")
		if status != "" && !isApplicationStatus(status) {
			return validationError{"status", "must be one of " + strings.Join(applicationStatuses, ", ")}
		}
		items, total, err := st.ListApplications(ctx, applicationFilter{Status: status, Limit: limit, Offset: offset})
		if err != nil {
			return err
		}
		writePage(w, items, total, limit, offset)
		return nil
	})(w, r)
}

func (s *server) getApplication(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		a, err := st.GetApplication(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, a)
		return nil
	})(w, r)
}

func (s *server) patchApplication(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var in statusInput
		if err := readJSON(w, r, &in); err != nil {
			return err
		}
		if !isApplicationStatus(in.Status) {
			return validationError{"status", "must be one of " + strings.Join(applicationStatuses, ", ")}
		}
		a, err := st.SetApplicationStatus(ctx, id, in.Status)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, a)
		return nil
	})(w, r)
}

func (s *server) deleteApplication(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := st.DeleteApplication(ctx, id); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	})(w, r)
}

// screenApplication queues a screening job on SQS; a worker scores the application later.
func (s *server) screenApplication(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if s.sqs == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "QUEUE_URL is not configured"})
			return nil
		}
		if _, err := st.RequestScreening(ctx, id); err != nil {
			return err
		}
		body := fmt.Sprintf(`{"type":"screen","applicationId":%d,"requestedAt":%q}`, id, time.Now().UTC().Format(time.RFC3339))
		out, sendErr := s.sqs.SendMessageBatch(ctx, &sqs.SendMessageBatchInput{
			QueueUrl: aws.String(s.queueURL),
			Entries:  []types.SendMessageBatchRequestEntry{{Id: aws.String("1"), MessageBody: aws.String(body)}},
		})
		if sendErr == nil && len(out.Failed) > 0 {
			sendErr = errors.New("queue rejected the message")
		}
		if sendErr != nil {
			slog.Error("queue screening", "err", sendErr)
			// Undo the "queued" mark so the client can try again; use a fresh context in case ctx expired.
			cctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
			defer cancel()
			_ = st.CancelScreening(cctx, id)
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "failed to queue the screening"})
			return nil
		}
		s.wakeWorker()
		writeJSON(w, http.StatusAccepted, map[string]any{"applicationId": id, "screeningStatus": "queued"})
		return nil
	})(w, r)
}

func (s *server) getScreening(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		a, err := st.GetApplication(ctx, id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"applicationId": a.ID, "screeningStatus": a.ScreeningStatus, "screeningScore": a.ScreeningScore,
			"requestedAt": a.ScreeningRequestedAt, "doneAt": a.ScreeningDoneAt, "applicationStatus": a.Status,
		})
		return nil
	})(w, r)
}

// ---- reports, search, seed ---------------------------------------------------------------

func (s *server) statsSummary(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		m, err := st.Summary(ctx)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, m)
		return nil
	})(w, r)
}

func (s *server) statsByStatus(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		m, err := st.CountByStatus(ctx)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, m)
		return nil
	})(w, r)
}

func (s *server) statsTopPositions(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		limit, err := intParam(r, "limit", 5, 1, 50)
		if err != nil {
			return err
		}
		items, err := st.TopPositions(ctx, limit)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return nil
	})(w, r)
}

func (s *server) search(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if len([]rune(q)) < 2 {
			return validationError{"q", "must be at least 2 characters"}
		}
		limit, err := intParam(r, "limit", 10, 1, 50)
		if err != nil {
			return err
		}
		res, err := st.Search(ctx, q, limit)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, res)
		return nil
	})(w, r)
}

// seed creates demo data so the endpoints and load tests have something to read.
func (s *server) seed(w http.ResponseWriter, r *http.Request) {
	s.handle(func(ctx context.Context, st store, w http.ResponseWriter, r *http.Request) error {
		var req seedRequest
		if r.ContentLength != 0 {
			if err := readJSON(w, r, &req); err != nil {
				return err
			}
		}
		req.normalize()
		res, err := st.Seed(ctx, req)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusCreated, res)
		return nil
	})(w, r)
}
