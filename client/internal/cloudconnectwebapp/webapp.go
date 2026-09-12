package cloudconnectwebapp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"time"

	"github.com/Kaese72/cloud-connect/client/internal/applianceregistry"
	"github.com/Kaese72/cloud-connect/client/internal/logging"
	"github.com/Kaese72/cloud-connect/client/internal/persistence"
	"github.com/Kaese72/cloud-connect/client/internal/tunnel"
	"github.com/Kaese72/cloud-connect/client/restmodels"
	"github.com/danielgtaylor/huma/v2"
)

// stateExpiry bounds how long a browser has to complete the cloud-side leg
// of enrollment (login + redirect back) before the state this service
// handed out becomes unredeemable - see the README's "Enrollment" section.
const stateExpiry = 5 * time.Minute

type webApp struct {
	persistence       persistence.CloudConnectClientDB
	applianceRegistry applianceregistry.Client
	tunnel            *tunnel.Supervisor
	cloudEnrollURL    string
}

func NewWebApp(p persistence.CloudConnectClientDB, applianceRegistry applianceregistry.Client, supervisor *tunnel.Supervisor, cloudEnrollURL string) webApp {
	return webApp{
		persistence:       p,
		applianceRegistry: applianceRegistry,
		tunnel:            supervisor,
		cloudEnrollURL:    cloudEnrollURL,
	}
}

// ResumeIfEnrolled starts the tunnel supervisor if the persisted state is
// already 'enrolled' - called once at boot so a pod restart reconnects the
// tunnel automatically without the human re-running enrollment.
func (app webApp) ResumeIfEnrolled(ctx context.Context) error {
	enrollment, err := app.persistence.GetCurrentEnrollment(ctx)
	if err != nil {
		return err
	}
	if enrollment.Status == persistence.StatusEnrolled && enrollment.ApplianceSecret != nil && enrollment.ServerURL != nil {
		app.tunnel.Start(*enrollment.ApplianceSecret, *enrollment.ServerURL)
	}
	return nil
}

func generateState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (app webApp) toStatusResponse(e persistence.Enrollment) restmodels.StatusResponse {
	return restmodels.StatusResponse{
		Status:          string(e.Status),
		Hostname:        e.Hostname,
		TunnelConnected: app.tunnel.Connected(),
	}
}

// GetStatus reports the current enrollment state and live tunnel
// connectivity - see the README's "Enrollment" section.
func (app webApp) GetStatus(ctx context.Context, input *struct{}) (*struct {
	Body restmodels.StatusResponse
}, error) {
	enrollment, err := app.persistence.GetCurrentEnrollment(ctx)
	if err != nil {
		logging.ErrorErr(err, ctx)
		return nil, huma.Error500InternalServerError("failed to look up enrollment state")
	}
	return &struct {
		Body restmodels.StatusResponse
	}{Body: app.toStatusResponse(enrollment)}, nil
}

// StartEnrollment begins the OAuth-authorization-code-style enrollment flow
// - see the README. It hands back a cloud URL for the browser to navigate
// to; the local UI is expected to redirect the browser there immediately.
func (app webApp) StartEnrollment(ctx context.Context, input *struct {
	Body restmodels.EnrollmentStartRequest
}) (*struct {
	Body restmodels.EnrollmentStartResponse
}, error) {
	state, err := generateState()
	if err != nil {
		logging.ErrorErr(err, ctx)
		return nil, huma.Error500InternalServerError("failed to generate enrollment state")
	}
	expiresAt := time.Now().Add(stateExpiry)
	if err := app.persistence.SaveEnrollmentState(ctx, state, input.Body.ReturnTo, expiresAt); err != nil {
		logging.ErrorErr(err, ctx)
		return nil, huma.Error500InternalServerError("failed to save enrollment state")
	}

	cloudURL := app.cloudEnrollURL + "?state=" + url.QueryEscape(state) + "&return_to=" + url.QueryEscape(input.Body.ReturnTo)

	return &struct {
		Body restmodels.EnrollmentStartResponse
	}{Body: restmodels.EnrollmentStartResponse{CloudURL: cloudURL}}, nil
}

// CompleteEnrollment is called by the local UI's callback page once the
// browser has been redirected back from the cloud with a code+state pair -
// see the README. It validates the state, redeems the code against
// appliance-registry server-to-server, persists the result, and starts the
// tunnel.
func (app webApp) CompleteEnrollment(ctx context.Context, input *struct {
	Body restmodels.EnrollmentCompleteRequest
}) (*struct {
	Body restmodels.StatusResponse
}, error) {
	stored, err := app.persistence.GetEnrollmentState(ctx, input.Body.State)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, huma.Error401Unauthorized("invalid or expired enrollment state")
		}
		logging.ErrorErr(err, ctx)
		return nil, huma.Error500InternalServerError("failed to look up enrollment state")
	}
	// The state is single-use regardless of what happens next - a failed
	// redemption should not leave a replayable state lying around.
	if err := app.persistence.DeleteEnrollmentState(ctx, input.Body.State); err != nil {
		logging.ErrorErr(err, ctx)
	}
	if time.Now().After(stored.ExpiresAt) {
		return nil, huma.Error401Unauthorized("invalid or expired enrollment state")
	}

	redeemed, err := app.applianceRegistry.Redeem(ctx, input.Body.ApplianceID, input.Body.Code)
	if err != nil {
		logging.ErrorErr(err, ctx)
		return nil, huma.Error502BadGateway("failed to redeem enrollment code with appliance-registry")
	}

	enrollment, err := app.persistence.InsertEnrolled(ctx, input.Body.ApplianceID, redeemed.ApplianceSecret, redeemed.TunnelURL, redeemed.Hostname)
	if err != nil {
		logging.ErrorErr(err, ctx)
		return nil, huma.Error500InternalServerError("failed to persist enrollment")
	}

	app.tunnel.Start(redeemed.ApplianceSecret, redeemed.TunnelURL)

	return &struct {
		Body restmodels.StatusResponse
	}{Body: app.toStatusResponse(enrollment)}, nil
}

// ResetEnrollment de-enrolls the appliance: stops the tunnel and returns to
// the pre-enrollment state, ready to enroll again.
func (app webApp) ResetEnrollment(ctx context.Context, input *struct{}) (*struct {
	Body restmodels.StatusResponse
}, error) {
	app.tunnel.Stop()
	enrollment, err := app.persistence.InsertUnenrolled(ctx)
	if err != nil {
		logging.ErrorErr(err, ctx)
		return nil, huma.Error500InternalServerError("failed to reset enrollment")
	}
	return &struct {
		Body restmodels.StatusResponse
	}{Body: app.toStatusResponse(enrollment)}, nil
}
