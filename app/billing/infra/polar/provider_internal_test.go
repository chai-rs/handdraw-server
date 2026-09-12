package polar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/chai-rs/handdraw-server/app/billing/model"
	"github.com/stretchr/testify/require"
)

const (
	testCloudMonth = "466beb9c-08f4-4445-9d83-aae610d066ab"
	testCloudYear  = "da67b324-07cd-40d8-8e75-d5221931fe2f"
	testTeamMonth  = "6299e9e5-6937-4165-81d0-c1ac2568d7ab"
	testTeamYear   = "c20cdaf0-499f-4f04-93fb-b0e7ec958779"
)

func validConfig(baseURL string) Config {
	return Config{
		BaseURL: baseURL, AccessToken: "polar_oat_test", SuccessURL: "https://handdraw.dev/app/cloud?checkout_id={CHECKOUT_ID}",
		ReturnURL: "https://handdraw.dev/app/cloud", CloudMonthlyProduct: testCloudMonth,
		CloudYearlyProduct: testCloudYear, TeamMonthlyProduct: testTeamMonth, TeamYearlyProduct: testTeamYear,
		Timeout: time.Second,
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestConfigRejectsIncompleteOrMalformedCatalog(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing access token", func(c *Config) { c.AccessToken = "" }},
		{"relative success URL", func(c *Config) { c.SuccessURL = "/app/cloud" }},
		{"malformed product ID", func(c *Config) { c.TeamYearlyProduct = "team-year" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := validConfig("https://sandbox-api.polar.sh")
			tc.mutate(&config)
			require.ErrorIs(t, config.Validate(), model.ErrInvalid)
		})
	}
}

func TestProductCatalogMapsEveryPlanAndIntervalToItsFixedSKU(t *testing.T) {
	provider := &Provider{config: validConfig("https://polar.test")}
	for _, tc := range []struct {
		plan     string
		interval string
		want     string
	}{
		{"cloud", "month", testCloudMonth},
		{"cloud", "year", testCloudYear},
		{"team", "month", testTeamMonth},
		{"team", "year", testTeamYear},
	} {
		t.Run(tc.plan+" "+tc.interval, func(t *testing.T) {
			product, err := provider.product(tc.plan, tc.interval)
			require.NoError(t, err)
			require.Equal(t, tc.want, product)
		})
	}
}

func TestCreateCheckoutUsesFixedProductAndWorkspaceMetadata(t *testing.T) {
	require.NoError(t, validConfig("https://polar.test").Validate())

	var received checkoutRequest
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, checkoutPath, r.URL.Path)
		require.Equal(t, "Bearer polar_oat_test", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))

		return &http.Response{
			StatusCode: http.StatusCreated,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"ec0f3698-3959-4a4b-9f68-b194213fd943","url":"https://sandbox.polar.sh/checkout/ec0f3698"}`)),
		}, nil
	})}

	provider := &Provider{client: client, config: validConfig("https://polar.test")}
	seats := 3
	created, err := provider.create(context.Background(), checkoutRequest{
		Products: []string{testTeamMonth}, Seats: &seats, AllowTrial: true,
		ExternalCustomerID: "ws_01KTEST", Metadata: metadata{
			IntentID: "bci_01KTEST", WorkspaceID: "ws_01KTEST", Plan: "team", BillingInterval: "month", Seats: seats,
		},
		SuccessURL: provider.config.SuccessURL, ReturnURL: provider.config.ReturnURL,
	})
	require.NoError(t, err)
	require.Equal(t, "ec0f3698-3959-4a4b-9f68-b194213fd943", created.ID)
	require.Equal(t, []string{testTeamMonth}, received.Products)
	require.NotNil(t, received.Seats)
	require.Equal(t, seats, *received.Seats)
	require.True(t, received.AllowTrial)
	require.Equal(t, "ws_01KTEST", received.ExternalCustomerID)
	require.Equal(t, "bci_01KTEST", received.Metadata.IntentID)
}

func TestCreateCheckoutRejectsProviderFailure(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnprocessableEntity,
			Body:       io.NopCloser(strings.NewReader(`{"detail":"invalid product"}`)),
			Header:     make(http.Header),
		}, nil
	})}

	provider := &Provider{client: client, config: validConfig("https://polar.test")}
	_, err := provider.create(context.Background(), checkoutRequest{Products: []string{testCloudMonth}})
	require.ErrorContains(t, err, "polar checkout: 422")
}
