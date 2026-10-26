package main

import "context"

type applicantFilter struct {
	Q             string
	Limit, Offset int
}

type positionFilter struct {
	Department, Status string
	Limit, Offset      int
}

type applicationFilter struct {
	Status                  string
	ApplicantID, PositionID int64
	Limit, Offset           int
}

// store is everything the HTTP handlers need from the database. The Postgres implementation is in
// store_pg.go; handlers never see SQL, which keeps them testable.
type store interface {
	CreateApplicant(ctx context.Context, in applicantInput) (applicant, error)
	GetApplicant(ctx context.Context, id int64) (applicant, error)
	ListApplicants(ctx context.Context, f applicantFilter) ([]applicant, int, error)
	UpdateApplicant(ctx context.Context, id int64, in applicantInput) (applicant, error)
	DeleteApplicant(ctx context.Context, id int64) error

	CreatePosition(ctx context.Context, in positionInput) (position, error)
	GetPosition(ctx context.Context, id int64) (position, error)
	ListPositions(ctx context.Context, f positionFilter) ([]position, int, error)
	UpdatePosition(ctx context.Context, id int64, in positionInput) (position, error)
	DeletePosition(ctx context.Context, id int64) error
	ClosePosition(ctx context.Context, id int64) (position, error)

	CreateApplication(ctx context.Context, in applicationInput) (application, error)
	GetApplication(ctx context.Context, id int64) (application, error)
	ListApplications(ctx context.Context, f applicationFilter) ([]application, int, error)
	SetApplicationStatus(ctx context.Context, id int64, to string) (application, error)
	DeleteApplication(ctx context.Context, id int64) error
	// RequestScreening marks the screening as queued; CancelScreening undoes it when the queue
	// message could not be sent.
	RequestScreening(ctx context.Context, id int64) (application, error)
	CancelScreening(ctx context.Context, id int64) error

	Summary(ctx context.Context) (summary, error)
	CountByStatus(ctx context.Context) (map[string]int, error)
	TopPositions(ctx context.Context, limit int) ([]topPosition, error)
	Search(ctx context.Context, q string, limit int) (searchResult, error)
	Seed(ctx context.Context, req seedRequest) (seedResult, error)
	Ping(ctx context.Context) error
}
