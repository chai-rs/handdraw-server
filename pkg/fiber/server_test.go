package fx

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	errx "github.com/chai-rs/handdraw-server/pkg/error"
	valx "github.com/chai-rs/handdraw-server/pkg/validator"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerMountsApplicationRoutesBesideProbeRoutes(t *testing.T) {
	server, err := New(Params{Routes: func(router fiber.Router) {
		router.Get("/api/v1/example", func(c fiber.Ctx) error {
			return c.JSON(fiber.Map{"ok": true})
		})
	}})
	require.NoError(t, err)

	for _, path := range []string{"/api/v1/example", "/livez", "/readyz", "/startupz"} {
		response := request(t, server, http.MethodGet, path, nil)
		assert.Equal(t, http.StatusOK, response.StatusCode, path)
	}
}

func TestRequestIDIsGeneratedAndPropagatedIntoTheRequestContext(t *testing.T) {
	server, err := New(Params{Routes: func(router fiber.Router) {
		router.Get("/request-id", func(c fiber.Ctx) error {
			generated := requestid.FromContext(c)
			rich := errx.FromContext(c.Context()).Errorf("request context")
			errWithID, ok := errx.AsError(rich)
			if !ok {
				return errors.New("request error context is not an errx error")
			}

			return c.JSON(fiber.Map{
				"middleware": generated,
				"context":    requestid.FromContext(c.Context()),
				"error":      errWithID.RequestID(),
			})
		})
	}})
	require.NoError(t, err)

	response := request(t, server, http.MethodGet, "/request-id", nil)
	requestID := response.Header.Get(fiber.HeaderXRequestID)
	require.NotEmpty(t, requestID)

	var body map[string]string
	require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
	assert.Equal(t, requestID, body["middleware"])
	assert.Equal(t, requestID, body["context"])
	assert.Equal(t, requestID, body["error"])
}

func TestOversizedRequestIDIsReplaced(t *testing.T) {
	server, err := New(Params{})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	incoming := strings.Repeat("x", MaxRequestIDLength+1)
	req.Header.Set(fiber.HeaderXRequestID, incoming)

	response, err := server.app.Test(req)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, response.Body.Close()) })

	generated := response.Header.Get(fiber.HeaderXRequestID)
	assert.NotEmpty(t, generated)
	assert.NotEqual(t, incoming, generated)
	assert.LessOrEqual(t, len(generated), MaxRequestIDLength)
}

func TestCORSIsDisabledByDefault(t *testing.T) {
	server, err := New(Params{})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	req.Header.Set(fiber.HeaderOrigin, "https://app.example.com")
	response, err := server.app.Test(req)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, response.Body.Close()) })

	assert.Empty(t, response.Header.Get(fiber.HeaderAccessControlAllowOrigin))
}

func TestCORSAllowsAConfiguredOriginAndExposesRequestID(t *testing.T) {
	server, err := New(Params{Config: Config{CORS: CORSConfig{
		Enabled:      true,
		AllowOrigins: []string{"https://app.example.com"},
	}}})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/example", nil)
	req.Header.Set(fiber.HeaderOrigin, "https://app.example.com")
	req.Header.Set(fiber.HeaderAccessControlRequestMethod, fiber.MethodPost)
	req.Header.Set(fiber.HeaderAccessControlRequestHeaders, "Content-Type, If-Match, Idempotency-Key")
	response, err := server.app.Test(req)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, response.Body.Close()) })

	assert.Equal(t, "https://app.example.com", response.Header.Get(fiber.HeaderAccessControlAllowOrigin))
	assert.Contains(t, response.Header.Get(fiber.HeaderAccessControlExposeHeaders), fiber.HeaderXRequestID)
	for _, header := range []string{fiber.HeaderETag, fiber.HeaderLocation, fiber.HeaderRetryAfter} {
		assert.Contains(t, response.Header.Get(fiber.HeaderAccessControlExposeHeaders), header)
	}
	for _, header := range []string{fiber.HeaderIfMatch, "Idempotency-Key"} {
		assert.Contains(t, response.Header.Get(fiber.HeaderAccessControlAllowHeaders), header)
	}

	assert.NotEmpty(t, response.Header.Get(fiber.HeaderXRequestID))
}

func TestCORSRejectsWildcardOriginsWithCredentials(t *testing.T) {
	_, err := New(Params{Config: Config{CORS: CORSConfig{
		Enabled:          true,
		AllowOrigins:     []string{wildcardOrigin},
		AllowCredentials: true,
	}}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "credentials")
}

func TestInvalidCORSOriginReturnsAnErrorInsteadOfPanicking(t *testing.T) {
	assert.NotPanics(t, func() {
		_, err := New(Params{Config: Config{CORS: CORSConfig{
			Enabled:      true,
			AllowOrigins: []string{"not an origin"},
		}}})
		require.Error(t, err)
	})
}

type invalidRequest struct {
	Name string `json:"name"`
}

func (r *invalidRequest) Validate() error {
	return valx.Struct(r, valx.Field(&r.Name, valx.Required))
}

func TestBindRunsDTOValidationAndUsesTheSharedErrorResponse(t *testing.T) {
	server, err := New(Params{Routes: func(router fiber.Router) {
		router.Post("/bind", func(c fiber.Ctx) error {
			var input invalidRequest
			if err := c.Bind().JSON(&input); err != nil {
				return err
			}

			return c.SendStatus(fiber.StatusNoContent)
		})
	}})
	require.NoError(t, err)

	response := request(t, server, http.MethodPost, "/bind", strings.NewReader(`{"name":""}`))
	body := assertErrorResponse(
		t,
		response,
		http.StatusBadRequest,
		"invalid_request",
		"invalid request",
	)
	assert.Equal(t, map[string]any{
		"fields": map[string]any{"name": "cannot be blank"},
	}, body.Error.Details)
}

func TestMalformedJSONUsesTheSharedBadRequestResponse(t *testing.T) {
	server, err := New(Params{Routes: func(router fiber.Router) {
		router.Post("/bind", func(c fiber.Ctx) error {
			var input invalidRequest
			return c.Bind().JSON(&input)
		})
	}})
	require.NoError(t, err)

	response := request(t, server, http.MethodPost, "/bind", strings.NewReader(`{"name":`))
	assertErrorResponse(
		t,
		response,
		http.StatusBadRequest,
		"malformed_request",
		"malformed request",
	)
}

func TestErrxStatusAndPublicMessageReachTheCaller(t *testing.T) {
	server, err := New(Params{Routes: func(router fiber.Router) {
		router.Get("/missing", func(fiber.Ctx) error {
			return errx.Code("customer_not_found").
				Status(http.StatusNotFound).
				Public("customer not found").
				Wrap(errors.New("no row"))
		})
	}})
	require.NoError(t, err)

	response := request(t, server, http.MethodGet, "/missing", nil)
	assertErrorResponse(
		t,
		response,
		http.StatusNotFound,
		"customer_not_found",
		"customer not found",
	)
}

func TestUnknownErrorsAreNotExposedToCallers(t *testing.T) {
	server, err := New(Params{Routes: func(router fiber.Router) {
		router.Get("/boom", func(fiber.Ctx) error {
			return errors.New("database password leaked")
		})
	}})
	require.NoError(t, err)

	response := request(t, server, http.MethodGet, "/boom", nil)
	assertErrorResponse(
		t,
		response,
		http.StatusInternalServerError,
		"internal_error",
		"internal server error",
	)
}

func TestFiberServerErrorsAreNotExposedToCallers(t *testing.T) {
	server, err := New(Params{Routes: func(router fiber.Router) {
		router.Get("/fiber-boom", func(fiber.Ctx) error {
			return fiber.NewError(http.StatusInternalServerError, "database password leaked")
		})
	}})
	require.NoError(t, err)

	response := request(t, server, http.MethodGet, "/fiber-boom", nil)
	assertErrorResponse(
		t,
		response,
		http.StatusInternalServerError,
		"internal_error",
		"internal server error",
	)
}

func TestPanicsAreRecoveredWithoutLeakingTheirValue(t *testing.T) {
	server, err := New(Params{Routes: func(router fiber.Router) {
		router.Get("/panic", func(fiber.Ctx) error {
			panic("database password leaked")
		})
	}})
	require.NoError(t, err)

	response := request(t, server, http.MethodGet, "/panic", nil)
	assertErrorResponse(
		t,
		response,
		http.StatusInternalServerError,
		"internal_error",
		"internal server error",
	)
}

func assertErrorResponse(
	t *testing.T,
	response *http.Response,
	status int,
	code string,
	message string,
) Response[any] {
	t.Helper()

	assert.Equal(t, status, response.StatusCode)

	var body Response[any]
	require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
	assert.False(t, body.Success)
	assert.Nil(t, body.Meta.Pagination)
	assert.Nil(t, body.Result)
	require.NotNil(t, body.Error)
	assert.Equal(t, code, body.Error.Code)
	assert.Equal(t, message, body.Error.Message)
	assert.Equal(t, response.Header.Get(fiber.HeaderXRequestID), body.Meta.RequestID)

	return body
}

func request(t *testing.T, server *Server, method, path string, body *strings.Reader) *http.Response {
	t.Helper()

	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, body)
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	}

	response, err := server.app.Test(req)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, response.Body.Close()) })

	return response
}
