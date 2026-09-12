package db

import (
	"context"
	"encoding/json"

	"github.com/chai-rs/handdraw-server/app/billing/model"
	"github.com/uptrace/bun"
)

// PolarWebhookQueue stores authenticated deliveries behind function-only billing credentials.
type PolarWebhookQueue struct{ worker *bun.DB }

var _ model.WebhookQueue = (*PolarWebhookQueue)(nil)

// NewPolarWebhookQueue binds the durable inbox to the restricted billing connection.
func NewPolarWebhookQueue(worker *bun.DB) *PolarWebhookQueue {
	return &PolarWebhookQueue{worker: worker}
}

// Accept commits a delivery identity and exact payload before the HTTP handler acknowledges it.
func (q *PolarWebhookQueue) Accept(ctx context.Context, event model.ProviderEvent) error {
	raw, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}

	_, err = q.worker.ExecContext(ctx, "SELECT handdraw.accept_polar_webhook(?,?,?,?::jsonb)", event.ProviderEventID, event.Type, event.OccurredAt, string(raw))

	return mapped(err)
}

// LeaseWebhook obtains one expiring delivery lease in provider occurrence order.
func (q *PolarWebhookQueue) LeaseWebhook(ctx context.Context, token string) (*model.WebhookTask, error) {
	var raw []byte
	if err := q.worker.NewRaw("SELECT handdraw.lease_polar_webhook(?)", token).Scan(ctx, &raw); err != nil {
		return nil, mapped(err)
	}

	if len(raw) == 0 || string(raw) == "null" {
		return nil, model.ErrEmpty
	}

	var task model.WebhookTask
	if err := json.Unmarshal(raw, &task); err != nil {
		return nil, err
	}

	return &task, nil
}

// CompleteWebhook acknowledges success or schedules bounded retry without dropping the delivery.
func (q *PolarWebhookQueue) CompleteWebhook(ctx context.Context, task model.WebhookTask, failure error) error {
	var message any
	if failure != nil {
		message = failure.Error()
	}

	_, err := q.worker.ExecContext(ctx, "SELECT handdraw.complete_polar_webhook(?,?,?)", task.ProviderEventID, task.Token, message)

	return mapped(err)
}
