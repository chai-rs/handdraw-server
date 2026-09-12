package polar

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/chai-rs/handdraw-server/app/billing/model"
)

var _ model.PortalProvider = (*Provider)(nil)

// CreatePortal returns a short-lived Polar customer portal URL for the workspace customer.
func (p *Provider) CreatePortal(ctx context.Context, externalCustomerID string) (string, error) {
	if externalCustomerID == "" || len(externalCustomerID) > 200 {
		return "", model.ErrInvalid
	}

	body, err := json.Marshal(map[string]string{"external_customer_id": externalCustomerID, "return_url": p.config.ReturnURL})
	if err != nil {
		return "", err
	}

	var response struct {
		URL string `json:"customer_portal_url"`
	}
	if err = p.request(ctx, http.MethodPost, "/v1/customer-sessions/", bytes.NewReader(body), &response); err != nil {
		return "", err
	}

	parsed, err := url.ParseRequestURI(response.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", model.ErrInvalid
	}

	return response.URL, nil
}
