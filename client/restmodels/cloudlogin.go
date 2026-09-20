package restmodels

// The types below are the internal API the appliance's authentication
// service uses to offer "log in with Humi Cloud" - see the README's "Cloud
// login" section. This service is the only thing on the appliance that holds
// the appliance's cloud credentials, so the authentication service goes
// through it rather than talking to appliance-registry itself.

// CloudLoginStatusResponse reports whether cloud login can be offered, i.e.
// whether the appliance is enrolled with the cloud.
type CloudLoginStatusResponse struct {
	Available bool `json:"available"`
}

// CloudLoginStartRequest carries the authentication service's own one-time
// state value and the local UI callback URL the browser should return to.
type CloudLoginStartRequest struct {
	State    string `json:"state" minLength:"1" maxLength:"128"`
	ReturnTo string `json:"returnTo" minLength:"1" maxLength:"512"`
}

// CloudLoginStartResponse is the cloud URL the browser should be redirected
// to next.
type CloudLoginStartResponse struct {
	CloudURL string `json:"cloudUrl"`
}

type CloudLoginRedeemRequest struct {
	Code string `json:"code" minLength:"1"`
}

// CloudLoginUserResponse is the cloud identity established by redeeming a
// login code.
type CloudLoginUserResponse struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Surname  string `json:"surname"`
	Email    string `json:"email"`
}

// CloudLoginAccessResponse answers whether a previously logged-in cloud user
// still has access to this appliance. Allowed is false both when the user
// has lost access and when this appliance's own cloud credentials are no
// longer accepted; a cloud that cannot be reached at all is reported as a
// 502 instead, so callers can tell "denied" from "unknown".
type CloudLoginAccessResponse struct {
	Allowed bool `json:"allowed"`
}
