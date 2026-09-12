package api_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chai-rs/handdraw-server/app/billing/inbound/api"
	"github.com/chai-rs/handdraw-server/app/billing/model"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
)

type webhookDecoder struct {
	event model.ProviderEvent
	err   error
}

func (d webhookDecoder) Decode([]byte, http.Header) (model.ProviderEvent, error) {
	return d.event, d.err
}

type webhookInbox struct {
	accepted []model.ProviderEvent
	err      error
}

func (i *webhookInbox) Accept(_ context.Context, event model.ProviderEvent) error {
	i.accepted = append(i.accepted, event)
	return i.err
}

func TestPolarWebhookPersistsBeforeAcknowledging(t *testing.T) {
	inbox := &webhookInbox{}
	event := model.ProviderEvent{ProviderEventID: "evt_1", Type: "subscription.active"}
	app := fiber.New()
	api.NewPolarWebhook(webhookDecoder{event: event}, inbox).Register(app.Group("/v1"))
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/polar", bytes.NewBufferString(`{"type":"subscription.active"}`))
	req.Header.Set("Content-Type", "application/json")

	response, err := app.Test(req)

	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	require.Equal(t, []model.ProviderEvent{event}, inbox.accepted)
}

func TestPolarWebhookFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		decoder webhookDecoder
		inbox   *webhookInbox
		status  int
	}{
		{name: "invalid signature", decoder: webhookDecoder{err: model.ErrInvalid}, inbox: &webhookInbox{}, status: http.StatusForbidden},
		{name: "durable inbox unavailable", decoder: webhookDecoder{event: model.ProviderEvent{ProviderEventID: "evt_2", Type: "order.paid"}}, inbox: &webhookInbox{err: errors.New("database unavailable")}, status: http.StatusServiceUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := fiber.New(fiber.Config{ErrorHandler: fx.ErrorHandler})
			api.NewPolarWebhook(test.decoder, test.inbox).Register(app.Group("/v1"))
			req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/polar", bytes.NewBufferString(`{"type":"order.paid"}`))
			req.Header.Set("Content-Type", "application/json")
			response, err := app.Test(req)
			require.NoError(t, err)
			require.Equal(t, test.status, response.StatusCode)
		})
	}
}
