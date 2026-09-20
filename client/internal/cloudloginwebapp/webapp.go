// Package cloudloginwebapp is the internal API the appliance's authentication
// service uses to offer "log in with Humi Cloud". It is authenticated with a
// static service token rather than a user's use token, because its callers
// are exactly the requests that have no local session yet (starting a login,
// completing one) or are refreshing one.
package cloudloginwebapp

import (
	"context"
	"crypto/subtle"
	"net/url"
	"strconv"
	"strings"

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
	serviceTokens     []string
}

func NewWebApp(p persistence.CloudConnectClientDB, applianceRegistry applianceregistry.Client, cloudLoginURL string, serviceTokens []string) webApp {
	return webApp{persistence: p, applianceRegistry: applianceRegistry, cloudLoginURL: cloudLoginURL, serviceTokens: serviceTokens}
}

// ParseTokenList splits a comma-separated config value into the set of
// currently-valid service tokens, so one can be rotated without a
// synchronized cutover.
func ParseTokenList(raw string) []string {
	var out []string
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (app webApp) checkServiceToken(authHeader string) error {
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return huma.Error401Unauthorized("invalid service token")
	}
	provided := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	for _, configured := range app.serviceTokens {
		if subtle.ConstantTimeCompare([]byte(provided), []byte(configured)) == 1 {
			return nil
		}
	}
	return huma.Error401Unauthorized("invalid service token")
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

func (app webApp) GetStatus(ctx context.Context, input *struct {
	Authorization string `header:"Authorization"`
}) (*struct {
	Body restmodels.CloudLoginStatusResponse
}, error) {
	if err := app.checkServiceToken(input.Authorization); err != nil {
		return nil, err
	}
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
	Authorization string `header:"Authorization"`
	Body          restmodels.CloudLoginStartRequest
}) (*struct {
	Body restmodels.CloudLoginStartResponse
}, error) {
	if err := app.checkServiceToken(input.Authorization); err != nil {
		return nil, err
	}
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
	Authorization string `header:"Authorization"`
	Body          restmodels.CloudLoginRedeemRequest
}) (*struct {
	Body restmodels.CloudLoginUserResponse
}, error) {
	if err := app.checkServiceToken(input.Authorization); err != nil {
		return nil, err
	}
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
	Authorization string `header:"Authorization"`
	CloudUserID   int64  `path:"cloudUserId"`
}) (*struct {
	Body restmodels.CloudLoginAccessResponse
}, error) {
	if err := app.checkServiceToken(input.Authorization); err != nil {
		return nil, err
	}
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
