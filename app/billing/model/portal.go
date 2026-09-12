package model

import "context"

// PortalRepository resolves only an Owner-authorized workspace customer reference.
//
//mockery:generate: true
type PortalRepository interface {
	PortalCustomer(context.Context, string) (string, error)
}

// PortalProvider creates a short-lived customer portal session.
//
//mockery:generate: true
type PortalProvider interface {
	CreatePortal(context.Context, string) (string, error)
}
