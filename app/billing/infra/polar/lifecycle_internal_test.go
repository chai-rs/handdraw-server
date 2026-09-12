package polar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeatChangesUseExplicitProrationPolicy(t *testing.T) {
	tests := []struct {
		name        string
		seats       int
		sourceSeats int
		proration   string
	}{
		{name: "increase invoices now", seats: 4, sourceSeats: 2, proration: "invoice"},
		{name: "decrease starts next period", seats: 2, sourceSeats: 4, proration: "next_period"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var body map[string]any
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, http.MethodPatch, request.Method)
				require.Equal(t, "/v1/subscriptions/subscription-1", request.URL.Path)
				require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
				return jsonResponse(`{"id":"subscription-1"}`), nil
			})}
			provider := &Provider{client: client, config: validConfig("https://polar.test")}
			_, err := provider.mutateSubscription(context.Background(), operation{
				Kind: "seat_change", SourceSubscriptionID: "subscription-1", Seats: test.seats, SourceSeats: test.sourceSeats,
			})
			require.NoError(t, err)
			require.Equal(t, float64(test.seats), body["seats"])
			require.Equal(t, test.proration, body["proration_behavior"])
		})
	}
}

func TestCreatePortalUsesServerOwnedReturnURL(t *testing.T) {
	var body map[string]string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, "/v1/customer-sessions/", request.URL.Path)
		require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
		return jsonResponse(`{"customer_portal_url":"https://sandbox.polar.sh/polar_cl_test"}`), nil
	})}
	provider := &Provider{client: client, config: validConfig("https://polar.test")}

	portalURL, err := provider.CreatePortal(context.Background(), "ws_01KTEST")

	require.NoError(t, err)
	require.Equal(t, "https://sandbox.polar.sh/polar_cl_test", portalURL)
	require.Equal(t, "ws_01KTEST", body["external_customer_id"])
	require.Equal(t, provider.config.ReturnURL, body["return_url"])
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
