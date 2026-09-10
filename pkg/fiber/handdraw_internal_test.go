package fx

// These tests need the private Fiber app to exercise HTTP contracts without opening a listener.
import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	boardapi "github.com/chai-rs/handdraw-server/internal/board/inbound/api"
	"github.com/chai-rs/handdraw-server/internal/board/model"
	"github.com/chai-rs/handdraw-server/internal/board/model/mocks"
	"github.com/chai-rs/handdraw-server/internal/board/service"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestBoardServiceErrorsUseHanddrawHTTPContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cause  error
		status int
		code   string
	}{
		{"hidden", model.ErrNotFound, 404, "not_found"},
		{"stale", model.ErrRevisionConflict, 412, "revision_conflict"},
		{"forbidden", model.ErrPermissionDenied, 403, "permission_denied"},
		{"not empty", model.ErrProjectNotEmpty, 409, "project_not_empty"},
		{"unexpected", errors.New("password=must-not-leak"), 500, "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := mocks.NewMockBoardRepository(t)
			ws, err := resourceid.New(model.WorkspaceIDPrefix)
			require.NoError(t, err)
			id, err := resourceid.New(model.BoardIDPrefix)
			require.NoError(t, err)
			repo.EXPECT().Get(mock.Anything, ws, id).Return(model.Board{}, tc.cause).Once()
			svc := service.NewBoardService(repo)
			server, err := New(Params{Routes: func(router fiber.Router) {
				router.Get("/board", boardapi.Handle(func(c fiber.Ctx) error {
					_, err := svc.Get(c.Context(), ws, id)
					return err
				}))
			}})
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodGet, "/board", nil)
			req.Header.Set(fiber.HeaderXRequestID, "client-chosen-id")
			response, err := server.app.Test(req)
			require.NoError(t, err)
			defer func() { require.NoError(t, response.Body.Close()) }()
			require.Equal(t, tc.status, response.StatusCode)
			require.Equal(t, "private, no-store", response.Header.Get(fiber.HeaderCacheControl))
			var body map[string]any
			require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
			require.Len(t, body, 3)
			require.Equal(t, false, body["success"])
			meta := body["meta"].(map[string]any)
			detail := body["error"].(map[string]any)
			require.Equal(t, tc.code, detail["code"])
			require.Equal(t, response.Header.Get(fiber.HeaderXRequestID), meta["request_id"])
			require.NotEqual(t, "client-chosen-id", meta["request_id"])
			require.NotContains(t, detail["message"], "must-not-leak")
			require.NotContains(t, detail, "request_id")
			require.NotContains(t, body, "result")
		})
	}
}

func TestJSONBindingRejectsUnknownTrailingAndInvalidUTF8(t *testing.T) {
	server, err := New(Params{Routes: func(router fiber.Router) {
		router.Post("/bind", func(c fiber.Ctx) error {
			var dto struct {
				Name string `json:"name"`
			}
			if err := c.Bind().JSON(&dto); err != nil {
				return err
			}
			return Success(c, dto)
		})
	}})
	require.NoError(t, err)
	for _, body := range []string{`{"name":"ok","extra":true}`, `{"name":"ok"} {}`, "{\"name\":\"" + string([]byte{0xff}) + "\"}"} {
		req := httptest.NewRequest(http.MethodPost, "/bind", strings.NewReader(body))
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		response, err := server.app.Test(req)
		require.NoError(t, err)
		require.Equal(t, 400, response.StatusCode)
		require.NoError(t, response.Body.Close())
	}
}

func TestPaginatedContractUsesExpressoEnvelopeAndKeysetMetadata(t *testing.T) {
	server, err := New(Params{Routes: func(router fiber.Router) {
		router.Get("/list", func(c fiber.Ctx) error { return Paginated(c, []string(nil), Pagination{}) })
	}})
	require.NoError(t, err)
	response := request(t, server, http.MethodGet, "/list", nil)
	var body map[string]any
	require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
	require.Equal(t, map[string]any{"success": true, "result": []any{}, "meta": map[string]any{"request_id": response.Header.Get(fiber.HeaderXRequestID), "pagination": map[string]any{"next_cursor": nil}}}, body)
}
