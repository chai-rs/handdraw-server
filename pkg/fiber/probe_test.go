package fx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type pointerCheck struct {
	name string
}

func (c *pointerCheck) Name() string {
	return c.name
}

func (*pointerCheck) Check(context.Context) error {
	return nil
}

func TestNewRejectsATypedNilCheck(t *testing.T) {
	var checker *pointerCheck

	assert.NotPanics(t, func() {
		_, err := New(Params{ReadinessChecks: []Check{checker}})
		require.Error(t, err)
	})
}

func TestLivenessDoesNotCallDependencyChecks(t *testing.T) {
	var calls atomic.Int64
	server, err := New(Params{ReadinessChecks: []Check{
		NewCheck("postgres", func(context.Context) error {
			calls.Add(1)
			return errors.New("down")
		}),
	}})
	require.NoError(t, err)

	response := request(t, server, http.MethodGet, "/livez", nil)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Zero(t, calls.Load())
}

func TestReadinessReportsUnavailableWithoutLeakingTheCheckError(t *testing.T) {
	server, err := New(Params{ReadinessChecks: []Check{
		NewCheck("postgres", func(context.Context) error {
			return errors.New("password=secret")
		}),
	}})
	require.NoError(t, err)

	response := request(t, server, http.MethodGet, "/readyz", nil)
	assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)

	encoded, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "password=secret")

	body := make(map[string]any)
	require.NoError(t, json.Unmarshal(encoded, &body))
	components, ok := body["components"].([]any)
	require.True(t, ok)
	require.Len(t, components, 1)
	assert.Equal(t, map[string]any{
		"name": "postgres", "state": "unavailable", "ready": false,
	}, components[0])
}

func TestReadinessChecksRunInParallel(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	check := func(name string) Check {
		return NewCheck(name, func(context.Context) error {
			started <- name
			<-release
			return nil
		})
	}

	server, err := New(Params{ReadinessChecks: []Check{check("postgres"), check("redis")}})
	require.NoError(t, err)

	responseDone := make(chan *http.Response, 1)
	errorDone := make(chan error, 1)
	go func() {
		response, requestErr := server.app.Test(httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if requestErr != nil {
			errorDone <- requestErr
			return
		}
		responseDone <- response
	}()

	seen := map[string]bool{}
	for range 2 {
		select {
		case name := <-started:
			seen[name] = true
		case err := <-errorDone:
			require.NoError(t, err)
		case <-time.After(time.Second):
			require.FailNow(t, "readiness checks did not start together")
		}
	}
	close(release)

	response := <-responseDone
	t.Cleanup(func() { require.NoError(t, response.Body.Close()) })
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, map[string]bool{"postgres": true, "redis": true}, seen)
}

func TestReadinessBoundsEachCheckByTheProbeTimeout(t *testing.T) {
	server, err := New(Params{
		Config: Config{ProbeTimeout: 20 * time.Millisecond},
		ReadinessChecks: []Check{NewCheck("slow", func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})},
	})
	require.NoError(t, err)

	started := time.Now()
	response := request(t, server, http.MethodGet, "/readyz", nil)
	assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	assert.Less(t, time.Since(started), time.Second)
}

func TestStartupFallsBackToReadinessChecks(t *testing.T) {
	var calls atomic.Int64
	server, err := New(Params{ReadinessChecks: []Check{
		NewCheck("postgres", func(context.Context) error {
			calls.Add(1)
			return nil
		}),
	}})
	require.NoError(t, err)

	response := request(t, server, http.MethodGet, "/startupz", nil)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, int64(1), calls.Load())
}

func TestReadinessWithNoChecksReturnsAnEmptyComponentList(t *testing.T) {
	server, err := New(Params{})
	require.NoError(t, err)

	response := request(t, server, http.MethodGet, "/readyz", nil)

	var body struct {
		Components []componentStatus `json:"components"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
	assert.NotNil(t, body.Components)
	assert.Empty(t, body.Components)
}
