package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// ErrInvalidWebhook rejects an unauthenticated, stale or unsupported provider delivery.
var ErrInvalidWebhook = errors.New("invalid provider webhook")

// ProviderEvent is one authenticated provider delivery retained before acknowledgement.
type ProviderEvent struct {
	ProviderEventID string          `json:"provider_event_id"`
	Type            string          `json:"type"`
	OccurredAt      time.Time       `json:"occurred_at"`
	Payload         json.RawMessage `json:"payload"`
}

// WebhookTask carries one durable delivery lease to the reconciliation worker.
type WebhookTask struct {
	ProviderEvent
	Token    string `json:"token"`
	Attempts int    `json:"attempts"`
}

// WebhookDecoder authenticates the exact request bytes before decoding their envelope.
//
//mockery:generate: true
type WebhookDecoder interface {
	Decode([]byte, http.Header) (ProviderEvent, error)
}

// WebhookInbox durably deduplicates authenticated deliveries.
//
//mockery:generate: true
type WebhookInbox interface {
	Accept(context.Context, ProviderEvent) error
}

// WebhookQueue leases and completes authenticated provider deliveries.
//
//mockery:generate: true
type WebhookQueue interface {
	WebhookInbox
	LeaseWebhook(context.Context, string) (*WebhookTask, error)
	CompleteWebhook(context.Context, WebhookTask, error) error
}
