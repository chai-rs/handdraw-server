// Package polar creates hosted Polar checkout sessions for durable billing intentions.
package polar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chai-rs/handdraw-server/app/billing/model"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const (
	checkoutPath  = "/v1/checkouts/"
	maxTrialSeats = 5
)

// Config identifies the Polar environment and the immutable catalog products.
type Config struct {
	BaseURL             string        `split_words:"true" default:"https://sandbox-api.polar.sh"`
	AccessToken         string        `split_words:"true" json:"-"`
	SuccessURL          string        `split_words:"true"`
	ReturnURL           string        `split_words:"true"`
	CloudMonthlyProduct string        `split_words:"true"`
	CloudYearlyProduct  string        `split_words:"true"`
	TeamMonthlyProduct  string        `split_words:"true"`
	TeamYearlyProduct   string        `split_words:"true"`
	Timeout             time.Duration `default:"10s"`
}

// Validate rejects incomplete catalogs and redirect URLs outside HTTP(S).
func (c Config) Validate() error {
	if c.AccessToken == "" || c.Timeout <= 0 {
		return model.ErrInvalid
	}

	for _, raw := range []string{c.BaseURL, c.SuccessURL, c.ReturnURL} {
		u, err := url.ParseRequestURI(raw)
		if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
			return model.ErrInvalid
		}
	}

	for _, id := range []string{c.CloudMonthlyProduct, c.CloudYearlyProduct, c.TeamMonthlyProduct, c.TeamYearlyProduct} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			return model.ErrInvalid
		}
	}

	return nil
}

type operation struct {
	WorkspaceID     string `json:"workspace_id"`
	Kind            string `json:"kind"`
	Plan            string `json:"plan"`
	BillingInterval string `json:"billing_interval"`
	Seats           int    `json:"seats"`
	CheckoutID      string `json:"checkout_id"`
	CheckoutURL     string `json:"checkout_url"`
}

type checkoutRequest struct {
	Products           []string `json:"products"`
	Seats              *int     `json:"seats,omitempty"`
	AllowTrial         bool     `json:"allow_trial"`
	ExternalCustomerID string   `json:"external_customer_id"`
	Metadata           metadata `json:"metadata"`
	SuccessURL         string   `json:"success_url"`
	ReturnURL          string   `json:"return_url"`
}

type metadata struct {
	IntentID        string `json:"intent_id"`
	WorkspaceID     string `json:"workspace_id"`
	Plan            string `json:"plan"`
	BillingInterval string `json:"billing_interval"`
	Seats           int    `json:"seats"`
}

type checkoutResponse struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// Provider implements the billing provider boundary with Polar's hosted checkout API.
type Provider struct {
	db     *bun.DB
	client *http.Client
	config Config
}

var _ model.Provider = (*Provider)(nil)

// New validates all four SKUs before the worker can accept billing work.
func New(db *bun.DB, client *http.Client, config Config) (*Provider, error) {
	if db == nil || config.Validate() != nil {
		return nil, model.ErrInvalid
	}

	if client == nil {
		client = &http.Client{Timeout: config.Timeout}
	}

	return &Provider{db: db, client: client, config: config}, nil
}

// Observe reuses a recorded checkout or creates one for an unprocessed checkout intention.
func (p *Provider) Observe(ctx context.Context, id string) (model.Snapshot, error) {
	op, err := p.prepare(ctx, id)
	if err != nil {
		return model.Snapshot{}, err
	}

	if op.CheckoutID != "" {
		return pending(op.CheckoutID, op.CheckoutURL), nil
	}

	if op.Kind != "checkout" {
		return model.Snapshot{}, model.ErrConflict
	}

	product, err := p.product(op.Plan, op.BillingInterval)
	if err != nil {
		return model.Snapshot{}, err
	}

	body := checkoutRequest{
		Products:           []string{product},
		AllowTrial:         op.Plan != "team" || op.Seats <= maxTrialSeats,
		ExternalCustomerID: op.WorkspaceID,
		Metadata: metadata{
			IntentID: id, WorkspaceID: op.WorkspaceID, Plan: op.Plan,
			BillingInterval: op.BillingInterval, Seats: op.Seats,
		},
		SuccessURL: p.config.SuccessURL,
		ReturnURL:  p.config.ReturnURL,
	}
	if op.Plan == "team" {
		body.Seats = &op.Seats
	}

	created, err := p.create(ctx, body)
	if err != nil {
		return model.Snapshot{}, err
	}

	return pending(created.ID, created.URL), nil
}

func (p *Provider) prepare(ctx context.Context, id string) (operation, error) {
	var raw []byte
	if err := p.db.NewRaw("SELECT handdraw.polar_billing_prepare(?)", id).Scan(ctx, &raw); err != nil {
		return operation{}, err
	}

	var result operation
	if err := json.Unmarshal(raw, &result); err != nil {
		return operation{}, err
	}

	return result, nil
}

func (p *Provider) product(plan, interval string) (string, error) {
	products := map[string]string{
		"cloud/month": p.config.CloudMonthlyProduct,
		"cloud/year":  p.config.CloudYearlyProduct,
		"team/month":  p.config.TeamMonthlyProduct,
		"team/year":   p.config.TeamYearlyProduct,
	}

	product := products[plan+"/"+interval]
	if product == "" {
		return "", model.ErrInvalid
	}

	return product, nil
}

func (p *Provider) create(ctx context.Context, payload checkoutRequest) (checkoutResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return checkoutResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.config.BaseURL, "/")+checkoutPath, bytes.NewReader(body))
	if err != nil {
		return checkoutResponse{}, err
	}

	req.Header.Set("Authorization", "Bearer "+p.config.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	res, err := p.client.Do(req)
	if err != nil {
		return checkoutResponse{}, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusCreated {
		detail, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return checkoutResponse{}, fmt.Errorf("polar checkout: %d: %s", res.StatusCode, strings.TrimSpace(string(detail)))
	}

	var created checkoutResponse
	if err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&created); err != nil {
		return checkoutResponse{}, err
	}

	id, err := uuid.Parse(created.ID)

	checkoutURL, urlErr := url.ParseRequestURI(created.URL)
	if err != nil || id.String() != created.ID || urlErr != nil || checkoutURL.Scheme != "https" || checkoutURL.Host == "" {
		return checkoutResponse{}, model.ErrInvalid
	}

	return created, nil
}

func pending(id, checkoutURL string) model.Snapshot {
	return model.Snapshot{
		Provider: "polar", Version: 1, Status: "pending", CheckoutID: id,
		CheckoutURL: checkoutURL, Orders: []model.Order{}, Refunds: []model.Refund{},
	}
}
