package polar

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/chai-rs/handdraw-server/app/billing/model"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
	"github.com/stretchr/testify/require"
)

func TestWebhookVerifiesRawBodyAndDecodesSupportedEvent(t *testing.T) {
	secret := "sandbox-webhook-secret"
	body := []byte(`{"type":"subscription.active","timestamp":"2026-09-12T12:00:00Z","data":{"id":"f3bb7d85-8518-47d2-9280-1d41a61b939c"}}`)
	headers := signedHeaders(t, secret, "evt_delivery_1", body, time.Now())
	webhook, err := NewWebhook(secret)
	require.NoError(t, err)

	event, err := webhook.Decode(body, headers)

	require.NoError(t, err)
	require.Equal(t, "evt_delivery_1", event.ProviderEventID)
	require.Equal(t, "subscription.active", event.Type)
	require.Equal(t, time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC), event.OccurredAt)
	require.JSONEq(t, string(body), string(event.Payload))
}

func TestWebhookRejectsTamperingStaleDeliveryAndUnsupportedEvent(t *testing.T) {
	secret := "sandbox-webhook-secret"
	webhook, err := NewWebhook(secret)
	require.NoError(t, err)
	now := time.Now()

	tests := []struct {
		name    string
		body    []byte
		headers http.Header
	}{
		{
			name:    "tampered body",
			body:    []byte(`{"type":"subscription.active","timestamp":"2026-09-12T12:00:00Z","data":{"id":"changed"}}`),
			headers: signedHeaders(t, secret, "evt_delivery_2", []byte(`{"type":"subscription.active","timestamp":"2026-09-12T12:00:00Z","data":{"id":"original"}}`), now),
		},
		{
			name:    "stale delivery",
			body:    []byte(`{"type":"subscription.active","timestamp":"2026-09-12T12:00:00Z","data":{"id":"x"}}`),
			headers: signedHeaders(t, secret, "evt_delivery_3", []byte(`{"type":"subscription.active","timestamp":"2026-09-12T12:00:00Z","data":{"id":"x"}}`), now.Add(-6*time.Minute)),
		},
		{
			name:    "unsupported event",
			body:    []byte(`{"type":"benefit.created","timestamp":"2026-09-12T12:00:00Z","data":{"id":"x"}}`),
			headers: signedHeaders(t, secret, "evt_delivery_4", []byte(`{"type":"benefit.created","timestamp":"2026-09-12T12:00:00Z","data":{"id":"x"}}`), now),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, decodeErr := webhook.Decode(test.body, test.headers)
			require.ErrorIs(t, decodeErr, model.ErrInvalidWebhook)
		})
	}
}

func signedHeaders(t *testing.T, secret, id string, body []byte, at time.Time) http.Header {
	t.Helper()
	verifier, err := standardwebhooks.NewWebhook(base64.StdEncoding.EncodeToString([]byte(secret)))
	require.NoError(t, err)
	signature, err := verifier.Sign(id, at, body)
	require.NoError(t, err)
	headers := make(http.Header)
	headers.Set(standardwebhooks.HeaderWebhookID, id)
	headers.Set(standardwebhooks.HeaderWebhookTimestamp, fmt.Sprint(at.Unix()))
	headers.Set(standardwebhooks.HeaderWebhookSignature, signature)
	return headers
}
