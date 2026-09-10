package fx

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunReturnsWithoutListeningWhenContextIsAlreadyCancelled(t *testing.T) {
	server, err := New(Params{Config: Config{Address: "127.0.0.1:0"}})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		require.FailNow(t, "server did not honor the cancelled context")
	}
}

func TestRunGracefullyStopsAfterContextCancellation(t *testing.T) {
	address := availableAddress(t)
	server, err := New(Params{Config: Config{
		Address:         address,
		ShutdownTimeout: time.Second,
	}})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	waitUntilListening(t, address)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "server did not shut down within its deadline")
	}
}

func TestRunDrainsAnInFlightRequestBeforeReturning(t *testing.T) {
	server, address, started, release := newBlockingServer(t, time.Second)
	ctx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(ctx) }()
	waitUntilListening(t, address)

	responseDone := make(chan *http.Response, 1)
	requestError := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + address + "/block")
		if requestErr != nil {
			requestError <- requestErr
			return
		}
		responseDone <- response
	}()
	<-started
	cancel()

	select {
	case err := <-runDone:
		require.NoError(t, err)
		require.FailNow(t, "server returned before the in-flight request completed")
	case <-time.After(30 * time.Millisecond):
	}

	close(release)
	select {
	case response := <-responseDone:
		t.Cleanup(func() { require.NoError(t, response.Body.Close()) })
		assert.Equal(t, http.StatusNoContent, response.StatusCode)
	case err := <-requestError:
		require.NoError(t, err)
	}
	require.NoError(t, <-runDone)
}

func TestRunForcesShutdownAfterTheDeadline(t *testing.T) {
	server, address, started, release := newBlockingServer(t, 20*time.Millisecond)
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(ctx) }()
	waitUntilListening(t, address)

	go func() {
		response, _ := http.Get("http://" + address + "/block")
		if response != nil {
			_ = response.Body.Close()
		}
	}()
	<-started
	cancel()

	select {
	case err := <-runDone:
		require.Error(t, err)
	case <-time.After(time.Second):
		require.FailNow(t, "server did not force shutdown after its deadline")
	}
}

func TestRunDrainsHTTPBeforeShuttingDownTools(t *testing.T) {
	toolStopped := make(chan struct{})
	tool := &testTool{
		name: "worker",
		shutdown: func(context.Context) error {
			close(toolStopped)
			return nil
		},
	}
	server, address, requestStarted, releaseRequest := newBlockingServerWithTools(
		t,
		time.Second,
		[]Tool{tool},
	)
	ctx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(ctx) }()
	waitUntilListening(t, address)

	go func() {
		response, _ := http.Get("http://" + address + "/block")
		if response != nil {
			_ = response.Body.Close()
		}
	}()
	<-requestStarted
	cancel()

	select {
	case <-toolStopped:
		require.FailNow(t, "tool stopped before the in-flight request drained")
	case <-time.After(30 * time.Millisecond):
	}

	close(releaseRequest)
	require.NoError(t, <-runDone)
	select {
	case <-toolStopped:
	case <-time.After(time.Second):
		require.FailNow(t, "tool was not stopped after HTTP drained")
	}
}

func TestRunShutsToolsDownInReverseStartOrder(t *testing.T) {
	var (
		mu    sync.Mutex
		order []string
	)
	newTool := func(name string) Tool {
		return &testTool{
			name: name,
			start: func(context.Context) error {
				mu.Lock()
				defer mu.Unlock()
				order = append(order, "start "+name)
				return nil
			},
			shutdown: func(context.Context) error {
				mu.Lock()
				defer mu.Unlock()
				order = append(order, "stop "+name)
				return nil
			},
		}
	}
	address := availableAddress(t)
	server, err := New(Params{
		Config: Config{Address: address},
		Tools:  []Tool{newTool("first"), newTool("second")},
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(ctx) }()
	waitUntilListening(t, address)
	cancel()
	require.NoError(t, <-runDone)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"start first", "start second", "stop second", "stop first"}, order)
}

func TestRunRollsBackStartedToolsWhenStartupFails(t *testing.T) {
	var order []string
	first := &testTool{
		name:  "first",
		start: func(context.Context) error { order = append(order, "start first"); return nil },
		shutdown: func(context.Context) error {
			order = append(order, "stop first")
			return nil
		},
	}
	second := &testTool{
		name:  "second",
		start: func(context.Context) error { order = append(order, "start second"); return errors.New("boom") },
		shutdown: func(context.Context) error {
			order = append(order, "stop second")
			return nil
		},
	}
	server, err := New(Params{Tools: []Tool{first, second}})
	require.NoError(t, err)

	err = server.Run(t.Context())
	require.Error(t, err)
	assert.ErrorContains(t, err, "start tool \"second\"")
	assert.Equal(t, []string{"start first", "start second", "stop second", "stop first"}, order)
}

func TestRunStopsWhenAToolExitsUnexpectedly(t *testing.T) {
	exited := make(chan error, 1)
	tool := &testTool{name: "worker", done: exited}
	address := availableAddress(t)
	server, err := New(Params{Config: Config{Address: address}, Tools: []Tool{tool}})
	require.NoError(t, err)
	runDone := make(chan error, 1)
	go func() { runDone <- server.Run(t.Context()) }()
	waitUntilListening(t, address)

	exited <- errors.New("worker failed")
	err = <-runDone
	require.Error(t, err)
	assert.ErrorContains(t, err, "tool \"worker\" stopped")
	assert.ErrorContains(t, err, "worker failed")
}

func newBlockingServer(
	t *testing.T,
	shutdownTimeout time.Duration,
) (*Server, string, <-chan struct{}, chan<- struct{}) {
	t.Helper()

	return newBlockingServerWithTools(t, shutdownTimeout, nil)
}

func newBlockingServerWithTools(
	t *testing.T,
	shutdownTimeout time.Duration,
	tools []Tool,
) (*Server, string, <-chan struct{}, chan<- struct{}) {
	t.Helper()

	started := make(chan struct{})
	release := make(chan struct{})
	address := availableAddress(t)
	server, err := New(Params{
		Config: Config{Address: address, ShutdownTimeout: shutdownTimeout},
		Tools:  tools,
		Routes: func(router fiber.Router) {
			router.Get("/block", func(c fiber.Ctx) error {
				close(started)
				<-release
				return c.SendStatus(fiber.StatusNoContent)
			})
		},
	})
	require.NoError(t, err)

	return server, address, started, release
}

type testTool struct {
	name     string
	start    func(context.Context) error
	done     <-chan error
	shutdown func(context.Context) error
}

// Name identifies the test tool.
func (t *testTool) Name() string {
	return t.name
}

// Start invokes the configured startup behavior.
func (t *testTool) Start(ctx context.Context) error {
	if t.start == nil {
		return nil
	}

	return t.start(ctx)
}

// Done exposes the configured exit channel.
func (t *testTool) Done() <-chan error {
	return t.done
}

// Shutdown invokes the configured shutdown behavior.
func (t *testTool) Shutdown(ctx context.Context) error {
	if t.shutdown == nil {
		return nil
	}

	return t.shutdown(ctx)
}

func waitUntilListening(t *testing.T, address string) {
	t.Helper()

	require.Eventually(t, func() bool {
		connection, err := net.DialTimeout("tcp", address, 10*time.Millisecond)
		if err != nil {
			return false
		}
		_ = connection.Close()
		return true
	}, time.Second, 5*time.Millisecond)
}

func availableAddress(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	return address
}

func TestHTTPBodyLimitRejectsOversizedJSON(t *testing.T) {
	address := availableAddress(t)
	server, err := New(Params{Config: Config{Address: address}, Routes: func(router fiber.Router) {
		router.Post("/limited", func(c fiber.Ctx) error { assert.Fail(t, "oversized request reached handler"); return nil })
	}})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			assert.Fail(t, "server did not stop")
		}
	})
	waitUntilListening(t, address)
	req, err := http.NewRequest(http.MethodPost, "http://"+address+"/limited", strings.NewReader(strings.Repeat("x", 65537)))
	require.NoError(t, err)
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	client := &http.Client{Timeout: time.Second}
	response, err := client.Do(req)
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()
	require.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
	require.Equal(t, "private, no-store", response.Header.Get(fiber.HeaderCacheControl))
	var body Response[any]
	require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
	require.Equal(t, "request_too_large", body.Error.Code)
	require.NotEmpty(t, body.Meta.RequestID)
}
