//go:build integration

package db_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	local "github.com/chai-rs/handdraw-server/app/local_sharing/model"
	"github.com/fasthttp/websocket"
	"github.com/stretchr/testify/require"
)

func localSocket(t *testing.T, base, id, token string) *websocket.Conn {
	t.Helper()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(base, "http")+"/v1/local-collaboration?room=local:"+id, http.Header{"Origin": []string{"http://127.0.0.1:5175"}})
	if response != nil {
		_ = response.Body.Close()
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.WriteJSON(map[string]any{"type": "auth", "token": token, "protocol_version": 1}))
	return conn
}

func localEvent(t *testing.T, c *websocket.Conn) local.Event {
	t.Helper()
	require.NoError(t, c.SetReadDeadline(time.Now().Add(5*time.Second)))
	var e local.Event
	require.NoError(t, c.ReadJSON(&e))
	return e
}

func (s *accessSuite) TestLocalSharingUsesSeparateReadOnlySocketsAndLeavesNoCloudContent() {
	t := s.T()
	f := s.setup(t)
	base, token := s.http(t)
	var before, after int
	query := "SELECT (SELECT count(*) FROM handdraw.boards)+(SELECT count(*) FROM handdraw.board_documents)+(SELECT count(*) FROM handdraw.assets)"
	require.NoError(t, s.admin.NewRaw(query).Scan(t.Context(), &before))
	status, body, _ := request(t, "POST", base+"/v1/local-share-sessions", token(f.owner), `{"schema_version":1}`, "")
	require.Equal(t, 201, status, body)
	raw, err := json.Marshal(body["result"])
	require.NoError(t, err)
	var a local.Admission
	require.NoError(t, json.Unmarshal(raw, &a))
	forged := localSocket(t, base, a.ID, token(f.owner))
	require.Equal(t, "session-ended", localEvent(t, forged).Type)
	host := localSocket(t, base, a.ID, a.Token)
	require.Equal(t, "host", localEvent(t, host).Role)
	require.NoError(t, host.WriteJSON(map[string]any{"type": "snapshot", "snapshot": map[string]any{"schema_version": 1, "notes": "Allocation feeds position sizing"}}))
	require.Equal(t, uint64(1), localEvent(t, host).Revision)
	status, body, _ = request(t, "POST", base+"/v1/local-share-sessions/"+a.ID+"/join", token(f.outsider), `{"invite_token":"`+a.InviteToken+`"}`, "")
	require.Equal(t, 200, status, body)
	viewerToken := body["result"].(map[string]any)["token"].(string)
	viewer := localSocket(t, base, a.ID, viewerToken)
	event := localEvent(t, viewer)
	require.Equal(t, "viewer", event.Role)
	require.Contains(t, string(event.Snapshot), "Allocation feeds position sizing")
	require.NoError(t, viewer.WriteJSON(map[string]any{"type": "snapshot", "snapshot": map[string]any{"changed": true}}))
	require.NoError(t, viewer.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, _, err = viewer.ReadMessage()
	require.Error(t, err)
	status, body, _ = request(t, "POST", base+"/v1/local-share-sessions/"+a.ID+"/tokens", token(f.outsider), "", "")
	require.Equal(t, 200, status, body)
	viewer = localSocket(t, base, a.ID, body["result"].(map[string]any)["token"].(string))
	require.Contains(t, string(localEvent(t, viewer).Snapshot), "Allocation feeds position sizing")
	require.NoError(t, host.Close())
	require.Equal(t, "host_disconnected", localEvent(t, viewer).Reason)
	status, body, _ = request(t, "POST", base+"/v1/local-share-sessions/"+a.ID+"/join", token(f.outsider), `{"invite_token":"`+a.InviteToken+`"}`, "")
	require.Equal(t, 410, status, body)
	require.NoError(t, s.admin.NewRaw(query).Scan(t.Context(), &after))
	require.Equal(t, before, after)
}
