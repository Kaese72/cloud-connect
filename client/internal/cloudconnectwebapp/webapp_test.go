package cloudconnectwebapp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Kaese72/authentication/usertoken"
	"github.com/Kaese72/cloud-connect/client/internal/applianceregistry"
	"github.com/Kaese72/cloud-connect/client/internal/persistence"
	"github.com/Kaese72/cloud-connect/client/internal/tunnel"
	"github.com/danielgtaylor/huma/v2"
)

// adminCtx is a background context carrying admin permissions, as
// usertoken.Middleware would have placed them from a valid token - these
// tests call webApp methods directly, bypassing HTTP and Middleware.
func adminCtx() context.Context {
	return usertoken.ContextWithAuth(context.Background(), 1, usertoken.AdminPermissions())
}

type fakeDB struct {
	persistence.CloudConnectClientDB
	enrollment persistence.Enrollment
	// insertFailures is how many InsertEnrolled calls fail before one succeeds.
	insertFailures int
	inserts        int
}

func (d *fakeDB) GetCurrentEnrollment(context.Context) (persistence.Enrollment, error) {
	return d.enrollment, nil
}

func (d *fakeDB) InsertEnrolled(_ context.Context, applianceID int64, secret string, serverURL string, hostname string) (persistence.Enrollment, error) {
	d.inserts++
	if d.inserts <= d.insertFailures {
		return persistence.Enrollment{}, errors.New("db unavailable")
	}
	d.enrollment = persistence.Enrollment{
		Status: persistence.StatusEnrolled, ApplianceID: &applianceID, ApplianceSecret: &secret, ServerURL: &serverURL, Hostname: &hostname,
	}
	return d.enrollment, nil
}

func enrolled() persistence.Enrollment {
	id, secret, url, host := int64(5), "appliance-5:old", "https://old.example", "old.example"
	return persistence.Enrollment{Status: persistence.StatusEnrolled, ApplianceID: &id, ApplianceSecret: &secret, ServerURL: &url, Hostname: &host}
}

func statusOf(err error) int {
	var se huma.StatusError
	if errors.As(err, &se) {
		return se.GetStatus()
	}
	return 0
}

// registry starts a fake appliance-registry answering every request with
// status/body, recording the last request's method, path and Authorization.
func registry(t *testing.T, status int, body string, got *http.Request) applianceregistry.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got != nil {
			*got = *r.Clone(context.Background())
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return applianceregistry.NewClient(srv.URL)
}

const rotatedBody = `{"applianceSecret":"appliance-5:new","hostname":"new.example","tunnelUrl":"https://tunnel.new.example"}`

func newApp(t *testing.T, db *fakeDB, reg applianceregistry.Client) webApp {
	t.Helper()
	supervisor := tunnel.NewSupervisor()
	t.Cleanup(supervisor.Stop)
	return NewWebApp(db, reg, supervisor, "https://cloud/enroll")
}

func TestRotateSecretAuthenticatesWithTheCurrentSecretAndPersistsTheNewOne(t *testing.T) {
	db := &fakeDB{enrollment: enrolled()}
	var got http.Request
	app := newApp(t, db, registry(t, 200, rotatedBody, &got))

	res, err := app.RotateSecret(adminCtx(), &struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodPost || got.URL.Path != "/appliance-registry/v0/appliances/5/secret/rotate" {
		t.Fatalf("unexpected registry request: %s %s", got.Method, got.URL.Path)
	}
	if got.Header.Get("Authorization") != "Bearer appliance-5:old" {
		t.Fatalf("must authenticate with the current secret, got %q", got.Header.Get("Authorization"))
	}
	if s := db.enrollment.ApplianceSecret; s == nil || *s != "appliance-5:new" {
		t.Fatalf("new secret not persisted: %+v", db.enrollment)
	}
	if *db.enrollment.ServerURL != "https://tunnel.new.example" || *db.enrollment.Hostname != "new.example" {
		t.Fatalf("tunnel URL/hostname should follow the registry's answer: %+v", db.enrollment)
	}
	if res.Body.Status != string(persistence.StatusEnrolled) || res.Body.Hostname == nil || *res.Body.Hostname != "new.example" {
		t.Fatalf("unexpected status response: %+v", res.Body)
	}
}

func TestRotateSecretRequiresEnrollment(t *testing.T) {
	db := &fakeDB{enrollment: persistence.Enrollment{Status: persistence.StatusUnenrolled}}
	var got http.Request
	app := newApp(t, db, registry(t, 200, rotatedBody, &got))

	_, err := app.RotateSecret(adminCtx(), &struct{}{})
	if statusOf(err) != http.StatusConflict {
		t.Fatalf("expected 409, got %v", err)
	}
	if got.Method != "" {
		t.Fatal("must not call the registry when not enrolled")
	}
}

func TestRotateSecretKeepsTheCurrentSecretWhenTheRegistryFails(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		wantErr int
	}{
		// The registry no longer accepts the secret: nothing to rotate from.
		{"refused", 401, http.StatusConflict},
		{"registry error", 500, http.StatusBadGateway},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeDB{enrollment: enrolled()}
			app := newApp(t, db, registry(t, tc.status, `{}`, nil))

			_, err := app.RotateSecret(adminCtx(), &struct{}{})
			if statusOf(err) != tc.wantErr {
				t.Fatalf("expected %d, got %v", tc.wantErr, err)
			}
			if db.inserts != 0 || *db.enrollment.ApplianceSecret != "appliance-5:old" {
				t.Fatal("a failed rotation must leave the stored secret alone")
			}
		})
	}
}

// The registry invalidates the old secret the moment it answers, so a
// transient database error must be retried rather than stranding the appliance.
func TestRotateSecretRetriesPersistingTheNewSecret(t *testing.T) {
	db := &fakeDB{enrollment: enrolled(), insertFailures: 2}
	app := newApp(t, db, registry(t, 200, rotatedBody, nil))

	if _, err := app.RotateSecret(adminCtx(), &struct{}{}); err != nil {
		t.Fatal(err)
	}
	if db.inserts != 3 || *db.enrollment.ApplianceSecret != "appliance-5:new" {
		t.Fatalf("expected the third attempt to save the new secret, got %d inserts, %+v", db.inserts, db.enrollment)
	}
}

func TestRotateSecretReportsAnUnsavedSecret(t *testing.T) {
	db := &fakeDB{enrollment: enrolled(), insertFailures: persistRotatedAttempts}
	app := newApp(t, db, registry(t, 200, rotatedBody, nil))

	_, err := app.RotateSecret(adminCtx(), &struct{}{})
	if statusOf(err) != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %v", err)
	}
}
