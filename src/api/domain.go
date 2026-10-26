package main

import (
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// ---- resources ---------------------------------------------------------------------------

type applicant struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Phone     string    `json:"phone"`
	Skills    []string  `json:"skills"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type applicantInput struct {
	Name   string   `json:"name"`
	Email  string   `json:"email"`
	Phone  string   `json:"phone"`
	Skills []string `json:"skills"`
}

type position struct {
	ID         int64     `json:"id"`
	Title      string    `json:"title"`
	Department string    `json:"department"`
	Location   string    `json:"location"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type positionInput struct {
	Title      string `json:"title"`
	Department string `json:"department"`
	Location   string `json:"location"`
	Status     string `json:"status"`
}

type application struct {
	ID                   int64      `json:"id"`
	ApplicantID          int64      `json:"applicantId"`
	PositionID           int64      `json:"positionId"`
	Status               string     `json:"status"`
	ScreeningStatus      string     `json:"screeningStatus"`
	ScreeningScore       *int       `json:"screeningScore"`
	ScreeningRequestedAt *time.Time `json:"screeningRequestedAt"`
	ScreeningDoneAt      *time.Time `json:"screeningDoneAt"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

type applicationInput struct {
	ApplicantID int64 `json:"applicantId"`
	PositionID  int64 `json:"positionId"`
}

type statusInput struct {
	Status string `json:"status"`
}

// page is the envelope of every list endpoint.
type page[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type summary struct {
	Applicants      int `json:"applicants"`
	PositionsOpen   int `json:"positionsOpen"`
	PositionsClosed int `json:"positionsClosed"`
	Applications    int `json:"applications"`
	ScreeningQueued int `json:"screeningQueued"`
	ScreeningDone   int `json:"screeningDone"`
}

type topPosition struct {
	PositionID   int64  `json:"positionId"`
	Title        string `json:"title"`
	Applications int    `json:"applications"`
}

type searchResult struct {
	Applicants []applicant `json:"applicants"`
	Positions  []position  `json:"positions"`
}

type seedRequest struct {
	Applicants   int `json:"applicants"`
	Positions    int `json:"positions"`
	Applications int `json:"applications"`
}

type seedResult struct {
	Applicants   int `json:"applicants"`
	Positions    int `json:"positions"`
	Applications int `json:"applications"`
}

// ---- errors ------------------------------------------------------------------------------

var (
	errNotFound   = errors.New("not found")
	errConflict   = errors.New("conflict")
	errInvalidRef = errors.New("referenced applicant or position does not exist")
	errBadState   = errors.New("operation not allowed in the current state")
)

// validationError carries the field that failed so the client can show it.
type validationError struct{ field, msg string }

func (e validationError) Error() string { return e.field + ": " + e.msg }

// transitionError reports a status change the workflow does not allow.
type transitionError struct {
	from, to string
	allowed  []string
}

func (e transitionError) Error() string {
	return fmt.Sprintf("cannot change status from %q to %q (allowed: %s)", e.from, e.to, strings.Join(e.allowed, ", "))
}

// ---- workflow ----------------------------------------------------------------------------

// transitions is the application workflow. hired, rejected and withdrawn are final.
var transitions = map[string][]string{
	"applied":   {"screening", "rejected", "withdrawn"},
	"screening": {"interview", "rejected", "withdrawn"},
	"interview": {"offer", "rejected", "withdrawn"},
	"offer":     {"hired", "rejected", "withdrawn"},
	"hired":     nil,
	"rejected":  nil,
	"withdrawn": nil,
}

// applicationStatuses lists every status, in workflow order, for validation and reports.
var applicationStatuses = []string{"applied", "screening", "interview", "offer", "hired", "rejected", "withdrawn"}

func isApplicationStatus(s string) bool { return slices.Contains(applicationStatuses, s) }

func checkTransition(from, to string) error {
	if !isApplicationStatus(to) {
		return validationError{"status", "must be one of " + strings.Join(applicationStatuses, ", ")}
	}
	if !slices.Contains(transitions[from], to) {
		return transitionError{from: from, to: to, allowed: transitions[from]}
	}
	return nil
}

// ---- validation --------------------------------------------------------------------------

func checkLen(field, v string, min, max int) error {
	n := utf8.RuneCountInString(strings.TrimSpace(v))
	if n < min || n > max {
		if min > 0 {
			return validationError{field, fmt.Sprintf("must be %d-%d characters", min, max)}
		}
		return validationError{field, fmt.Sprintf("must be at most %d characters", max)}
	}
	return nil
}

func (in *applicantInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Phone = strings.TrimSpace(in.Phone)
	if err := checkLen("name", in.Name, 1, 100); err != nil {
		return err
	}
	if err := checkLen("email", in.Email, 3, 200); err != nil {
		return err
	}
	if addr, err := mail.ParseAddress(in.Email); err != nil || addr.Address != in.Email {
		return validationError{"email", "must be a valid email address"}
	}
	if err := checkLen("phone", in.Phone, 0, 30); err != nil {
		return err
	}
	if len(in.Skills) > 20 {
		return validationError{"skills", "at most 20 skills"}
	}
	if in.Skills == nil {
		in.Skills = []string{}
	}
	for i, s := range in.Skills {
		in.Skills[i] = strings.TrimSpace(s)
		if err := checkLen("skills", in.Skills[i], 1, 40); err != nil {
			return err
		}
	}
	return nil
}

func (in *positionInput) validate() error {
	in.Title = strings.TrimSpace(in.Title)
	in.Department = strings.TrimSpace(in.Department)
	in.Location = strings.TrimSpace(in.Location)
	if in.Status == "" {
		in.Status = "open"
	}
	if err := checkLen("title", in.Title, 1, 120); err != nil {
		return err
	}
	if err := checkLen("department", in.Department, 1, 60); err != nil {
		return err
	}
	if err := checkLen("location", in.Location, 0, 80); err != nil {
		return err
	}
	if in.Status != "open" && in.Status != "closed" {
		return validationError{"status", "must be open or closed"}
	}
	return nil
}

func (in applicationInput) validate() error {
	if in.ApplicantID <= 0 {
		return validationError{"applicantId", "is required"}
	}
	if in.PositionID <= 0 {
		return validationError{"positionId", "is required"}
	}
	return nil
}

// clamp limits a requested count to [lo, hi], using def when it is zero.
func clamp(v, def, lo, hi int) int {
	if v == 0 {
		v = def
	}
	return min(max(v, lo), hi)
}

func (r *seedRequest) normalize() {
	r.Applicants = clamp(r.Applicants, 20, 0, 500)
	r.Positions = clamp(r.Positions, 5, 0, 100)
	r.Applications = clamp(r.Applications, 40, 0, 2000)
}
