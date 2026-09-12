// Package applianceregistry is a minimal client for the one
// appliance-registry endpoint this service calls: redeeming an enrollment
// exchange code server-to-server, over plain outbound HTTPS, independent of
// the cloud-connect tunnel (which does not exist yet at this point in the
// flow) - see appliance-registry's README, "Enrollment" section.
package applianceregistry

import (
	"context"
	"encoding/json"
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

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) Client {
	return Client{baseURL: baseURL, httpClient: &http.Client{Timeout: 15 * time.Second}}
}

// Redeem exchanges a single-use enrollment code for the appliance's
// permanent cloud-connect secret - see EnrollRedeem in appliance-registry's
// internal/appliancewebapp/webapp.go.
func (c Client) Redeem(ctx context.Context, applianceID int64, exchangeCode string) (RedeemResponse, error) {
	url := c.baseURL + "/appliance-registry/v0/appliances/" + strconv.FormatInt(applianceID, 10) + "/enroll/redeem"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return RedeemResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+exchangeCode)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return RedeemResponse{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return RedeemResponse{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return RedeemResponse{}, fmt.Errorf("appliance-registry redeem failed with status %d: %s", resp.StatusCode, string(body))
	}

	var out RedeemResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return RedeemResponse{}, err
	}
	return out, nil
}
