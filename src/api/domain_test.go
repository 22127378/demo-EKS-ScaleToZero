package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubStore satisfies store but panics if a handler reaches the database: the tests below only
// exercise code that must fail (or answer) before any query.
type stubStore struct{ store }

func handlerWithStub() http.Handler {
	s := &server{started: time.Now(), pod: "pod-1", st: stubStore{}}
	return s.routes()
}

func TestTransitions(t *testing.T) {
	ok := [][2]string{
		{"applied", "screening"}, {"applied", "rejected"}, {"applied", "withdrawn"},
		{"screening", "interview"}, {"interview", "offer"}, {"offer", "hired"}, {"offer", "withdrawn"},
	}
	for _, c := range ok {
		if err := checkTransition(c[0], c[1]); err != nil {
			t.Errorf("%s -> %s should be allowed: %v", c[0], c[1], err)
		}
	}
	bad := [][2]string{
		{"applied", "offer"}, {"applied", "hired"}, {"screening", "applied"}, {"interview", "screening"},
		{"hired", "rejected"}, {"rejected", "applied"}, {"withdrawn", "applied"},
	}
	for _, c := range bad {
		var te transitionError
		if err := checkTransition(c[0], c[1]); !errors.As(err, &te) {
			t.Errorf("%s -> %s should be refused with a transitionError, got %v", c[0], c[1], err)
		}
	}
	var ve validationError
	if err := checkTransition("applied", "bogus"); !errors.As(err, &ve) {
		t.Errorf("unknown status should be a validation error, got %v", err)
	}
	for _, final := range []string{"hired", "rejected", "withdrawn"} {
		if len(transitions[final]) != 0 {
			t.Errorf("%s must be a final status", final)
		}
	}
}

func TestEveryStatusHasATransitionEntry(t *testing.T) {
	for _, s := range applicationStatuses {
		if _, ok := transitions[s]; !ok {
			t.Errorf("status %q has no entry in the transition table", s)
		}
	}
}

func TestApplicantValidation(t *testing.T) {
	good := applicantInput{Name: "  Ada Lovelace ", Email: " ADA@Example.com ", Skills: []string{" go "}}
	if err := good.validate(); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	if good.Name != "Ada Lovelace" || good.Email != "ada@example.com" || good.Skills[0] != "go" {
		t.Errorf("input not normalized: %+v", good)
	}
	cases := map[string]applicantInput{
		"name":   {Name: "", Email: "a@b.co"},
		"email":  {Name: "A", Email: "not-an-email"},
		"phone":  {Name: "A", Email: "a@b.co", Phone: strings.Repeat("1", 31)},
		"skills": {Name: "A", Email: "a@b.co", Skills: make([]string, 21)},
	}
	for field, in := range cases {
		var ve validationError
		if err := in.validate(); !errors.As(err, &ve) || ve.field != field {
			t.Errorf("expected a validation error on %q, got %v", field, err)
		}
	}
}

func TestPositionValidationDefaultsToOpen(t *testing.T) {
	p := positionInput{Title: "Engineer", Department: "Eng"}
	if err := p.validate(); err != nil || p.Status != "open" {
		t.Fatalf("validate = %v, status %q", err, p.Status)
	}
	bad := positionInput{Title: "x", Department: "y", Status: "paused"}
	var ve validationError
	if err := bad.validate(); !errors.As(err, &ve) || ve.field != "status" {
		t.Fatalf("expected a status validation error, got %v", err)
	}
}

func TestSeedRequestIsCapped(t *testing.T) {
	r := seedRequest{Applicants: 100000, Positions: -5, Applications: 0}
	r.normalize()
	if r.Applicants != 500 || r.Positions != 0 || r.Applications != 40 {
		t.Fatalf("normalize = %+v", r)
	}
}

func TestLikePatternEscapesWildcards(t *testing.T) {
	if got := likePattern(`50%_off\`); got != `%50\%\_off\\%` {
		t.Errorf("likePattern = %q", got)
	}
	if likePattern("   ") != "" {
		t.Error("blank query must mean no filter")
	}
}

// Without a database every data endpoint answers 503 and says why; /readyz and /version still work.
func TestDomainEndpointsWithoutDatabase(t *testing.T) {
	h := newTestServer(nil)
	routes := []struct{ method, path, body string }{
		{"POST", "/applicants", `{}`}, {"GET", "/applicants", ""}, {"GET", "/applicants/1", ""},
		{"PUT", "/applicants/1", `{}`}, {"DELETE", "/applicants/1", ""}, {"GET", "/applicants/1/applications", ""},
		{"POST", "/positions", `{}`}, {"GET", "/positions", ""}, {"GET", "/positions/1", ""},
		{"PUT", "/positions/1", `{}`}, {"DELETE", "/positions/1", ""}, {"POST", "/positions/1/close", ""},
		{"GET", "/positions/1/applications", ""}, {"POST", "/applications", `{}`}, {"GET", "/applications", ""},
		{"GET", "/applications/1", ""}, {"PATCH", "/applications/1", `{}`}, {"DELETE", "/applications/1", ""},
		{"POST", "/applications/1/screen", ""}, {"GET", "/applications/1/screening", ""},
		{"GET", "/stats/summary", ""}, {"GET", "/stats/applications-by-status", ""}, {"GET", "/stats/top-positions", ""},
		{"GET", "/search?q=go", ""}, {"POST", "/seed", ""},
	}
	for _, r := range routes {
		rec := do(h, r.method, r.path, r.body)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s = %d, want 503", r.method, r.path, rec.Code)
		}
	}
	if rec := do(h, "GET", "/readyz", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "not configured") {
		t.Errorf("GET /readyz = %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/version", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "version") {
		t.Errorf("GET /version = %d %s", rec.Code, rec.Body)
	}
}

// Bad requests are refused before the store is touched (stubStore would panic otherwise).
func TestBadRequestsNeverReachTheStore(t *testing.T) {
	h := handlerWithStub()
	cases := []struct {
		name, method, path, body string
		want                     int
	}{
		{"id is not a number", "GET", "/applicants/abc", "", 400},
		{"id is zero", "GET", "/positions/0", "", 400},
		{"negative id", "DELETE", "/applications/-3", "", 400},
		{"empty body", "POST", "/applicants", "", 400},
		{"malformed JSON", "POST", "/applicants", "{nope", 400},
		{"unknown field", "POST", "/applicants", `{"name":"A","email":"a@b.co","admin":true}`, 400},
		{"trailing data", "POST", "/applicants", `{"name":"A","email":"a@b.co"} {}`, 400},
		{"bad email", "POST", "/applicants", `{"name":"A","email":"x"}`, 400},
		{"missing position title", "POST", "/positions", `{"department":"Eng"}`, 400},
		{"application without ids", "POST", "/applications", `{}`, 400},
		{"unknown status in PATCH", "PATCH", "/applications/1", `{"status":"promoted"}`, 400},
		{"unknown status filter", "GET", "/applications?status=promoted", "", 400},
		{"bad position status filter", "GET", "/positions?status=paused", "", 400},
		{"limit too big", "GET", "/applicants?limit=1000", "", 400},
		{"limit not a number", "GET", "/applicants?limit=ten", "", 400},
		{"negative offset", "GET", "/applicants?offset=-1", "", 400},
		{"search term too short", "GET", "/search?q=a", "", 400},
		{"top positions limit", "GET", "/stats/top-positions?limit=0", "", 400},
	}
	for _, c := range cases {
		rec := do(h, c.method, c.path, c.body)
		if rec.Code != c.want {
			t.Errorf("%s: %s %s = %d (%s), want %d", c.name, c.method, c.path, rec.Code, rec.Body, c.want)
		}
	}
}

func TestUnknownRouteAndWrongMethod(t *testing.T) {
	h := handlerWithStub()
	if rec := do(h, "GET", "/nothing-here", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown route = %d, want 404", rec.Code)
	}
	if rec := do(h, "PUT", "/applications", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("wrong method = %d, want 405", rec.Code)
	}
}

func TestCORSAllowsAllMethodsTheAPIUses(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/applicants/1", nil)
	rec := httptest.NewRecorder()
	handlerWithStub().ServeHTTP(rec, req)
	got := rec.Header().Get("Access-Control-Allow-Methods")
	for _, m := range []string{"PUT", "PATCH", "DELETE"} {
		if !strings.Contains(got, m) {
			t.Errorf("CORS methods %q lack %s", got, m)
		}
	}
}
