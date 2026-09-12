package persistence

import (
	"context"
	"time"
)

type EnrollmentStatus string

const (
	StatusUnenrolled EnrollmentStatus = "unenrolled"
	StatusEnrolled   EnrollmentStatus = "enrolled"
)

// Enrollment is the most recent enrollment/config row - see
// migrations/V001.sql. A fresh row is inserted on every state transition
// (enroll, reset) rather than mutating one in place, so past enrollments
// stay auditable.
type Enrollment struct {
	ID              int64
	ApplianceID     *int64
	ApplianceSecret *string
	ServerURL       *string
	Hostname        *string
	Status          EnrollmentStatus
	EnrolledAt      *time.Time
	CreatedAt       time.Time
}

// EnrollmentState is a pending OAuth-style "state" value issued by
// enrollment/start, redeemed once by enrollment/complete.
type EnrollmentState struct {
	State     string
	ReturnTo  string
	ExpiresAt time.Time
}

// CloudConnectClientDB is the full persistence surface this service needs.
type CloudConnectClientDB interface {
	// GetCurrentEnrollment returns the most recently inserted Enrollment
	// row, or the zero-value unenrolled Enrollment if none exists yet.
	GetCurrentEnrollment(ctx context.Context) (Enrollment, error)
	// InsertEnrolled inserts a fresh 'enrolled' row, becoming the new
	// current enrollment.
	InsertEnrolled(ctx context.Context, applianceID int64, applianceSecret string, serverURL string, hostname string) (Enrollment, error)
	// InsertUnenrolled inserts a fresh 'unenrolled' row, becoming the new
	// current enrollment - used by enrollment/reset.
	InsertUnenrolled(ctx context.Context) (Enrollment, error)

	// SaveEnrollmentState replaces any existing state row with the same
	// value - state values are generated random enough that a collision is
	// not expected, so this is really an insert.
	SaveEnrollmentState(ctx context.Context, state string, returnTo string, expiresAt time.Time) error
	GetEnrollmentState(ctx context.Context, state string) (EnrollmentState, error)
	DeleteEnrollmentState(ctx context.Context, state string) error
}
