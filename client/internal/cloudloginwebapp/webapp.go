// Package cloudloginwebapp is the internal API the appliance's authentication
// service uses to offer "log in with Humi Cloud". Its router is authenticated
// by huemie-lib/k8sauth.RequireServiceAccount (see main.go), which verifies
// the caller is really authentication's own Kubernetes ServiceAccount via the
// TokenReview API, rather than a user's use token -- its callers are exactly
// the requests that have no local session yet (starting a login, completing
// one) or are refreshing one.
package cloudloginwebapp

import (
	"context"
	"net/url"
	"strconv"

	"github.com/Kaese72/cloud-connect/client/internal/applianceregistry"
	"github.com/Kaese72/cloud-connect/client/internal/logging"
	"github.com/Kaese72/cloud-connect/client/internal/persistence"
	"github.com/Kaese72/cloud-connect/client/restmodels"
	"github.com/danielgtaylor/huma/v2"
)

type webApp struct {
	persistence       persistence.CloudConnectClientDB
	applianceRegistry applianceregistry.Client
	cloudLoginURL     string
}

func NewWebApp(p persistence.CloudConnectClientDB, applianceRegistry applianceregistry.Client, cloudLoginURL string) webApp {
	return webApp{persistence: p, applianceRegistry: applianceRegistry, cloudLoginURL: cloudLoginURL}
}

// enrolled returns the current enrollment's cloud credentials, or a 409 if
// the appliance is not enrolled.
func (app webApp) enrolled(ctx context.Context) (applianceID int64, secret string, err error) {
	enrollment, err := app.persistence.GetCurrentEnrollment(ctx)
	if err != nil {
		logging.ErrorErr(err, ctx)
		return 0, "", huma.Error500InternalServerError("failed to look up enrollment state")
	}
	if enrollment.Status != persistence.StatusEnrolled || enrollment.ApplianceID == nil || enrollment.ApplianceSecret == nil {
		return 0, "", huma.Error409Conflict("appliance is not enrolled with the cloud")
	}
	return *enrollment.ApplianceID, *enrollment.ApplianceSecret, nil
}

func (app webApp) GetStatus(ctx context.Context, input *struct{}) (*struct {
	Body restmodels.CloudLoginStatusResponse
}, error) {
	_, _, err := app.enrolled(ctx)
	if err != nil {
		if se, ok := err.(huma.StatusError); ok && se.GetStatus() == 409 {
			return &struct {
				Body restmodels.CloudLoginStatusResponse
			}{Body: restmodels.CloudLoginStatusResponse{Available: false}}, nil
		}
		return nil, err
	}
	return &struct {
		Body restmodels.CloudLoginStatusResponse
	}{Body: restmodels.CloudLoginStatusResponse{Available: true}}, nil
}

// Start builds the cloud-ui URL the browser should be sent to. The state is
// generated and remembered by the caller (the authentication service); this
// service only forwards it.
func (app webApp) Start(ctx context.Context, input *struct {
	Body restmodels.CloudLoginStartRequest
}) (*struct {
	Body restmodels.CloudLoginStartResponse
}, error) {
	applianceID, _, err := app.enrolled(ctx)
	if err != nil {
		return nil, err
	}
	target, err := url.Parse(app.cloudLoginURL)
	if err != nil {
		logging.ErrorErr(err, ctx)
		return nil, huma.Error500InternalServerError("invalid cloud login URL")
	}
	query := target.Query()
	query.Set("state", input.Body.State)
	query.Set("return_to", input.Body.ReturnTo)
	query.Set("applianceId", strconv.FormatInt(applianceID, 10))
	target.RawQuery = query.Encode()
	return &struct {
		Body restmodels.CloudLoginStartResponse
	}{Body: restmodels.CloudLoginStartResponse{CloudURL: target.String()}}, nil
}

// Redeem exchanges the login code the browser brought back for the cloud
// user's identity, authenticating to the cloud as this appliance.
func (app webApp) Redeem(ctx context.Context, input *struct {
	Body restmodels.CloudLoginRedeemRequest
}) (*struct {
	Body restmodels.CloudLoginUserResponse
}, error) {
	applianceID, secret, err := app.enrolled(ctx)
	if err != nil {
		return nil, err
	}
	user, err := app.applianceRegistry.RedeemLoginCode(ctx, applianceID, secret, input.Body.Code)
	if err != nil {
		if applianceregistry.IsRefusal(err) {
			return nil, huma.Error403Forbidden("cloud login refused")
		}
		logging.ErrorErr(err, ctx)
		return nil, huma.Error502BadGateway("failed to redeem login code with appliance-registry")
	}
	return &struct {
		Body restmodels.CloudLoginUserResponse
	}{Body: restmodels.CloudLoginUserResponse{
		ID: user.ID, Username: user.Username, Name: user.Name, Surname: user.Surname, Email: user.Email,
	}}, nil
}

// CheckAccess reports whether a cloud user still has access to this
// appliance. A refusal from the registry (including it no longer accepting
// this appliance's secret) is reported as not allowed; only failing to get
// an answer at all is an error.
func (app webApp) CheckAccess(ctx context.Context, input *struct {
	CloudUserID int64 `path:"cloudUserId"`
}) (*struct {
	Body restmodels.CloudLoginAccessResponse
}, error) {
	applianceID, secret, err := app.enrolled(ctx)
	if err != nil {
		return nil, err
	}
	allowed, err := app.applianceRegistry.CheckAccess(ctx, applianceID, secret, input.CloudUserID)
	if err != nil {
		if applianceregistry.IsRefusal(err) {
			return &struct {
				Body restmodels.CloudLoginAccessResponse
			}{Body: restmodels.CloudLoginAccessResponse{Allowed: false}}, nil
		}
		logging.ErrorErr(err, ctx)
		return nil, huma.Error502BadGateway("failed to check access with appliance-registry")
	}
	return &struct {
		Body restmodels.CloudLoginAccessResponse
	}{Body: restmodels.CloudLoginAccessResponse{Allowed: allowed}}, nil
}
