// Package contracttest validates the portable OpenAPI contract and representative HTTP requests.
package contracttest_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/chai-rs/handdraw-server/internal/identity/inbound/api/dto"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/google/uuid"

	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/stretchr/testify/require"
)

func specification(t *testing.T) *openapi3.T {
	t.Helper()
	openapi3.DefineStringFormatValidator("handdraw-resource-id", openapi3.NewCallbackValidator(func(value string) error {
		prefix, _, ok := strings.Cut(value, "_")
		if !ok {
			return resourceid.ErrInvalid
		}
		return resourceid.Validate(value, prefix)
	}))
	openapi3.DefineStringFormatValidator("handdraw-revision", openapi3.NewCallbackValidator(func(value string) error {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 1 || strconv.FormatInt(n, 10) != value {
			return errors.New("invalid revision")
		}
		return nil
	}))
	openapi3.DefineStringFormatValidator("handdraw-idempotency-key", openapi3.NewCallbackValidator(func(value string) error {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return errors.New("invalid idempotency key")
		}
		return nil
	}))
	openapi3.DefineStringFormatValidator("handdraw-etag", openapi3.NewCallbackValidator(func(value string) error { _, err := fx.ParseIfMatch(value); return err }))
	loader := openapi3.NewLoader()
	document, err := loader.LoadFromFile("../../contracts/openapi.json")
	require.NoError(t, err)
	require.NoError(t, document.Validate(t.Context()))
	return document
}

// TestOpenAPIRequestsRejectInvalidMutationContracts checks real routing, schemas, headers and path formats.
func TestOpenAPIRequestsRejectInvalidMutationContracts(t *testing.T) {
	document := specification(t)
	router, err := legacy.NewRouter(document)
	require.NoError(t, err)
	const workspace = "ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv"
	for _, tc := range []struct {
		name, method, path, body, ifMatch, key string
		valid                                  bool
	}{
		{"create board", "POST", "/v1/workspaces/" + workspace + "/boards", `{"name":"Architecture","project_id":null,"initialization":"empty"}`, "", "52ef1b34-0161-49b2-8107-5adad101fb4c", true},
		{"zero idempotency", "POST", "/v1/workspaces/" + workspace + "/boards", `{"name":"Architecture","project_id":null,"initialization":"empty"}`, "", "00000000-0000-0000-0000-000000000000", false},
		{"unknown field", "POST", "/v1/workspaces/" + workspace + "/boards", `{"name":"Architecture","project_id":null,"initialization":"empty","role":"owner"}`, "", "52ef1b34-0161-49b2-8107-5adad101fb4c", false},
		{"missing idempotency", "POST", "/v1/workspaces/" + workspace + "/boards", `{"name":"Architecture","project_id":null,"initialization":"empty"}`, "", "", false},
		{"missing revision", "PATCH", "/v1/boards/brd_0ujtsYcgvSTl8PAuAdqWYSMnLOv", `{"name":"New"}`, "", "", false},
		{"strong revision", "PATCH", "/v1/boards/brd_0ujtsYcgvSTl8PAuAdqWYSMnLOv", `{"project_id":null}`, `"1"`, "", true},
		{"weak revision", "PATCH", "/v1/boards/brd_0ujtsYcgvSTl8PAuAdqWYSMnLOv", `{"name":"New"}`, `W/"1"`, "", false},
		{"empty patch", "PATCH", "/v1/boards/brd_0ujtsYcgvSTl8PAuAdqWYSMnLOv", `{}`, `"1"`, "", false},
		{"overflow ID", "GET", "/v1/boards/brd_zzzzzzzzzzzzzzzzzzzzzzzzzzz", "", "", "", false},
		{"wrong prefix", "GET", "/v1/boards/ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv", "", "", "", false},
		{"page limit", "GET", "/v1/workspaces/" + workspace + "/boards?limit=101", "", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, "http://127.0.0.1:8081"+tc.path, strings.NewReader(tc.body))
			request.Header.Set("Authorization", "Bearer contract-fixture")
			if tc.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			if tc.key != "" {
				request.Header.Set("Idempotency-Key", tc.key)
			}
			if tc.ifMatch != "" {
				request.Header.Set("If-Match", tc.ifMatch)
			}
			route, params, err := router.FindRoute(request)
			require.NoError(t, err)
			err = openapi3filter.ValidateRequest(t.Context(), &openapi3filter.RequestValidationInput{Request: request, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: func(context.Context, *openapi3filter.AuthenticationInput) error { return nil }}})
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// TestOpenAPIDocumentationMatchesPortableContract prevents drift when the shared docs checkout exists.
func TestOpenAPIDocumentationMatchesPortableContract(t *testing.T) {
	canonical, err := os.ReadFile("../../contracts/openapi.json")
	require.NoError(t, err)
	docs, err := os.ReadFile("../../../docs/architecture/openapi.yaml")
	if os.IsNotExist(err) {
		return
	}
	require.NoError(t, err)
	require.JSONEq(t, string(canonical), string(docs))
}

// TestPublicResponseShapes checks the actual shared envelope and identity DTO against the contract.
func TestPublicResponseShapes(t *testing.T) {
	spec := specification(t)
	me := dto.Me{ID: "usr_0ujtsYcgvSTl8PAuAdqWYSMnLOv", DisplayName: "Developer", Email: "developer@example.test"}
	raw, err := json.Marshal(fx.Response[dto.Me]{Success: true, Meta: fx.Meta{RequestID: "request-1"}, Result: &me})
	require.NoError(t, err)
	var value any
	require.NoError(t, json.Unmarshal(raw, &value))
	require.NoError(t, spec.Components.Schemas["MeResponse"].Value.VisitJSON(value))
	for _, value := range []any{float64(42), "0", "01", "9223372036854775808"} {
		require.Error(t, spec.Components.Schemas["Revision"].Value.VisitJSON(value))
	}
	require.NoError(t, spec.Components.Schemas["Revision"].Value.VisitJSON("9223372036854775807"))
}

// TestOpenAPICategoriesGroupOperationsByResponsibility keeps implementation flags out of the category names.
func TestOpenAPICategoriesGroupOperationsByResponsibility(t *testing.T) {
	spec := specification(t)
	known := map[string]bool{}
	for _, tag := range spec.Tags {
		known[tag.Name] = true
	}
	for _, item := range spec.Paths.Map() {
		for _, operation := range item.Operations() {
			require.Len(t, operation.Tags, 1)
			require.True(t, known[operation.Tags[0]])
			require.NotContains(t, operation.Tags, "Available when enabled")
		}
	}
	require.Equal(t, []string{"Members"}, spec.Paths.Value("/v1/workspaces/{workspace_id}/members").Get.Tags)
	require.Equal(t, []string{"Invitations"}, spec.Paths.Value("/v1/invitations/accept").Post.Tags)
}
