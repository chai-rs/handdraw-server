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
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

type polarSubscription struct {
	ID                  string         `json:"id"`
	ModifiedAt          *time.Time     `json:"modified_at"`
	CreatedAt           time.Time      `json:"created_at"`
	Status              string         `json:"status"`
	CurrentPeriodStart  time.Time      `json:"current_period_start"`
	CurrentPeriodEnd    time.Time      `json:"current_period_end"`
	TrialEnd            *time.Time     `json:"trial_end"`
	CancelAtPeriodEnd   bool           `json:"cancel_at_period_end"`
	CustomerID          string         `json:"customer_id"`
	ProductID           string         `json:"product_id"`
	CheckoutID          *string        `json:"checkout_id"`
	Seats               *int           `json:"seats"`
	Metadata            map[string]any `json:"metadata"`
	Customer            polarCustomer  `json:"customer"`
	PendingSubscription any            `json:"pending_update"`
}

type polarCustomer struct {
	ID         string  `json:"id"`
	ExternalID *string `json:"external_id"`
}

type polarOrder struct {
	ID             string             `json:"id"`
	CreatedAt      time.Time          `json:"created_at"`
	ModifiedAt     *time.Time         `json:"modified_at"`
	Status         string             `json:"status"`
	Paid           bool               `json:"paid"`
	TotalAmount    int64              `json:"total_amount"`
	Currency       string             `json:"currency"`
	SubscriptionID *string            `json:"subscription_id"`
	Subscription   *polarSubscription `json:"subscription"`
}

type polarRefund struct {
	ID             string     `json:"id"`
	CreatedAt      time.Time  `json:"created_at"`
	ModifiedAt     *time.Time `json:"modified_at"`
	Status         string     `json:"status"`
	Amount         int64      `json:"amount"`
	Currency       string     `json:"currency"`
	OrderID        string     `json:"order_id"`
	SubscriptionID *string    `json:"subscription_id"`
}

type page[T any] struct {
	Items      []T `json:"items"`
	Pagination struct {
		MaxPage int `json:"max_page"`
	} `json:"pagination"`
}

func (p *Provider) request(ctx context.Context, method, path string, body io.Reader, target any) error {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.config.BaseURL, "/")+path, body)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+p.config.AccessToken)
	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	response, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("polar API: %d: %s", response.StatusCode, strings.TrimSpace(string(detail)))
	}

	if target == nil {
		return nil
	}

	return json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(target)
}

func (p *Provider) subscription(ctx context.Context, id string) (polarSubscription, error) {
	var subscription polarSubscription

	err := p.request(ctx, http.MethodGet, "/v1/subscriptions/"+url.PathEscape(id), nil, &subscription)

	return subscription, err
}

func (p *Provider) mutateSubscription(ctx context.Context, op operation) (polarSubscription, error) {
	if op.SourceSubscriptionID == "" {
		return polarSubscription{}, model.ErrConflict
	}

	payload := map[string]any{}

	switch op.Kind {
	case "cancel":
		payload["cancel_at_period_end"] = true
	case "resume":
		payload["cancel_at_period_end"] = false
	case "seat_change":
		payload["seats"] = op.Seats
		if op.Seats < op.SourceSeats {
			payload["proration_behavior"] = "next_period"
		} else {
			payload["proration_behavior"] = "invoice"
		}
	case "plan_downgrade":
		if op.Phase == "checkout_ready" {
			return polarSubscription{}, model.ErrConflict
		}

		payload["cancel_at_period_end"] = true
	default:
		return polarSubscription{}, model.ErrConflict
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return polarSubscription{}, err
	}

	var subscription polarSubscription

	err = p.request(ctx, http.MethodPatch, "/v1/subscriptions/"+url.PathEscape(op.SourceSubscriptionID), bytes.NewReader(body), &subscription)

	return subscription, err
}

func (p *Provider) cancelAtPeriodEnd(ctx context.Context, subscriptionID string) error {
	body := strings.NewReader(`{"cancel_at_period_end":true}`)
	return p.request(ctx, http.MethodPatch, "/v1/subscriptions/"+url.PathEscape(subscriptionID), body, &polarSubscription{})
}

func (p *Provider) snapshot(ctx context.Context, op operation, subscription polarSubscription) (model.Snapshot, error) {
	product, err := p.product(op.Plan, op.BillingInterval)
	if err != nil || subscription.ID == "" || subscription.ProductID != product || subscription.Customer.ExternalID == nil || *subscription.Customer.ExternalID != op.WorkspaceID {
		return model.Snapshot{}, model.ErrConflict
	}

	seats := 1
	if subscription.Seats != nil {
		seats = *subscription.Seats
	}

	if seats != op.Seats && !(op.Kind == "seat_change" && op.Seats < op.SourceSeats && subscription.PendingSubscription != nil) {
		return model.Snapshot{}, model.ErrConflict
	}

	orders, newest, err := p.orders(ctx, subscription)
	if err != nil {
		return model.Snapshot{}, err
	}

	refunds, refundNewest, err := p.refunds(ctx, subscription.ID)
	if err != nil {
		return model.Snapshot{}, err
	}

	versionAt := subscription.CreatedAt
	if subscription.ModifiedAt != nil {
		versionAt = *subscription.ModifiedAt
	}

	if newest.After(versionAt) {
		versionAt = newest
	}

	if refundNewest.After(versionAt) {
		versionAt = refundNewest
	}

	state := "pending"

	snapshot := model.Snapshot{
		Provider: "polar", Version: versionAt.UnixNano(), IntentID: op.ID, WorkspaceID: op.WorkspaceID,
		CustomerID: subscription.CustomerID, ProductID: subscription.ProductID, Plan: op.Plan,
		BillingInterval: op.BillingInterval, Seats: seats, SubscriptionID: subscription.ID,
		Status: state, CardVerified: subscription.Status == "trialing", CancelAtPeriodEnd: subscription.CancelAtPeriodEnd,
		Orders: orders, Refunds: refunds,
	}
	if subscription.CheckoutID != nil {
		snapshot.CheckoutID = *subscription.CheckoutID
	}

	switch subscription.Status {
	case "trialing":
		snapshot.Status, snapshot.TrialEndsAt = "trialing", subscription.TrialEnd
	case "active":
		snapshot.Status, snapshot.PaidThroughAt = "active", &subscription.CurrentPeriodEnd
	case "past_due":
		snapshot.Status, snapshot.RenewalFailureAt = "renewal_failed", &versionAt
		snapshot.RenewalObligationID = "polar:" + subscription.ID + ":" + subscription.CurrentPeriodEnd.UTC().Format(time.RFC3339Nano)
	case "canceled", "unpaid", "incomplete_expired":
		snapshot.Status = "ended"
	case "incomplete":
		snapshot.Status = "pending"
	default:
		return model.Snapshot{}, model.ErrConflict
	}

	return snapshot, nil
}

func (p *Provider) orders(ctx context.Context, subscription polarSubscription) ([]model.Order, time.Time, error) {
	query := url.Values{"subscription_id": {subscription.ID}, "limit": {"100"}, "sorting": {"created_at"}}

	var response page[polarOrder]
	if err := p.request(ctx, http.MethodGet, "/v1/orders/?"+query.Encode(), nil, &response); err != nil {
		return nil, time.Time{}, err
	}

	result := make([]model.Order, 0, len(response.Items))

	var newest time.Time

	for _, order := range response.Items {
		if !order.Paid || order.Status != "paid" && order.Status != "refunded" && order.Status != "partially_refunded" || strings.ToUpper(order.Currency) != "USD" || order.TotalAmount < 1 {
			continue
		}

		from, until := subscription.CurrentPeriodStart, subscription.CurrentPeriodEnd
		if order.Subscription != nil {
			from, until = order.Subscription.CurrentPeriodStart, order.Subscription.CurrentPeriodEnd
		}

		if !until.After(from) {
			return nil, time.Time{}, model.ErrConflict
		}

		id, err := resourceid.New("ord")
		if err != nil {
			return nil, time.Time{}, err
		}

		result = append(result, model.Order{ID: id, ProviderOrderID: order.ID, AmountMinor: order.TotalAmount, PaidAt: order.CreatedAt, ServiceFrom: from, ServiceUntil: until})

		modified := order.CreatedAt
		if order.ModifiedAt != nil {
			modified = *order.ModifiedAt
		}

		if modified.After(newest) {
			newest = modified
		}
	}

	return result, newest, nil
}

func (p *Provider) refunds(ctx context.Context, subscriptionID string) ([]model.Refund, time.Time, error) {
	query := url.Values{"subscription_id": {subscriptionID}, "succeeded": {"true"}, "limit": {"100"}, "sorting": {"created_at"}}

	var response page[polarRefund]
	if err := p.request(ctx, http.MethodGet, "/v1/refunds/?"+query.Encode(), nil, &response); err != nil {
		return nil, time.Time{}, err
	}

	result := make([]model.Refund, 0, len(response.Items))

	var newest time.Time

	for _, refund := range response.Items {
		if refund.Status != "succeeded" || strings.ToUpper(refund.Currency) != "USD" || refund.Amount < 1 {
			continue
		}

		id, err := resourceid.New("rfnd")
		if err != nil {
			return nil, time.Time{}, err
		}

		result = append(result, model.Refund{ID: id, ProviderOrderID: refund.OrderID, ProviderRefundID: refund.ID, AmountMinor: refund.Amount, RefundedAt: refund.CreatedAt})

		modified := refund.CreatedAt
		if refund.ModifiedAt != nil {
			modified = *refund.ModifiedAt
		}

		if modified.After(newest) {
			newest = modified
		}
	}

	return result, newest, nil
}
