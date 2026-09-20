// Package applianceregistry is a minimal client for the appliance-registry
// endpoints this service calls over plain outbound HTTPS, independent of the
// cloud-connect tunnel: redeeming an enrollment exchange code (which happens
// before the tunnel exists - see appliance-registry's README, "Enrollment")
// and, once enrolled, the appliance-authenticated cloud-login calls (see
// "Cloud login").
package applianceregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

type RedeemResponse struct {
	ApplianceSecret string `json:"applianceSecret"`
	Hostname        string `json:"hostname"`
	TunnelURL       string `json:"tunnelUrl"`
}

// CloudUser is the cloud identity behind a redeemed login code.
type CloudUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Surname  string `json:"surname"`
	Email    string `json:"email"`
}

// StatusError is returned when appliance-registry answered with a non-2xx
// status, as opposed to being unreachable. A 4xx means the registry
// understood and refused the request; a 5xx means it failed to answer it.
type StatusError struct {
	Status int
	Body   string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("appliance-registry request failed with status %d: %s", e.Status, e.Body)
}

// IsRefusal reports whether err is the registry deliberately refusing the
// request (a 4xx), rather than a transport or server failure.
func IsRefusal(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status >= 400 && se.Status < 500
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) Client {
	return Client{baseURL: baseURL, httpClient: &http.Client{Timeout: 15 * time.Second}}
}

func (c Client) do(ctx context.Context, method string, path string, bearer string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return &StatusError{Status: resp.StatusCode, Body: string(respBody)}
	}
	return json.Unmarshal(respBody, out)
}

// Redeem exchanges a single-use enrollment code for the appliance's
// permanent cloud-connect secret - see EnrollRedeem in appliance-registry's
// internal/appliancewebapp/webapp.go.
func (c Client) Redeem(ctx context.Context, applianceID int64, exchangeCode string) (RedeemResponse, error) {
	var out RedeemResponse
	err := c.do(ctx, http.MethodPost, "/appliance-registry/v0/appliances/"+strconv.FormatInt(applianceID, 10)+"/enroll/redeem", exchangeCode, nil, &out)
	return out, err
}

// RedeemLoginCode exchanges a single-use cloud-login code for the cloud user
// it was issued to, authenticating as the appliance with its secret - see
// RedeemLoginCode in appliance-registry.
func (c Client) RedeemLoginCode(ctx context.Context, applianceID int64, applianceSecret string, code string) (CloudUser, error) {
	var out CloudUser
	err := c.do(ctx, http.MethodPost, "/appliance-registry/v0/appliances/"+strconv.FormatInt(applianceID, 10)+"/login-code/redeem", applianceSecret, map[string]string{"code": code}, &out)
	return out, err
}

// CheckAccess asks whether a cloud user still has access to this appliance -
// see CheckAccess in appliance-registry.
func (c Client) CheckAccess(ctx context.Context, applianceID int64, applianceSecret string, cloudUserID int64) (bool, error) {
	var out struct {
		Allowed bool `json:"allowed"`
	}
	err := c.do(ctx, http.MethodGet, "/appliance-registry/v0/appliances/"+strconv.FormatInt(applianceID, 10)+"/access/"+strconv.FormatInt(cloudUserID, 10), applianceSecret, nil, &out)
	return out.Allowed, err
}
