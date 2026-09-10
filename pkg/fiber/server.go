// Package fx adapts the shared Fiber server to Handdraw HTTP contracts.
package fx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"sync/atomic"

	errx "github.com/chai-rs/handdraw-server/pkg/error"
	"github.com/gofiber/fiber/v3"
)

// Params contains the server configuration, health checks, application routes, and owned tools.
type Params struct {
	Config          Config
	Routes          func(router fiber.Router)
	ReadinessChecks []Check
	StartupChecks   []Check
	Tools           []Tool
}

// Server owns the Fiber application and the lifecycle of its tools.
type Server struct {
	app             *fiber.App
	config          Config
	readinessChecks []Check
	startupChecks   []Check
	tools           []Tool
	running         atomic.Bool
	accepting       atomic.Bool
}

type toolExit struct {
	name string
	err  error
}

// New builds a configured server and mounts middleware, probes, then application routes.
func New(params Params) (*Server, error) {
	config := params.Config.withDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}

	if err := validateChecks("readiness", params.ReadinessChecks); err != nil {
		return nil, err
	}

	if err := validateChecks("startup", params.StartupChecks); err != nil {
		return nil, err
	}

	if err := validateTools(params.Tools); err != nil {
		return nil, err
	}

	server := &Server{
		config:          config,
		readinessChecks: slices.Clone(params.ReadinessChecks),
		startupChecks:   slices.Clone(params.StartupChecks),
		tools:           slices.Clone(params.Tools),
	}
	server.app = fiber.New(fiber.Config{
		AppName:             config.AppName,
		BodyLimit:           config.BodyLimit,
		JSONDecoder:         decodeJSON,
		ReadTimeout:         config.ReadTimeout,
		WriteTimeout:        config.WriteTimeout,
		IdleTimeout:         config.IdleTimeout,
		PassLocalsToContext: true,
		ErrorHandler:        errorHandler,
		StructValidator:     structValidator{},
	})

	if err := registerMiddleware(server.app, config); err != nil {
		return nil, err
	}

	server.registerProbes()

	if params.Routes != nil {
		params.Routes(server.app)
	}

	return server, nil
}

// Run starts the owned tools, listens until cancellation or an unexpected exit,
// then drains HTTP before shutting the tools down in reverse order.
func (s *Server) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	if ctx.Err() != nil {
		return nil
	}

	toolCtx, cancelTools := context.WithCancel(context.WithoutCancel(ctx))

	started, err := s.startTools(toolCtx)
	if err != nil {
		cancelTools()
		return errors.Join(err, s.shutdownTools(started))
	}

	listener, err := net.Listen("tcp", s.config.Address)
	if err != nil {
		cancelTools()
		return errors.Join(errx.Wrap(err), s.shutdownTools(started))
	}

	listenerDone := make(chan error, 1)
	go func() {
		listenerDone <- s.listen(listener)
	}()

	toolDone := s.watchTools(toolCtx, started)
	s.running.Store(true)
	s.accepting.Store(true)

	var (
		runErr         error
		listenerExited bool
	)

	select {
	case listenErr := <-listenerDone:
		listenerExited = true

		if ctx.Err() == nil {
			runErr = normalizeListenerError(listenErr)
			if runErr == nil {
				runErr = errors.New("fiber: HTTP server stopped unexpectedly")
			}
		}
	case exit := <-toolDone:
		if ctx.Err() == nil {
			runErr = exit.err
			if runErr == nil {
				runErr = fmt.Errorf("fiber: tool %q stopped unexpectedly", exit.name)
			} else {
				runErr = fmt.Errorf("fiber: tool %q stopped: %w", exit.name, runErr)
			}
		}
	case <-ctx.Done():
	}

	s.accepting.Store(false)
	httpErr := s.shutdownHTTP(listener, listenerDone, listenerExited)

	cancelTools()

	toolErr := s.shutdownTools(started)
	s.running.Store(false)

	return errors.Join(runErr, httpErr, toolErr)
}

// Shutdown drains in-flight requests until ctx expires.
func (s *Server) Shutdown(ctx context.Context) error {
	return errx.Wrap(s.app.ShutdownWithContext(ctx))
}

func (s *Server) startTools(ctx context.Context) ([]Tool, error) {
	started := make([]Tool, 0, len(s.tools))
	for _, tool := range s.tools {
		started = append(started, tool)
		if err := tool.Start(ctx); err != nil {
			return started, fmt.Errorf("fiber: start tool %q: %w", tool.Name(), err)
		}
	}

	return started, nil
}

func (s *Server) watchTools(ctx context.Context, tools []Tool) <-chan toolExit {
	exits := make(chan toolExit, len(tools))
	for _, tool := range tools {
		done := tool.Done()

		if done == nil {
			continue
		}

		go func() {
			var err error
			select {
			case <-ctx.Done():
				return
			case err = <-done:
			}

			exits <- toolExit{name: tool.Name(), err: err}
		}()
	}

	return exits
}

func (s *Server) shutdownHTTP(
	listener net.Listener,
	listenerDone <-chan error,
	listenerExited bool,
) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
	defer cancel()

	shutdownErr := s.Shutdown(shutdownCtx)
	closeErr := listener.Close()

	var listenErr error
	if !listenerExited {
		listenErr = <-listenerDone
	}

	return errors.Join(
		shutdownErr,
		normalizeListenerError(closeErr),
		normalizeListenerError(listenErr),
	)
}

func (s *Server) shutdownTools(tools []Tool) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.config.ToolShutdownTimeout)
	defer cancel()

	var shutdownErr error

	for _, tool := range slices.Backward(tools) {
		if err := tool.Shutdown(shutdownCtx); err != nil {
			shutdownErr = errors.Join(
				shutdownErr,
				fmt.Errorf("fiber: shutdown tool %q: %w", tool.Name(), err),
			)
		}
	}

	return shutdownErr
}

func (s *Server) listen(listener net.Listener) error {
	return errx.Wrap(s.app.Listener(listener, fiber.ListenConfig{
		DisableStartupMessage: true,
	}))
}

func normalizeListenerError(err error) error {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return nil
	}

	return err
}

func (s *Server) registerProbes() {
	s.app.Get("/livez", s.handleLive)
	s.app.Get("/readyz", s.handleReady)
	s.app.Get("/startupz", s.handleStartup)
}

func validateChecks(kind string, checks []Check) error {
	names := make(map[string]struct{}, len(checks))
	for i, checker := range checks {
		if isNilCheck(checker) {
			return fmt.Errorf("fiber: %s check %d is nil", kind, i)
		}

		name := checker.Name()
		if name == "" {
			return fmt.Errorf("fiber: %s check %d has no name", kind, i)
		}

		if _, exists := names[name]; exists {
			return fmt.Errorf("fiber: duplicate %s check %q", kind, name)
		}

		names[name] = struct{}{}
	}

	return nil
}

func isNilCheck(checker Check) bool {
	if checker == nil {
		return true
	}

	value := reflect.ValueOf(checker)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
