//go:build integration

package db_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	assetdb "github.com/chai-rs/handdraw-server/app/asset_management/infra/db"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	b, e := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jfKkAAAAASUVORK5CYII=")
	require.NoError(t, e)
	return b
}

func reservePayload(t *testing.T, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	b, e := json.Marshal(map[string]any{"purpose": "attachment", "size_bytes": len(data), "mime_type": "image/png", "sha256": hex.EncodeToString(sum[:])})
	require.NoError(t, e)
	return string(b)
}

// TestAssetFinalizationIsImmutableAndCountsOnce covers HTTP bytes, retry, range and post-revocation denial.
func (s *accessSuite) TestAssetFinalizationIsImmutableAndCountsOnce() {
	t := s.T()
	f := s.setup(t)
	base, token := s.http(t)
	board := s.discussionBoard(t, f, base, token(f.owner))
	data := tinyPNG(t)
	route := base + "/v1/boards/" + board + "/assets"
	payload := reservePayload(t, data)
	key := uuid.NewString()
	status, body, _ := request(t, "POST", route, token(f.viewer), payload, "", uuid.NewString())
	require.Equal(t, 403, status, body)
	status, body, _ = request(t, "POST", route, token(f.owner), payload, "", key)
	require.Equal(t, 201, status, body)
	id := body["result"].(map[string]any)["id"].(string)
	status, body, _ = request(t, "POST", route, token(f.owner), payload, "", key)
	require.Equal(t, 201, status, body)
	require.Equal(t, id, body["result"].(map[string]any)["id"])
	path := base + "/v1/assets/" + id
	status, body, _ = request(t, "POST", path+"/complete", token(f.owner), "", "")
	require.Equal(t, 503, status, body)
	status, body, _ = request(t, "PUT", path+"/upload", token(f.owner), "wrong bytes", "")
	require.Equal(t, 409, status, body)
	status, body, _ = request(t, "PUT", path+"/upload", token(f.owner), string(data), "")
	require.Equal(t, 200, status, body)
	for range 2 {
		status, body, _ = request(t, "POST", path+"/complete", token(f.owner), "", "")
		require.Equal(t, 200, status, body)
	}
	var usage struct {
		Used     int64 `bun:"used_bytes"`
		Reserved int64 `bun:"reserved_bytes"`
	}
	require.NoError(t, s.admin.NewRaw("SELECT used_bytes,reserved_bytes FROM handdraw.workspace_usage WHERE workspace_id=?", f.workspace).Scan(t.Context(), &usage))
	require.Equal(t, int64(len(data)), usage.Used)
	require.Zero(t, usage.Reserved)
	req, e := http.NewRequest(http.MethodGet, path+"/download", nil)
	require.NoError(t, e)
	req.Header.Set("Authorization", "Bearer "+token(f.viewer))
	req.Header.Set("Range", "bytes=0-7")
	response, e := http.DefaultClient.Do(req)
	require.NoError(t, e)
	defer response.Body.Close()
	require.Equal(t, 206, response.StatusCode)
	got, e := io.ReadAll(response.Body)
	require.NoError(t, e)
	require.Equal(t, data[:8], got)
	status, body, _ = request(t, "PUT", path+"/upload", token(f.owner), string(data), "")
	require.Equal(t, 409, status, body)
	_, e = s.admin.ExecContext(t.Context(), "DELETE FROM handdraw.workspace_members WHERE workspace_id=? AND user_id=?", f.workspace, f.viewer.id)
	require.NoError(t, e)
	status, body, _ = request(t, "GET", path+"/download", token(f.viewer), "", "")
	require.Equal(t, 404, status, body)
	status, body, _ = request(t, "DELETE", base+"/v1/boards/"+board, token(f.owner), "", `"1"`)
	require.Equal(t, 202, status, body)
	n, e := assetdb.NewWorker(s.cleanup, s.assets).RunOne(t.Context())
	require.NoError(t, e)
	require.Equal(t, 1, n)
	n, e = assetdb.NewWorker(s.cleanup, s.assets).RunOne(t.Context())
	require.NoError(t, e)
	require.Zero(t, n)
	require.NoError(t, s.admin.NewRaw("SELECT used_bytes,reserved_bytes FROM handdraw.workspace_usage WHERE workspace_id=?", f.workspace).Scan(t.Context(), &usage))
	require.Zero(t, usage.Used)
	require.Zero(t, usage.Reserved)
}

// TestAssetReservationChecksQuotaAndExpiry leaves bytes reserved until object cleanup confirms absence.
func (s *accessSuite) TestAssetReservationChecksQuotaAndExpiry() {
	t := s.T()
	f := s.setup(t)
	base, token := s.http(t)
	board := s.discussionBoard(t, f, base, token(f.owner))
	data := tinyPNG(t)
	payload := reservePayload(t, data)
	_, e := s.admin.ExecContext(t.Context(), "UPDATE handdraw.workspace_usage SET used_bytes=50000000000-? WHERE workspace_id=?", len(data), f.workspace)
	require.NoError(t, e)
	route := base + "/v1/boards/" + board + "/assets"
	status, body, _ := request(t, "POST", route, token(f.owner), payload, "", uuid.NewString())
	require.Equal(t, 201, status, body)
	id := body["result"].(map[string]any)["id"].(string)
	status, body, _ = request(t, "POST", route, token(f.owner), payload, "", uuid.NewString())
	require.Equal(t, 409, status, body)
	_, e = s.admin.ExecContext(t.Context(), "UPDATE handdraw.assets SET expires_at=clock_timestamp()-interval '1 second' WHERE id=?", id)
	require.NoError(t, e)
	status, body, _ = request(t, "PUT", base+"/v1/assets/"+id+"/upload", token(f.owner), string(data), "")
	require.Equal(t, 409, status, body)
	var reserved int64
	require.NoError(t, s.admin.NewRaw("SELECT reserved_bytes FROM handdraw.workspace_usage WHERE workspace_id=?", f.workspace).Scan(t.Context(), &reserved))
	require.Equal(t, int64(len(data)), reserved)
	_, e = assetdb.NewWorker(s.cleanup, s.assets).RunOne(t.Context())
	require.NoError(t, e)
	require.NoError(t, s.admin.NewRaw("SELECT reserved_bytes FROM handdraw.workspace_usage WHERE workspace_id=?", f.workspace).Scan(t.Context(), &reserved))
	require.Zero(t, reserved)
}
