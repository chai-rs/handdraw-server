// Package service drains transactional cleanup jobs with bounded polling and cancellation.
package service

import (
	"context"
	"time"

	"github.com/chai-rs/handdraw-server/internal/job/model"
)

// Worker polls the narrow cleanup port; retry/backoff state survives process restarts in PostgreSQL.
type Worker struct {
	port    model.Worker
	timeout time.Duration
}

// NewWorker receives the separately credentialed cleanup adapter.
func NewWorker(port model.Worker) *Worker { return &Worker{port: port, timeout: 5 * time.Second} }

// Run limits each pass to ten jobs and reports transient database failures without dropping queued work.
func (w *Worker) Run(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for range 10 {
				jobCtx, cancel := context.WithTimeout(ctx, w.timeout)
				n, err := w.port.RunOne(jobCtx)

				cancel()

				if err != nil {
					if ctx.Err() == nil {
						onError(err)
					}

					break
				}

				if n == 0 {
					break
				}
			}
		}
	}
}

// WithTimeout bounds a worker that performs external transfer I/O; ordinary cleanup keeps its short default.
func (w *Worker) WithTimeout(timeout time.Duration) *Worker {
	if timeout > 0 && timeout <= 90*time.Second {
		w.timeout = timeout
	}

	return w
}
