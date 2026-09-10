//go:build integration

package db_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func (s *accessSuite) discussionBoard(t *testing.T, f scenario, base, token string) string {
	t.Helper()
	status, body, _ := request(t, "POST", base+"/v1/workspaces/"+f.workspace+"/boards", token, `{"name":"Discussion","initialization":"get_started","project_id":null}`, "", "a11ceb00-0000-4000-8000-000000000001")
	require.Equal(t, 201, status, body)
	return body["result"].(map[string]any)["id"].(string)
}

// TestDiscussionAuthorModerationAndIsolation exercises Viewer discussion without granting content writes.
func (s *accessSuite) TestDiscussionAuthorModerationAndIsolation() {
	t := s.T()
	f := s.setup(t)
	base, token := s.http(t)
	board := s.discussionBoard(t, f, base, token(f.owner))
	route := base + "/v1/boards/" + board + "/comment-threads"
	status, body, _ := request(t, "POST", route, token(f.viewer), `{"anchor":{"kind":"board"},"body":"Why this allocation?"}`, "")
	require.Equal(t, 201, status, body)
	thread := body["result"].(map[string]any)["id"].(string)
	status, body, _ = request(t, "GET", base+"/v1/comment-threads/"+thread+"/comments", token(f.viewer), "", "")
	require.Equal(t, 200, status, body)
	message := body["result"].([]any)[0].(map[string]any)["id"].(string)
	status, body, _ = request(t, "PATCH", base+"/v1/comments/"+message, token(f.owner), `{"body":"Rewrite another person's words"}`, `"1"`)
	require.Equal(t, 403, status, body)
	status, body, _ = request(t, "PATCH", base+"/v1/comments/"+message, token(f.viewer), `{"body":"Edited question"}`, `"1"`)
	require.Equal(t, 200, status, body)
	status, body, _ = request(t, "PATCH", base+"/v1/comments/"+message, token(f.viewer), `{"body":"Stale edit"}`, `"1"`)
	require.Equal(t, 412, status, body)
	status, body, _ = request(t, "PATCH", base+"/v1/comment-threads/"+thread, token(f.editor), `{"status":"resolved"}`, `"1"`)
	require.Equal(t, 403, status, body)
	status, body, _ = request(t, "PATCH", base+"/v1/comment-threads/"+thread, token(f.owner), `{"status":"resolved","anchor":{"kind":"board"}}`, `"1"`)
	require.Equal(t, 400, status, body)
	status, body, _ = request(t, "PATCH", base+"/v1/comment-threads/"+thread, token(f.viewer), `{"status":"resolved"}`, `"1"`)
	require.Equal(t, 200, status, body)
	status, body, _ = request(t, "GET", route, token(f.outsider), "", "")
	require.Equal(t, 404, status, body)
	status, body, _ = request(t, "DELETE", base+"/v1/comments/"+message, token(f.owner), "", `"2"`)
	require.Equal(t, 200, status, body)
	require.Nil(t, body["result"].(map[string]any)["body"])
	status, body, _ = request(t, "PATCH", base+"/v1/comments/"+message, token(f.viewer), `{"body":"Resurrect"}`, `"3"`)
	require.Equal(t, 412, status, body)
	var count int
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.board_documents WHERE board_id=? AND revision=1", board).Scan(t.Context(), &count))
	require.Equal(t, 1, count)
}

// TestDiscussionAnchorAndCleanup preserves target identity and allows deleting the containing board.
func (s *accessSuite) TestDiscussionAnchorAndCleanup() {
	t := s.T()
	f := s.setup(t)
	base, token := s.http(t)
	board := s.discussionBoard(t, f, base, token(f.owner))
	route := base + "/v1/boards/" + board + "/comment-threads"
	status, body, _ := request(t, "POST", route, token(f.owner), `{"anchor":{"kind":"shape","page_id":"pag_0ujtsYcgvSTl8PAuAdqWYSMnLOv","element_id":"missing"},"body":"bad"}`, "")
	require.Equal(t, 400, status, body)
	status, body, _ = request(t, "POST", route, token(f.owner), `{"anchor":{"kind":"board"},"body":"Keep attribution"}`, "")
	require.Equal(t, 201, status, body)
	raw, err := json.Marshal(body["result"])
	require.NoError(t, err)
	require.NotContains(t, string(raw), "workspace_id")
	status, body, _ = request(t, "DELETE", base+"/v1/boards/"+board, token(f.owner), "", `"1"`)
	require.Equal(t, 202, status, body)
	for range 10 {
		_, err = s.cleanup.ExecContext(t.Context(), "SELECT handdraw.run_board_cleanup()")
		require.NoError(t, err)
	}
	require.NoError(t, err)
	var count int
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.comment_threads WHERE board_id=?", board).Scan(t.Context(), &count))
	require.Zero(t, count)
}
