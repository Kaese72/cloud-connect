package cloudloginwebapp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Kaese72/cloud-connect/client/internal/applianceregistry"
	"github.com/Kaese72/cloud-connect/client/internal/persistence"
	"github.com/Kaese72/cloud-connect/client/restmodels"
	"github.com/danielgtaylor/huma/v2"
)

type fakeDB struct {
	persistence.CloudConnectClientDB
	enrollment persistence.Enrollment
}

func (d fakeDB) GetCurrentEnrollment(context.Context) (persistence.Enrollment, error) {
	return d.enrollment, nil
}

func enrolled() persistence.Enrollment {
	id, secret := int64(5), "appliance-5:s3cret"
	return persistence.Enrollment{Status: persistence.StatusEnrolled, ApplianceID: &id, ApplianceSecret: &secret}
}

func statusOf(err error) int {
	var se huma.StatusError
	if errors.As(err, &se) {
		return se.GetStatus()
	}
	return 0
}

// registry starts a fake appliance-registry that answers every request with
// status/body and records the last request's Authorization header.
func registry(t *testing.T, status int, body string, gotAuth *string) applianceregistry.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotAuth != nil {
			*gotAuth = r.Header.Get("Authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return applianceregistry.NewClient(srv.URL)
}

const token = "Bearer internal-token"

type accessInput = struct {
	Authorization string `header:"Authorization"`
	CloudUserID   int64  `path:"cloudUserId"`
}

func TestCheckAccessAuthenticatesAsTheApplianceAndMapsAnswers(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantAllowed bool
		wantErr     int
	}{
		{"allowed", 200, `{"allowed":true}`, true, 0},
		{"denied", 200, `{"allowed":false}`, false, 0},
		// The registry no longer accepts this appliance's secret (rotated or
		// revoked): that is a denial, not "unknown".
		{"stale secret", 401, `{"detail":"invalid appliance secret"}`, false, 0},
		{"appliance gone", 404, `{}`, false, 0},
		// Failing to get an answer must stay distinguishable from "no".
		{"registry error", 500, `boom`, false, http.StatusBadGateway},
		{"registry unavailable", 503, `boom`, false, http.StatusBadGateway},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotAuth string
			app := NewWebApp(fakeDB{enrollment: enrolled()}, registry(t, tc.status, tc.body, &gotAuth), "https://cloud/appliance-login", []string{"internal-token"})
			res, err := app.CheckAccess(context.Background(), &accessInput{Authorization: token, CloudUserID: 7})
			if tc.wantErr != 0 {
				if statusOf(err) != tc.wantErr {
					t.Fatalf("expected %d, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if res.Body.Allowed != tc.wantAllowed {
				t.Errorf("allowed = %v, want %v", res.Body.Allowed, tc.wantAllowed)
			}
			if gotAuth != "Bearer appliance-5:s3cret" {
				t.Errorf("the registry must be called with the appliance secret, got %q", gotAuth)
			}
		})
	}
}

func TestCheckAccessUnreachableRegistryIsNotDenied(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	registryURL := srv.URL
	srv.Close() // nothing is listening any more
	app := NewWebApp(fakeDB{enrollment: enrolled()}, applianceregistry.NewClient(registryURL), "https://cloud/appliance-login", []string{"internal-token"})

	_, err := app.CheckAccess(context.Background(), &accessInput{Authorization: token, CloudUserID: 7})
	if statusOf(err) != http.StatusBadGateway {
		t.Fatalf("expected 502 when the registry cannot be reached, got %v", err)
	}
}

func TestEndpointsRequireServiceToken(t *testing.T) {
	app := NewWebApp(fakeDB{enrollment: enrolled()}, registry(t, 200, `{"allowed":true}`, nil), "https://cloud/appliance-login", []string{"internal-token"})
	for _, header := range []string{"", "Bearer wrong", "internal-token"} {
		if _, err := app.CheckAccess(context.Background(), &accessInput{Authorization: header, CloudUserID: 7}); statusOf(err) != http.StatusUnauthorized {
			t.Errorf("CheckAccess with %q: expected 401, got %v", header, err)
		}
	}
}

func TestNotEnrolledIsConflictExceptForStatus(t *testing.T) {
	app := NewWebApp(fakeDB{enrollment: persistence.Enrollment{Status: persistence.StatusUnenrolled}}, registry(t, 200, `{}`, nil), "https://cloud/appliance-login", []string{"internal-token"})

	if _, err := app.CheckAccess(context.Background(), &accessInput{Authorization: token, CloudUserID: 7}); statusOf(err) != http.StatusConflict {
		t.Errorf("CheckAccess: expected 409, got %v", err)
	}
	res, err := app.GetStatus(context.Background(), &struct {
		Authorization string `header:"Authorization"`
	}{Authorization: token})
	if err != nil || res.Body.Available {
		t.Errorf("status must report unavailable rather than fail, got %+v %v", res, err)
	}
}

func TestStartBuildsCloudURLWithApplianceAndState(t *testing.T) {
	app := NewWebApp(fakeDB{enrollment: enrolled()}, registry(t, 200, `{}`, nil), "https://cloud.example/appliance-login", []string{"internal-token"})
	res, err := app.Start(context.Background(), &struct {
		Authorization string `header:"Authorization"`
		Body          restmodels.CloudLoginStartRequest
	}{Authorization: token, Body: restmodels.CloudLoginStartRequest{State: "abc123", ReturnTo: "https://app.local/cloud-login/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	got := res.Body.CloudURL
	for _, want := range []string{"https://cloud.example/appliance-login?", "state=abc123", "applianceId=5", "return_to=https%3A%2F%2Fapp.local%2Fcloud-login%2Fcallback"} {
		if !strings.Contains(got, want) {
			t.Errorf("cloud URL %q is missing %q", got, want)
		}
	}
}

func TestRedeemRefusalIsForbiddenAndOutageIsBadGateway(t *testing.T) {
	body := &struct {
		Authorization string `header:"Authorization"`
		Body          restmodels.CloudLoginRedeemRequest
	}{Authorization: token, Body: restmodels.CloudLoginRedeemRequest{Code: "c"}}

	refused := NewWebApp(fakeDB{enrollment: enrolled()}, registry(t, 401, `{}`, nil), "https://cloud/appliance-login", []string{"internal-token"})
	if _, err := refused.Redeem(context.Background(), body); statusOf(err) != http.StatusForbidden {
		t.Errorf("expected 403 when the registry refuses the code, got %v", err)
	}

	broken := NewWebApp(fakeDB{enrollment: enrolled()}, registry(t, 500, `{}`, nil), "https://cloud/appliance-login", []string{"internal-token"})
	if _, err := broken.Redeem(context.Background(), body); statusOf(err) != http.StatusBadGateway {
		t.Errorf("expected 502 when the registry fails, got %v", err)
	}

	ok := NewWebApp(fakeDB{enrollment: enrolled()}, registry(t, 200, `{"id":7,"username":"alice","name":"Alice","surname":"A","email":"a@example.com"}`, nil), "https://cloud/appliance-login", []string{"internal-token"})
	res, err := ok.Redeem(context.Background(), body)
	if err != nil || res.Body.ID != 7 || res.Body.Username != "alice" {
		t.Errorf("expected alice, got %+v %v", res, err)
	}
}
