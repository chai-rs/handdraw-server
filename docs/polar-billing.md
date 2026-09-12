# Polar billing

Handdraw uses four fixed recurring products. Cloud products have a fixed price; Team products use seat-based pricing. Checkout always runs on Polar, requires a payment method, and allows the product's seven-day trial only for a workspace's first eligible subscription.

Set the `APP_BILLING_POLAR_*` variables from `.env.example`. Keep the access token and webhook secret outside Git. The runtime token needs `checkouts:write`, `customer_sessions:write`, `orders:read`, `refunds:read`, `subscriptions:read`, and `subscriptions:write`. Webhook administration can use a separate deployment credential.

Register this callback after the API has a public HTTPS origin:

```text
POST https://<api-origin>/v1/webhooks/polar
```

Use raw webhook format and subscribe to `order.paid`, `order.refunded`, `refund.updated`, and every `subscription.*` event supported by the server. Copy the endpoint's generated secret to `APP_BILLING_POLAR_WEBHOOK_SECRET` before starting the application.

The handler verifies the Standard Webhooks signature over the untouched request body, rejects unsupported events, and commits a deduplicated inbox record before returning `202`. A matching event wakes the existing billing intent. The worker then reads the current subscription, orders, and refunds from Polar before changing Handdraw entitlements.

Enable multiple subscriptions in the Polar organization. Cloud-to-Team changes briefly create a Team subscription and schedule the old Cloud subscription to end at its current period. Team-to-Cloud waits for Team access to end, removes the confirmed excess members, and then creates the Cloud checkout. Disable customer-managed seat and plan changes in Polar Customer Portal; Handdraw owns those reviewed transitions. Keep payment-method updates, invoices, and cancellation available.
