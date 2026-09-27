package cloudconnectwebapp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"sync"
	"time"

	"github.com/Kaese72/authentication/usertoken"
	"github.com/Kaese72/cloud-connect/client/internal/applianceregistry"
	"github.com/Kaese72/cloud-connect/client/internal/logging"
	"github.com/Kaese72/cloud-connect/client/internal/persistence"
	"github.com/Kaese72/cloud-connect/client/internal/tunnel"
	"github.com/Kaese72/cloud-connect/client/restmodels"
	"github.com/danielgtaylor/huma/v2"
)

// requireCloudConnectView/requireCloudConnectModify return a huma error
// unless the caller (as put in ctx by usertoken.Middleware) may view/modify
// cloud connectivity. Every endpoint in this file calls one of the two first;
// admins always pass.
func requireCloudConnectView(ctx context.Context) error {
	permissions, ok := usertoken.PermissionsFromContext(ctx)
	if !ok || !permissions.HasView(usertoken.ResourceCloudConnect) {
		return huma.Error403Forbidden("view access to cloud connect is required")
	}
	return nil
}

func requireCloudConnectModify(ctx context.Context) error {
	permissions, ok := usertoken.PermissionsFromContext(ctx)
	if !ok || !permissions.HasModify(usertoken.ResourceCloudConnect) {
		return huma.Error403Forbidden("modify access to cloud connect is required")
	}
	return nil
}

// stateExpiry bounds how long a browser has to complete the cloud-side leg
// of enrollment (login + redirect back) before the state this service
// handed out becomes unredeemable - see the README's "Enrollment" section.
const stateExpiry = 5 * time.Minute

type webApp struct {
	persistence       persistence.CloudConnectClientDB
	applianceRegistry applianceregistry.Client
	tunnel            *tunnel.Supervisor
	cloudEnrollURL    string
	// rotateMu is a pointer because webApp is passed around by value.
	rotateMu *sync.Mutex
}

func NewWebApp(p persistence.CloudConnectClientDB, applianceRegistry applianceregistry.Client, supervisor *tunnel.Supervisor, cloudEnrollURL string) webApp {
	return webApp{
		persistence:       p,
		applianceRegistry: applianceRegistry,
		tunnel:            supervisor,
		cloudEnrollURL:    cloudEnrollURL,
		rotateMu:          &sync.Mutex{},
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
	if err := requireCloudConnectView(ctx); err != nil {
		return nil, err
	}
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
	if err := requireCloudConnectModify(ctx); err != nil {
		return nil, err
	}
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
	if err := requireCloudConnectModify(ctx); err != nil {
		return nil, err
	}
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

// persistRotatedAttempts is how often a freshly rotated secret is written to
// the database before giving up. The registry has already invalidated the old
// secret by then, so a transient DB error must not strand the appliance.
const persistRotatedAttempts = 3

// RotateSecret replaces this appliance's cloud secret by asking
// appliance-registry to issue a new one (authenticating with the current
// one), then persists it and reconnects the tunnel with it - see the
// README's "Rotating the appliance secret".
func (app webApp) RotateSecret(ctx context.Context, input *struct{}) (*struct {
	Body restmodels.StatusResponse
}, error) {
	if err := requireCloudConnectModify(ctx); err != nil {
		return nil, err
	}
	// One rotation at a time: a second concurrent call would present the
	// secret the first has just invalidated.
	app.rotateMu.Lock()
	defer app.rotateMu.Unlock()

	enrollment, err := app.persistence.GetCurrentEnrollment(ctx)
	if err != nil {
		logging.ErrorErr(err, ctx)
		return nil, huma.Error500InternalServerError("failed to look up enrollment state")
	}
	if enrollment.Status != persistence.StatusEnrolled || enrollment.ApplianceID == nil || enrollment.ApplianceSecret == nil {
		return nil, huma.Error409Conflict("appliance is not enrolled with the cloud")
	}

	rotated, err := app.applianceRegistry.RotateSecret(ctx, *enrollment.ApplianceID, *enrollment.ApplianceSecret)
	if err != nil {
		if applianceregistry.IsRefusal(err) {
			// The registry no longer accepts our secret (revoked, or rotated
			// from the cloud side), so there is nothing to rotate from.
			return nil, huma.Error409Conflict("appliance-registry no longer accepts this appliance's secret; disconnect and enroll again")
		}
		logging.ErrorErr(err, ctx)
		return nil, huma.Error502BadGateway("failed to rotate the secret with appliance-registry")
	}

	// Detached from the request: once the registry has answered, finishing
	// the write matters more than the caller staying connected.
	persistCtx := context.WithoutCancel(ctx)
	var saved persistence.Enrollment
	for attempt := 1; attempt <= persistRotatedAttempts; attempt++ {
		saved, err = app.persistence.InsertEnrolled(persistCtx, *enrollment.ApplianceID, rotated.ApplianceSecret, rotated.TunnelURL, rotated.Hostname)
		if err == nil {
			break
		}
		logging.ErrorErr(err, ctx)
		time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("the secret was rotated but could not be saved; disconnect and enroll again")
	}

	// Start is a no-op while running, so restart to pick up the new secret.
	app.tunnel.Stop()
	app.tunnel.Start(rotated.ApplianceSecret, rotated.TunnelURL)

	return &struct {
		Body restmodels.StatusResponse
	}{Body: app.toStatusResponse(saved)}, nil
}

// ResetEnrollment de-enrolls the appliance: stops the tunnel and returns to
// the pre-enrollment state, ready to enroll again.
func (app webApp) ResetEnrollment(ctx context.Context, input *struct{}) (*struct {
	Body restmodels.StatusResponse
}, error) {
	if err := requireCloudConnectModify(ctx); err != nil {
		return nil, err
	}
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
