// Package txer states the transaction boundary as a port, so a service can say
// "these writes commit together" without naming a database or importing a driver.
// The bun package holds the implementation that satisfies it.
package txer

import "context"

// TransactionFn is the work to run inside a transaction. It takes a context and
// nothing else because the transaction itself travels on that context, which is
// what keeps the signature free of any database type.
type TransactionFn func(ctx context.Context) error

// Transactioner runs a function inside a transaction, committing when it returns
// nil and rolling back when it returns an error.
//
//mockery:generate: true
type Transactioner interface {
	RunInTx(ctx context.Context, fn TransactionFn) error
}
