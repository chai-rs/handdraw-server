package fx

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSuccessWrapsAResultWithRequestMetadata(t *testing.T) {
	server, err := New(Params{Routes: func(router fiber.Router) {
		router.Get("/resource", func(c fiber.Ctx) error {
			return Success(c, map[string]string{"id": "resource-1"})
		})
	}})
	require.NoError(t, err)

	response := request(t, server, http.MethodGet, "/resource", nil)
	assert.Equal(t, http.StatusOK, response.StatusCode)

	var body Response[map[string]string]
	require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
	require.True(t, body.Success)
	require.Equal(t, response.Header.Get(fiber.HeaderXRequestID), body.Meta.RequestID)
	require.NotNil(t, body.Result)
	assert.Equal(t, "resource-1", (*body.Result)["id"])
	assert.NotEmpty(t, response.Header.Get(fiber.HeaderXRequestID))
	assert.Nil(t, body.Error)
}

func TestCreatedPreservesStatusAndLocation(t *testing.T) {
	server, err := New(Params{Routes: func(router fiber.Router) {
		router.Post("/resources", func(c fiber.Ctx) error {
			return Created(c, "/resources/resource-1", map[string]string{"id": "resource-1"})
		})
	}})
	require.NoError(t, err)

	response := request(t, server, http.MethodPost, "/resources", nil)
	assert.Equal(t, http.StatusCreated, response.StatusCode)
	assert.Equal(t, "/resources/resource-1", response.Header.Get(fiber.HeaderLocation))
	var body map[string]any
	require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
	require.Equal(t, map[string]any{
		"success": true, "meta": map[string]any{"request_id": response.Header.Get(fiber.HeaderXRequestID)},
		"result": map[string]any{"id": "resource-1"},
	}, body)
}

func TestPaginatedReturnsAnEmptyArrayAndNullCursor(t *testing.T) {
	server, err := New(Params{Routes: func(router fiber.Router) {
		router.Get("/resources", func(c fiber.Ctx) error {
			return Paginated(c, []string(nil), Pagination{
				NextCursor: nil,
			})
		})
	}})
	require.NoError(t, err)

	response := request(t, server, http.MethodGet, "/resources", nil)

	var body Response[[]string]
	require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
	require.True(t, body.Success)
	require.Equal(t, response.Header.Get(fiber.HeaderXRequestID), body.Meta.RequestID)
	require.NotNil(t, body.Result)
	assert.Empty(t, *body.Result)
	require.NotNil(t, body.Meta.Pagination)
	assert.Equal(t, Pagination{NextCursor: nil}, *body.Meta.Pagination)
	assert.Nil(t, body.Error)
}

func TestSnakeCasePreservesAcronymBoundaries(t *testing.T) {
	tests := map[string]string{
		"Name":              "name",
		"AccountID":         "account_id",
		"CreatedAtGTEMs":    "created_at_gte_ms",
		"WeightConvergence": "weight_convergence",
	}

	for input, expected := range tests {
		t.Run(input, func(t *testing.T) {
			assert.Equal(t, expected, snakeCase(input))
		})
	}
}
