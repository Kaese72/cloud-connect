package restmodels

// StatusResponse describes the current enrollment/connection state - see
// the README's "Enrollment" section. TunnelConnected reflects the live
// chisel client state, not anything persisted, since it can flip between
// polls independent of any API call.
type StatusResponse struct {
	Status          string  `json:"status"`
	Hostname        *string `json:"hostname,omitempty"`
	TunnelConnected bool    `json:"tunnelConnected"`
}

// EnrollmentStartRequest carries the local UI's own callback URL, so this
// service doesn't need to know huemie-ui's origin as config - see the
// README.
type EnrollmentStartRequest struct {
	ReturnTo string `json:"returnTo" minLength:"1" maxLength:"512"`
}

// EnrollmentStartResponse is the cloud URL the browser should be redirected
// to next.
type EnrollmentStartResponse struct {
	CloudURL string `json:"cloudUrl"`
}

// EnrollmentCompleteRequest is what the local UI's callback page collects
// from the browser's query string and forwards here.
type EnrollmentCompleteRequest struct {
	Code        string `json:"code" minLength:"1"`
	State       string `json:"state" minLength:"1"`
	ApplianceID int64  `json:"applianceId"`
}
