package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"demo/internal/db"
)

// These tests need a real PostgreSQL. They run only when DB_HOST (and the other DB_* variables) are
// set, for example against a throwaway container, and are skipped otherwise.

type captureSQS struct {
	mu     sync.Mutex
	bodies []string
	fail   bool
}

func (c *captureSQS) SendMessageBatch(_ context.Context, in *sqs.SendMessageBatchInput, _ ...func(*sqs.Options)) (*sqs.SendMessageBatchOutput, error) {
	if c.fail {
		return nil, fmt.Errorf("queue unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ok := make([]types.SendMessageBatchResultEntry, len(in.Entries))
	for _, e := range in.Entries {
		c.bodies = append(c.bodies, *e.MessageBody)
	}
	return &sqs.SendMessageBatchOutput{Successful: ok}, nil
}

type env struct {
	h   http.Handler
	d   *db.DB
	sqs *captureSQS
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := db.New()
	if d == nil {
		t.Skip("DB_HOST is not set: skipping the PostgreSQL integration tests")
	}
	q := &captureSQS{}
	s := &server{started: time.Now(), pod: "it-pod", db: d, st: pgStore{d: d}, sqs: q, queueURL: "http://queue"}
	return &env{h: s.routes(), d: d, sqs: q}
}

// call sends a request and returns the status and the decoded JSON object (nil for empty bodies).
func (e *env) call(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.ContentLength = int64(len(body))
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: response is not JSON: %s", method, path, rec.Body)
		}
	}
	return rec.Code, out
}

func (e *env) want(t *testing.T, code int, got int, body map[string]any, what string) map[string]any {
	t.Helper()
	if got != code {
		t.Fatalf("%s: status %d, want %d (body %v)", what, got, code, body)
	}
	return body
}

func num(m map[string]any, key string) int64 { return int64(m[key].(float64)) }

func uniq(prefix string) string { return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()) }

func TestIntegrationApplicantsAndPositions(t *testing.T) {
	e := newEnv(t)
	email := uniq("ada") + "@example.test"

	code, a := e.call(t, "POST", "/applicants", fmt.Sprintf(`{"name":"Ada","email":%q,"skills":["go","sql"]}`, email))
	e.want(t, 201, code, a, "create applicant")
	id := num(a, "id")

	code, dup := e.call(t, "POST", "/applicants", fmt.Sprintf(`{"name":"Other","email":%q}`, email))
	e.want(t, 409, code, dup, "duplicate email")

	code, got := e.call(t, "GET", fmt.Sprintf("/applicants/%d", id), "")
	e.want(t, 200, code, got, "get applicant")
	if got["email"] != email {
		t.Errorf("email = %v", got["email"])
	}

	code, upd := e.call(t, "PUT", fmt.Sprintf("/applicants/%d", id), fmt.Sprintf(`{"name":"Ada L","email":%q,"phone":"123","skills":["go"]}`, email))
	e.want(t, 200, code, upd, "update applicant")
	if upd["name"] != "Ada L" || upd["phone"] != "123" {
		t.Errorf("update not applied: %v", upd)
	}

	code, lst := e.call(t, "GET", "/applicants?q="+email[:12], "")
	e.want(t, 200, code, lst, "search applicants")
	if lst["total"].(float64) < 1 {
		t.Errorf("list by query found nothing: %v", lst)
	}

	code, nf := e.call(t, "GET", "/applicants/999999999", "")
	e.want(t, 404, code, nf, "missing applicant")
	code, nf = e.call(t, "PUT", "/applicants/999999999", fmt.Sprintf(`{"name":"X","email":%q}`, uniq("x")+"@example.test"))
	e.want(t, 404, code, nf, "update missing applicant")

	// positions
	code, p := e.call(t, "POST", "/positions", `{"title":"Platform Engineer","department":"Eng-it","location":"Remote"}`)
	e.want(t, 201, code, p, "create position")
	pid := num(p, "id")
	if p["status"] != "open" {
		t.Errorf("new position should be open: %v", p)
	}
	code, lp := e.call(t, "GET", "/positions?department=Eng-it&status=open", "")
	e.want(t, 200, code, lp, "list positions")
	if lp["total"].(float64) < 1 {
		t.Errorf("filter found nothing: %v", lp)
	}
	code, up := e.call(t, "PUT", fmt.Sprintf("/positions/%d", pid), `{"title":"Senior Platform Engineer","department":"Eng-it","location":"Hanoi","status":"open"}`)
	e.want(t, 200, code, up, "update position")
	code, cl := e.call(t, "POST", fmt.Sprintf("/positions/%d/close", pid), "")
	e.want(t, 200, code, cl, "close position")
	if cl["status"] != "closed" {
		t.Errorf("close did not close: %v", cl)
	}

	code, _ = e.call(t, "DELETE", fmt.Sprintf("/positions/%d", pid), "")
	if code != 204 {
		t.Errorf("delete position = %d, want 204", code)
	}
	code, _ = e.call(t, "DELETE", fmt.Sprintf("/applicants/%d", id), "")
	if code != 204 {
		t.Errorf("delete applicant = %d, want 204", code)
	}
	code, nf = e.call(t, "DELETE", fmt.Sprintf("/applicants/%d", id), "")
	e.want(t, 404, code, nf, "delete twice")
}

func TestIntegrationApplicationWorkflow(t *testing.T) {
	e := newEnv(t)
	_, a := e.call(t, "POST", "/applicants", fmt.Sprintf(`{"name":"Flow","email":%q}`, uniq("flow")+"@example.test"))
	_, p := e.call(t, "POST", "/positions", `{"title":"Flow role","department":"Flow-dept"}`)
	aid, pid := num(a, "id"), num(p, "id")

	body := fmt.Sprintf(`{"applicantId":%d,"positionId":%d}`, aid, pid)
	code, app := e.call(t, "POST", "/applications", body)
	e.want(t, 201, code, app, "apply")
	id := num(app, "id")
	if app["status"] != "applied" || app["screeningStatus"] != "none" {
		t.Errorf("new application state: %v", app)
	}

	code, dup := e.call(t, "POST", "/applications", body)
	e.want(t, 409, code, dup, "apply twice")
	code, bad := e.call(t, "POST", "/applications", fmt.Sprintf(`{"applicantId":999999999,"positionId":%d}`, pid))
	e.want(t, 422, code, bad, "unknown applicant")
	code, bad = e.call(t, "POST", "/applications", fmt.Sprintf(`{"applicantId":%d,"positionId":999999999}`, aid))
	e.want(t, 422, code, bad, "unknown position")

	// An invalid jump is refused and tells the client what is allowed.
	code, tr := e.call(t, "PATCH", fmt.Sprintf("/applications/%d", id), `{"status":"offer"}`)
	e.want(t, 409, code, tr, "applied -> offer")
	if tr["allowed"] == nil {
		t.Errorf("a refused transition should list the allowed ones: %v", tr)
	}
	for _, next := range []string{"screening", "interview", "offer", "hired"} {
		code, r := e.call(t, "PATCH", fmt.Sprintf("/applications/%d", id), fmt.Sprintf(`{"status":%q}`, next))
		e.want(t, 200, code, r, "-> "+next)
	}
	code, fin := e.call(t, "PATCH", fmt.Sprintf("/applications/%d", id), `{"status":"rejected"}`)
	e.want(t, 409, code, fin, "hired is final")

	// sub-resource listings
	code, la := e.call(t, "GET", fmt.Sprintf("/applicants/%d/applications", aid), "")
	e.want(t, 200, code, la, "applications of the applicant")
	if la["total"].(float64) != 1 {
		t.Errorf("applicant should have 1 application: %v", la)
	}
	code, lp := e.call(t, "GET", fmt.Sprintf("/positions/%d/applications?status=hired", pid), "")
	e.want(t, 200, code, lp, "applications of the position")
	if lp["total"].(float64) != 1 {
		t.Errorf("position should have 1 hired application: %v", lp)
	}
	code, nf := e.call(t, "GET", "/applicants/999999999/applications", "")
	e.want(t, 404, code, nf, "applications of a missing applicant")

	// A closed position takes no more applications.
	_, p2 := e.call(t, "POST", "/positions", `{"title":"Closed role","department":"Flow-dept","status":"closed"}`)
	code, cl := e.call(t, "POST", "/applications", fmt.Sprintf(`{"applicantId":%d,"positionId":%d}`, aid, num(p2, "id")))
	e.want(t, 409, code, cl, "apply to a closed position")

	// Deleting the applicant removes their applications (cascade).
	code, _ = e.call(t, "DELETE", fmt.Sprintf("/applicants/%d", aid), "")
	if code != 204 {
		t.Fatalf("delete applicant = %d", code)
	}
	code, gone := e.call(t, "GET", fmt.Sprintf("/applications/%d", id), "")
	e.want(t, 404, code, gone, "application after cascade delete")
}

// The whole asynchronous path: API marks the screening queued and sends a message; the worker's
// database call completes it; the API then reports the result.
func TestIntegrationScreeningFlow(t *testing.T) {
	e := newEnv(t)
	_, a := e.call(t, "POST", "/applicants", fmt.Sprintf(`{"name":"Screen","email":%q}`, uniq("scr")+"@example.test"))
	_, p := e.call(t, "POST", "/positions", `{"title":"Screen role","department":"Screen-dept"}`)
	_, app := e.call(t, "POST", "/applications", fmt.Sprintf(`{"applicantId":%d,"positionId":%d}`, num(a, "id"), num(p, "id")))
	id := num(app, "id")
	path := fmt.Sprintf("/applications/%d", id)

	code, q := e.call(t, "POST", path+"/screen", "")
	e.want(t, 202, code, q, "queue screening")
	if q["screeningStatus"] != "queued" {
		t.Errorf("screening should be queued: %v", q)
	}
	if len(e.sqs.bodies) != 1 || !strings.Contains(e.sqs.bodies[0], fmt.Sprintf(`"applicationId":%d`, id)) || !strings.Contains(e.sqs.bodies[0], `"type":"screen"`) {
		t.Fatalf("queue message = %v", e.sqs.bodies)
	}
	code, again := e.call(t, "POST", path+"/screen", "")
	e.want(t, 409, code, again, "queue twice")
	code, st := e.call(t, "GET", path+"/screening", "")
	e.want(t, 200, code, st, "status while queued")
	if st["screeningStatus"] != "queued" || st["screeningScore"] != nil {
		t.Errorf("queued state: %v", st)
	}

	// What the worker does when it receives the message.
	found, err := e.d.Screen(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("worker Screen = %v, %v", found, err)
	}
	code, done := e.call(t, "GET", path+"/screening", "")
	e.want(t, 200, code, done, "status after the worker")
	if done["screeningStatus"] != "done" || int(done["screeningScore"].(float64)) != db.ScreeningScore(id) {
		t.Errorf("done state: %v (want score %d)", done, db.ScreeningScore(id))
	}
	if done["applicationStatus"] != "screening" {
		t.Errorf("a screened new application moves to 'screening': %v", done)
	}
	// A repeated or stale message changes nothing.
	if again, err := e.d.Screen(context.Background(), id); err != nil || again {
		t.Errorf("second Screen = %v, %v; want false, nil", again, err)
	}

	// A finished application cannot be screened.
	e.call(t, "PATCH", path, `{"status":"rejected"}`)
	code, no := e.call(t, "POST", path+"/screen", "")
	e.want(t, 409, code, no, "screen a rejected application")
}

func TestIntegrationScreeningIsUndoneWhenTheQueueFails(t *testing.T) {
	e := newEnv(t)
	_, a := e.call(t, "POST", "/applicants", fmt.Sprintf(`{"name":"Q","email":%q}`, uniq("q")+"@example.test"))
	_, p := e.call(t, "POST", "/positions", `{"title":"Q role","department":"Q-dept"}`)
	_, app := e.call(t, "POST", "/applications", fmt.Sprintf(`{"applicantId":%d,"positionId":%d}`, num(a, "id"), num(p, "id")))
	path := fmt.Sprintf("/applications/%d", num(app, "id"))

	e.sqs.fail = true
	code, r := e.call(t, "POST", path+"/screen", "")
	e.want(t, 502, code, r, "queue failure")
	_, st := e.call(t, "GET", path+"/screening", "")
	if st["screeningStatus"] != "none" {
		t.Fatalf("a failed queue send must leave the application unscreened, got %v", st)
	}
	e.sqs.fail = false
	code, ok := e.call(t, "POST", path+"/screen", "")
	e.want(t, 202, code, ok, "retry after the failure")
}

func TestIntegrationReportsSearchAndSeed(t *testing.T) {
	e := newEnv(t)
	_, before := e.call(t, "GET", "/stats/summary", "")

	code, s := e.call(t, "POST", "/seed", `{"applicants":30,"positions":4,"applications":60}`)
	e.want(t, 201, code, s, "seed")
	if s["applicants"].(float64) != 30 || s["positions"].(float64) != 4 || s["applications"].(float64) != 60 {
		t.Fatalf("seed result: %v", s)
	}
	code, def := e.call(t, "POST", "/seed", "")
	e.want(t, 201, code, def, "seed with defaults")
	if def["applicants"].(float64) != 20 {
		t.Errorf("default seed: %v", def)
	}

	_, after := e.call(t, "GET", "/stats/summary", "")
	if grown := after["applicants"].(float64) - before["applicants"].(float64); grown != 50 {
		t.Errorf("applicants grew by %v, want 50", grown)
	}
	if grown := after["applications"].(float64) - before["applications"].(float64); grown != 100 {
		t.Errorf("applications grew by %v, want 100", grown)
	}

	code, by := e.call(t, "GET", "/stats/applications-by-status", "")
	e.want(t, 200, code, by, "by status")
	var sum float64
	for _, st := range applicationStatuses {
		v, ok := by[st]
		if !ok {
			t.Errorf("status %q missing from the report", st)
		}
		sum += v.(float64)
	}
	if sum != after["applications"].(float64) {
		t.Errorf("per-status counts add up to %v, total is %v", sum, after["applications"])
	}

	code, top := e.call(t, "GET", "/stats/top-positions?limit=3", "")
	e.want(t, 200, code, top, "top positions")
	if len(top["items"].([]any)) == 0 || len(top["items"].([]any)) > 3 {
		t.Errorf("top positions: %v", top)
	}

	code, found := e.call(t, "GET", "/search?q=Seed%20Position", "")
	e.want(t, 200, code, found, "search")
	if len(found["positions"].([]any)) == 0 {
		t.Errorf("search found no seeded position: %v", found)
	}
	// The LIKE wildcard must be taken literally, not as "match anything".
	code, wild := e.call(t, "GET", "/search?q=%25%25", "")
	e.want(t, 200, code, wild, "search for percent signs")
	if len(wild["applicants"].([]any)) != 0 || len(wild["positions"].([]any)) != 0 {
		t.Errorf("a literal %%%% should match nothing: %v", wild)
	}

	// pagination
	code, pg := e.call(t, "GET", "/applicants?limit=10&offset=20", "")
	e.want(t, 200, code, pg, "page")
	if len(pg["items"].([]any)) != 10 || pg["total"].(float64) < 30 || pg["limit"].(float64) != 10 || pg["offset"].(float64) != 20 {
		t.Errorf("page envelope: limit %v offset %v total %v items %d", pg["limit"], pg["offset"], pg["total"], len(pg["items"].([]any)))
	}
}

func TestIntegrationReadyzAndVisits(t *testing.T) {
	e := newEnv(t)
	code, r := e.call(t, "GET", "/readyz", "")
	e.want(t, 200, code, r, "readyz")
	if r["database"] != "up" {
		t.Errorf("readyz: %v", r)
	}
	code, v1 := e.call(t, "GET", "/db", "")
	e.want(t, 200, code, v1, "db 1")
	_, v2 := e.call(t, "GET", "/db", "")
	if v2["visits"].(float64) != v1["visits"].(float64)+1 {
		t.Errorf("visit counter: %v then %v", v1["visits"], v2["visits"])
	}
}

// Many pods run the schema DDL at the same moment on a fresh database; it must not fail.
func TestIntegrationConcurrentFirstUse(t *testing.T) {
	if os.Getenv("DB_HOST") == "" {
		t.Skip("DB_HOST is not set")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := db.New() // a separate pool, as a separate pod would have
			if _, err := d.Pool(context.Background()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent first connect failed: %v", err)
	}
}
