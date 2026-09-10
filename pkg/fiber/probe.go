package fx

import (
	"context"
	"fmt"
	"time"

	logx "github.com/chai-rs/handdraw-server/pkg/logger"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
)

// Check reports whether one dependency is available to the server.
// Name must be stable; both methods must be concurrency-safe, and Check must return when ctx is cancelled.
type Check interface {
	Name() string
	Check(ctx context.Context) error
}

// CheckFunc performs one dependency health check.
type CheckFunc func(ctx context.Context) error

type namedCheck struct {
	name string
	fn   CheckFunc
}

// NewCheck names a dependency health check.
func NewCheck(name string, fn CheckFunc) Check {
	return namedCheck{name: name, fn: fn}
}

func (c namedCheck) Name() string {
	return c.name
}

func (c namedCheck) Check(ctx context.Context) error {
	if c.fn == nil {
		return fmt.Errorf("probe %q has no check function", c.name)
	}

	return c.fn(ctx)
}

type probeResponse struct {
	Ready      bool              `json:"ready"`
	Components []componentStatus `json:"components"`
}

type componentStatus struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Ready bool   `json:"ready"`
}

func (s *Server) handleLive(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok"})
}

func (s *Server) handleReady(c fiber.Ctx) error {
	if s.running.Load() && !s.accepting.Load() {
		return c.Status(fiber.StatusServiceUnavailable).JSON(probeResponse{Ready: false})
	}

	return s.handleChecks(c, s.readinessChecks)
}

func (s *Server) handleStartup(c fiber.Ctx) error {
	checks := s.startupChecks
	if len(checks) == 0 {
		checks = s.readinessChecks
	}

	return s.handleChecks(c, checks)
}

func (s *Server) handleChecks(c fiber.Ctx, checks []Check) error {
	components := s.runChecks(c.Context(), checks)
	ready := true

	for _, component := range components {
		if !component.Ready {
			ready = false
			break
		}
	}

	status := fiber.StatusOK
	if !ready {
		status = fiber.StatusServiceUnavailable
	}

	return c.Status(status).JSON(probeResponse{Ready: ready, Components: components})
}

func (s *Server) runChecks(ctx context.Context, checks []Check) []componentStatus {
	type result struct {
		index     int
		component componentStatus
	}

	results := make(chan result, len(checks))
	for i, checker := range checks {
		go func() {
			name := checker.Name()
			started := time.Now()

			checkCtx, cancel := context.WithTimeout(ctx, s.config.ProbeTimeout)
			defer cancel()

			err := runCheck(checkCtx, checker)

			component := componentStatus{Name: name, State: "ready", Ready: err == nil}
			if err != nil {
				component.State = "unavailable"

				logx.Warn().
					Str("component", name).
					Str("request_id", requestid.FromContext(ctx)).
					Dur("duration", time.Since(started)).
					Msg("readiness check failed")
			}

			results <- result{index: i, component: component}
		}()
	}

	components := make([]componentStatus, len(checks))
	for range checks {
		result := <-results
		components[result.index] = result.component
	}

	return components
}

func runCheck(ctx context.Context, checker Check) error {
	return callCheck(ctx, checker)
}

func callCheck(ctx context.Context, checker Check) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("probe panicked: %v", recovered)
		}
	}()

	return checker.Check(ctx)
}
