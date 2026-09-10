//go:build integration

package db_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	accessdb "github.com/chai-rs/handdraw-server/app/access/infra/db"
	accessservice "github.com/chai-rs/handdraw-server/app/access/service"
	collabdb "github.com/chai-rs/handdraw-server/app/collaboration/infra/db"
	collabservice "github.com/chai-rs/handdraw-server/app/collaboration/service"
	collabws "github.com/chai-rs/handdraw-server/internal/collaboration/inbound/ws"
	controlcodec "github.com/chai-rs/handdraw-server/internal/collaboration/infra/protocol"
	documentcodec "github.com/chai-rs/handdraw-server/internal/document/infra/ygo"
	identitydb "github.com/chai-rs/handdraw-server/internal/identity/infra/db"
	"github.com/chai-rs/handdraw-server/internal/identity/infra/supabase"
	identityservice "github.com/chai-rs/handdraw-server/internal/identity/service"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func (s *accessSuite) collaboration(t *testing.T, providerURL string) (string, func()) {
	t.Helper()
	verifier, err := supabase.New(supabase.Config{ProjectURL: providerURL, PublishableKey: "local-public-key"})
	require.NoError(t, err)
	authority, err := collabdb.Acquire(t.Context(), s.request)
	require.NoError(t, err)
	duplicate, err := collabdb.Acquire(t.Context(), s.request)
	require.Error(t, err)
	require.Nil(t, duplicate)
	server, err := collabws.New(collabservice.New(identityservice.New(verifier, identitydb.NewProfileRepository(s.resolver)), authority, accessservice.New(accessdb.New()), collabdb.NewDocuments(), documentcodec.Codec{}), controlcodec.Codec{}, collabws.Config{Origins: []string{"http://127.0.0.1:5175"}})
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	app, err := fx.New(fx.Params{Config: fx.Config{Address: address}, Routes: func(r fiber.Router) { server.Register(r.Group("/v1")) }})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	require.Eventually(t, func() bool {
		c, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	}, 5*time.Second, 10*time.Millisecond)
	var once sync.Once
	stop := func() {
		once.Do(func() {
			server.Close()
			cancel()
			require.NoError(t, <-done)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.NoError(t, authority.Close(ctx))
		})
	}
	t.Cleanup(stop)
	return "ws://" + address + "/v1/collaboration", stop
}

func runCollaborationClient(t *testing.T, config map[string]any) {
	t.Helper()
	data, err := json.Marshal(config)
	require.NoError(t, err)
	script, err := filepath.Abs("../../../../../handdraw-client/scripts/collaboration-interop.mjs")
	require.NoError(t, err)
	command := exec.CommandContext(t.Context(), "node", script)
	command.Stdin = strings.NewReader(string(data))
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	t.Log(string(output))
}

// TestCollaborationPersistsFiveRealYjsPeersAndReopensAfterRestart exercises the production Fiber/Auth/RLS/CAS path.
func (s *accessSuite) TestCollaborationPersistsFiveRealYjsPeersAndReopensAfterRestart() {
	t := s.T()
	f := s.setup(t)
	base, token := s.http(t)
	status, data, _ := request(t, "POST", base+"/v1/workspaces/"+f.workspace+"/boards", token(f.owner), `{"name":"Live architecture","initialization":"get_started","project_id":null}`, "", uuid.NewString())
	require.Equal(t, 201, status, data)
	board := data["result"].(map[string]any)["id"].(string)
	claims := jwt.MapClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(token(f.owner), claims)
	require.NoError(t, err)
	issuer, err := claims.GetIssuer()
	require.NoError(t, err)
	address, stop := s.collaboration(t, strings.TrimSuffix(issuer, "/auth/v1"))
	config := map[string]any{"url": address, "board": board, "owner": token(f.owner), "editor": token(f.editor), "viewer": token(f.viewer), "outsider": token(f.outsider), "api": base, "workspace": f.workspace, "editor_id": f.editor.id, "mode": "write"}
	runCollaborationClient(t, config)
	stop()
	address, _ = s.collaboration(t, strings.TrimSuffix(issuer, "/auth/v1"))
	config["url"] = address
	config["mode"] = "reopen"
	runCollaborationClient(t, config)
	var revision int64
	require.NoError(t, s.admin.NewRaw("SELECT revision FROM handdraw.board_documents WHERE board_id=?", board).Scan(t.Context(), &revision))
	require.Greater(t, revision, int64(1))
}

// TestCollaborationFailureDoesNotPublishUncommittedHistory tests database failure and competing-writer fencing.
func (s *accessSuite) TestCollaborationFailureDoesNotPublishUncommittedHistory() {
	t := s.T()
	f := s.setup(t)
	base, token := s.http(t)
	claims := jwt.MapClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(token(f.owner), claims)
	require.NoError(t, err)
	issuer, err := claims.GetIssuer()
	require.NoError(t, err)
	for _, mode := range []string{"commit_failure", "cas_conflict", "authority_lost"} {
		t.Run(mode, func(t *testing.T) {
			status, data, _ := request(t, "POST", base+"/v1/workspaces/"+f.workspace+"/boards", token(f.owner), `{"name":"Failure case","initialization":"get_started","project_id":null}`, "", uuid.NewString())
			require.Equal(t, 201, status, data)
			board := data["result"].(map[string]any)["id"].(string)
			address, stop := s.collaboration(t, strings.TrimSuffix(issuer, "/auth/v1"))
			fault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var failure error
				switch mode {
				case "cas_conflict":
					_, failure = s.admin.ExecContext(r.Context(), "UPDATE handdraw.board_documents SET revision=revision+1 WHERE board_id=?", board)
				case "commit_failure":
					_, failure = s.admin.ExecContext(r.Context(), "CREATE FUNCTION handdraw.test_fail_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected commit failure'; END $$; CREATE CONSTRAINT TRIGGER test_fail_commit AFTER UPDATE ON handdraw.board_documents DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION handdraw.test_fail_commit()")
				case "authority_lost":
					_, failure = s.admin.ExecContext(r.Context(), "SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype='advisory' AND classid=168 AND objid=2549676873")
				}
				if failure != nil {
					w.WriteHeader(500)
					return
				}
				w.WriteHeader(204)
			}))
			defer fault.Close()
			runCollaborationClient(t, map[string]any{"url": address, "board": board, "owner": token(f.owner), "viewer": token(f.viewer), "mode": "failure", "fault_url": fault.URL, "api": base})
			if mode == "commit_failure" {
				_, err = s.admin.ExecContext(t.Context(), "DROP TRIGGER test_fail_commit ON handdraw.board_documents; DROP FUNCTION handdraw.test_fail_commit()")
				require.NoError(t, err)
			}
			stop()
		})
	}
}
