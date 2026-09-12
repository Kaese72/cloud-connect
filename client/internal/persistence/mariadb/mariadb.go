package mariadb

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Kaese72/cloud-connect/client/internal/config"
	"github.com/Kaese72/cloud-connect/client/internal/logging"
	"github.com/Kaese72/cloud-connect/client/internal/persistence"
	"go.elastic.co/apm/module/apmsql"
)

var _ persistence.CloudConnectClientDB = mariadbPersistence{}

type mariadbPersistence struct {
	db *sql.DB
}

func NewMariadbPersistence(conf config.DatabaseConfig) (mariadbPersistence, error) {
	db, err := apmsql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&loc=UTC", conf.User, conf.Password, conf.Host, conf.Port, conf.Database))
	if err != nil {
		logging.Fatal(err.Error(), context.Background())
		return mariadbPersistence{}, err
	}
	return mariadbPersistence{db: db}, nil
}

const enrollmentColumns = `id, applianceId, applianceSecret, serverUrl, hostname, status, enrolledAt, createdAt`

func scanEnrollment(row interface{ Scan(...interface{}) error }) (persistence.Enrollment, error) {
	var e persistence.Enrollment
	var status string
	var applianceID sql.NullInt64
	var applianceSecret, serverURL, hostname sql.NullString
	var enrolledAt sql.NullTime
	if err := row.Scan(&e.ID, &applianceID, &applianceSecret, &serverURL, &hostname, &status, &enrolledAt, &e.CreatedAt); err != nil {
		return persistence.Enrollment{}, err
	}
	e.Status = persistence.EnrollmentStatus(status)
	if applianceID.Valid {
		e.ApplianceID = &applianceID.Int64
	}
	if applianceSecret.Valid {
		e.ApplianceSecret = &applianceSecret.String
	}
	if serverURL.Valid {
		e.ServerURL = &serverURL.String
	}
	if hostname.Valid {
		e.Hostname = &hostname.String
	}
	if enrolledAt.Valid {
		e.EnrolledAt = &enrolledAt.Time
	}
	return e, nil
}

func (m mariadbPersistence) GetCurrentEnrollment(ctx context.Context) (persistence.Enrollment, error) {
	row := m.db.QueryRowContext(ctx, `SELECT `+enrollmentColumns+` FROM enrollment ORDER BY id DESC LIMIT 1`)
	e, err := scanEnrollment(row)
	if err == sql.ErrNoRows {
		return persistence.Enrollment{Status: persistence.StatusUnenrolled}, nil
	}
	return e, err
}

func (m mariadbPersistence) InsertEnrolled(ctx context.Context, applianceID int64, applianceSecret string, serverURL string, hostname string) (persistence.Enrollment, error) {
	result, err := m.db.ExecContext(ctx, `
		INSERT INTO enrollment (applianceId, applianceSecret, serverUrl, hostname, status, enrolledAt)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		applianceID, applianceSecret, serverURL, hostname, persistence.StatusEnrolled)
	if err != nil {
		return persistence.Enrollment{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return persistence.Enrollment{}, err
	}
	row := m.db.QueryRowContext(ctx, `SELECT `+enrollmentColumns+` FROM enrollment WHERE id = ?`, id)
	return scanEnrollment(row)
}

func (m mariadbPersistence) InsertUnenrolled(ctx context.Context) (persistence.Enrollment, error) {
	result, err := m.db.ExecContext(ctx, `INSERT INTO enrollment (status) VALUES (?)`, persistence.StatusUnenrolled)
	if err != nil {
		return persistence.Enrollment{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return persistence.Enrollment{}, err
	}
	row := m.db.QueryRowContext(ctx, `SELECT `+enrollmentColumns+` FROM enrollment WHERE id = ?`, id)
	return scanEnrollment(row)
}

func (m mariadbPersistence) SaveEnrollmentState(ctx context.Context, state string, returnTo string, expiresAt time.Time) error {
	_, err := m.db.ExecContext(ctx, `
		INSERT INTO enrollmentStates (state, returnTo, expiresAt) VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE returnTo = VALUES(returnTo), expiresAt = VALUES(expiresAt), createdAt = CURRENT_TIMESTAMP`,
		state, returnTo, expiresAt)
	return err
}

func (m mariadbPersistence) GetEnrollmentState(ctx context.Context, state string) (persistence.EnrollmentState, error) {
	row := m.db.QueryRowContext(ctx, `SELECT state, returnTo, expiresAt FROM enrollmentStates WHERE state = ?`, state)
	var s persistence.EnrollmentState
	if err := row.Scan(&s.State, &s.ReturnTo, &s.ExpiresAt); err != nil {
		return persistence.EnrollmentState{}, err
	}
	return s, nil
}

func (m mariadbPersistence) DeleteEnrollmentState(ctx context.Context, state string) error {
	_, err := m.db.ExecContext(ctx, `DELETE FROM enrollmentStates WHERE state = ?`, state)
	return err
}
