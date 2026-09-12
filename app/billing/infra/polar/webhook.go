package polar

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/chai-rs/handdraw-server/app/billing/model"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

var supportedEvents = map[string]struct{}{
	"order.paid":              {},
	"order.refunded":          {},
	"refund.updated":          {},
	"subscription.active":     {},
	"subscription.canceled":   {},
	"subscription.created":    {},
	"subscription.past_due":   {},
	"subscription.revoked":    {},
	"subscription.uncanceled": {},
	"subscription.updated":    {},
}

// Webhook verifies Polar's Standard Webhooks signature against the untouched request body.
type Webhook struct{ verifier *standardwebhooks.Webhook }

var _ model.WebhookDecoder = (*Webhook)(nil)

// NewWebhook converts Polar's literal configured secret to the encoding expected by the standard library.
func NewWebhook(secret string) (*Webhook, error) {
	if secret == "" || len(secret) > 512 {
		return nil, model.ErrInvalid
	}

	verifier, err := standardwebhooks.NewWebhook(base64.StdEncoding.EncodeToString([]byte(secret)))
	if err != nil {
		return nil, model.ErrInvalid
	}

	return &Webhook{verifier: verifier}, nil
}

// Decode authenticates freshness, delivery identity and raw bytes before accepting supported JSON.
func (w *Webhook) Decode(body []byte, headers http.Header) (model.ProviderEvent, error) {
	if len(body) == 0 || len(body) > 256*1024 || w.verifier.Verify(body, headers) != nil {
		return model.ProviderEvent{}, model.ErrInvalidWebhook
	}

	var envelope struct {
		Type      string          `json:"type"`
		Timestamp time.Time       `json:"timestamp"`
		Data      json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Timestamp.IsZero() || len(envelope.Data) == 0 {
		return model.ProviderEvent{}, model.ErrInvalidWebhook
	}

	if _, ok := supportedEvents[envelope.Type]; !ok {
		return model.ProviderEvent{}, model.ErrInvalidWebhook
	}

	id := headers.Get(standardwebhooks.HeaderWebhookID)
	if id == "" || len(id) > 200 {
		return model.ProviderEvent{}, model.ErrInvalidWebhook
	}

	payload := append(json.RawMessage(nil), body...)

	return model.ProviderEvent{ProviderEventID: id, Type: envelope.Type, OccurredAt: envelope.Timestamp.UTC(), Payload: payload}, nil
}
