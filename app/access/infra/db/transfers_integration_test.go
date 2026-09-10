//go:build integration

package db_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	asset "github.com/chai-rs/handdraw-server/internal/asset/model"
	assetmocks "github.com/chai-rs/handdraw-server/internal/asset/model/mocks"
	"github.com/stretchr/testify/mock"

	assetdb "github.com/chai-rs/handdraw-server/app/asset_management/infra/db"
	transferdb "github.com/chai-rs/handdraw-server/app/transfer/infra/db"
	transfermodel "github.com/chai-rs/handdraw-server/app/transfer/model"
	transferservice "github.com/chai-rs/handdraw-server/app/transfer/service"
	documentcodec "github.com/chai-rs/handdraw-server/internal/document/infra/ygo"
	document "github.com/chai-rs/handdraw-server/internal/document/model"
	jobdb "github.com/chai-rs/handdraw-server/internal/job/infra/db"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func (s *accessSuite) upload(t *testing.T, base, token, board, purpose, mime string, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	payload, e := json.Marshal(map[string]any{"purpose": purpose, "mime_type": mime, "size_bytes": len(data), "sha256": hex.EncodeToString(sum[:])})
	require.NoError(t, e)
	status, body, _ := request(t, "POST", base+"/v1/boards/"+board+"/assets", token, string(payload), "", uuid.NewString())
	require.Equal(t, 201, status, body)
	id := body["result"].(map[string]any)["id"].(string)
	status, body, _ = request(t, "PUT", base+"/v1/assets/"+id+"/upload", token, string(data), "")
	require.Equal(t, 200, status, body)
	status, body, _ = request(t, "POST", base+"/v1/assets/"+id+"/complete", token, "", "")
	require.Equal(t, 200, status, body)
	return id
}

func rawDownload(t *testing.T, path, token string) []byte {
	t.Helper()
	req, e := http.NewRequest(http.MethodGet, path, nil)
	require.NoError(t, e)
	req.Header.Set("Authorization", "Bearer "+token)
	response, e := http.DefaultClient.Do(req)
	require.NoError(t, e)
	defer response.Body.Close()
	body, e := io.ReadAll(response.Body)
	require.NoError(t, e)
	require.Equal(t, 200, response.StatusCode, string(body))
	return body
}

func (s *accessSuite) transferWorker() *transferservice.Service {
	return transferservice.New(transferdb.New(s.transfer), s.assets, documentcodec.Codec{}, nil)
}

// TestTransferArchiveRoundTripPublishesNotesLinksAndImagesTogether uses actual Garage objects and production SQL leases.
func (s *accessSuite) TestTransferArchiveRoundTripPublishesNotesLinksAndImagesTogether() {
	t := s.T()
	f := s.setup(t)
	base, token := s.http(t)
	board := s.discussionBoard(t, f, base, token(f.owner))
	png := tinyPNG(t)
	image := s.upload(t, base, token(f.owner), board, "attachment", "image/png", png)
	var original document.Snapshot
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
		scope, e := assetdb.ContentScope(ctx, board)
		require.NoError(t, e)
		var state []byte
		require.NoError(t, s.admin.NewRaw("SELECT state FROM handdraw.board_documents WHERE board_id=?", board).Scan(ctx, &state))
		original, e = (documentcodec.Codec{}).Decode(state, scope)
		require.NoError(t, e)
		page := original.Pages[original.PageOrder[0]]
		page.Scene.Elements["test-image"] = map[string]any{"id": "test-image", "type": "image", "fileId": "file", "x": 0, "y": 0, "width": 100, "height": 100, "angle": 0, "version": 1, "versionNonce": 1, "isDeleted": false}
		page.Scene.ElementOrder = append(page.Scene.ElementOrder, "test-image")
		page.Scene.Files["file"] = document.Asset{AssetID: image, MIMEType: "image/png"}
		original.Pages[page.ID] = page
		state, e = (documentcodec.Codec{}).Encode(original, scope)
		require.NoError(t, e)
		_, e = s.admin.ExecContext(ctx, "UPDATE handdraw.board_documents SET state=?,revision=revision+1 WHERE board_id=?", state, board)
		return e
	}))
	repo := transferdb.New(s.transfer)
	require.NoError(t, repo.Check(t.Context()))
	require.Error(t, transferdb.New(s.request).Check(t.Context()))
	status, body, _ := request(t, "POST", base+"/v1/boards/"+board+"/exports", token(f.viewer), `{"format":"handdraw","include_assets":true}`, "", uuid.NewString())
	require.Equal(t, 202, status, body)
	exportJob := body["result"].(map[string]any)["id"].(string)
	n, e := s.transferWorker().RunOne(t.Context())
	require.NoError(t, e)
	require.Equal(t, 1, n)
	status, body, _ = request(t, "GET", base+"/v1/jobs/"+exportJob, token(f.viewer), "", "")
	require.Equal(t, 200, status, body)
	require.Equal(t, "succeeded", body["result"].(map[string]any)["status"])
	artifact := body["result"].(map[string]any)["result_asset_id"].(string)
	archive := rawDownload(t, base+"/v1/assets/"+artifact+"/download", token(f.viewer))
	status, body, _ = request(t, "POST", base+"/v1/workspaces/"+f.workspace+"/boards", token(f.owner), `{"name":"Imported architecture","initialization":"import","project_id":null}`, "", uuid.NewString())
	require.Equal(t, 201, status, body)
	destination := body["result"].(map[string]any)["id"].(string)
	status, body, _ = request(t, "GET", base+"/v1/boards/"+destination+"/document", token(f.owner), "", "")
	require.Equal(t, 403, status, body)
	source := s.upload(t, base, token(f.owner), destination, "import_source", "application/json", archive)
	payload := `{"format":"handdraw","source_asset_id":"` + source + `","target_schema_version":1}`
	key := uuid.NewString()
	status, body, _ = request(t, "POST", base+"/v1/boards/"+destination+"/imports", token(f.owner), payload, "", key)
	require.Equal(t, 202, status, body)
	importJob := body["result"].(map[string]any)["id"].(string)
	status, body, _ = request(t, "POST", base+"/v1/boards/"+destination+"/imports", token(f.owner), payload, "", key)
	require.Equal(t, 202, status, body)
	require.Equal(t, importJob, body["result"].(map[string]any)["id"])
	n, e = s.transferWorker().RunOne(t.Context())
	require.NoError(t, e)
	require.Equal(t, 1, n)
	status, body, _ = request(t, "GET", base+"/v1/jobs/"+importJob, token(f.owner), "", "")
	require.Equal(t, 200, status, body)
	require.Equal(t, "succeeded", body["result"].(map[string]any)["status"])
	state := rawDownload(t, base+"/v1/boards/"+destination+"/document", token(f.viewer))
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
		scope, e := assetdb.ContentScope(ctx, destination)
		require.NoError(t, e)
		copy, e := (documentcodec.Codec{}).Decode(state, scope)
		require.NoError(t, e)
		require.Len(t, copy.Pages, len(original.Pages))
		require.Len(t, copy.Notes, len(original.Notes))
		require.NotEqual(t, original.PageOrder[0], copy.PageOrder[0])
		newPage := copy.Pages[copy.PageOrder[0]]
		for _, note := range copy.Notes {
			require.Contains(t, note, `page="`+newPage.ID+`"`)
			require.Contains(t, note, "Allocation becomes input to Position sizing.")
		}
		require.NotEqual(t, image, newPage.Scene.Files["file"].AssetID)
		require.Equal(t, png, rawDownload(t, base+"/v1/assets/"+newPage.Scene.Files["file"].AssetID+"/download", token(f.viewer)))
		return nil
	}))
}

// TestExpiredTransferLeaseCannotPublishOrFailTheReplacement fences workers after crash or timeout.
func (s *accessSuite) TestExpiredTransferLeaseCannotPublishOrFailTheReplacement() {
	t := s.T()
	f := s.setup(t)
	base, token := s.http(t)
	board := s.discussionBoard(t, f, base, token(f.owner))
	status, body, _ := request(t, "POST", base+"/v1/boards/"+board+"/exports", token(f.owner), `{"format":"handdraw","include_assets":true}`, "", uuid.NewString())
	require.Equal(t, 202, status, body)
	repo := transferdb.New(s.transfer)
	first, e := repo.Lease(t.Context(), strings.Repeat("1", 64))
	require.NoError(t, e)
	_, e = s.admin.ExecContext(t.Context(), "UPDATE handdraw.transfer_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=?", first.ID)
	require.NoError(t, e)
	replacement, e := repo.Lease(t.Context(), strings.Repeat("2", 64))
	require.NoError(t, e)
	require.Equal(t, first.ID, replacement.ID)
	e = repo.Run(t.Context(), first, func(context.Context, transfermodel.Payload) error {
		t.Fatal("expired worker entered protected callback")
		return nil
	})
	require.ErrorIs(t, e, transfermodel.ErrConflict)
	require.NoError(t, repo.Fail(t.Context(), first, "invalid_transfer"))
	require.NoError(t, repo.Run(t.Context(), replacement, func(context.Context, transfermodel.Payload) error { return nil }))
	require.NoError(t, repo.Fail(t.Context(), replacement, "invalid_transfer"))
}

// TestTransferRetryRecoversFinalizedObjectAfterPublicationFailure exercises a crash after bytes reach Garage.
func (s *accessSuite) TestTransferRetryRecoversFinalizedObjectAfterPublicationFailure() {
	t := s.T()
	f := s.setup(t)
	base, token := s.http(t)
	board := s.discussionBoard(t, f, base, token(f.owner))
	status, body, _ := request(t, "POST", base+"/v1/boards/"+board+"/exports", token(f.owner), `{"format":"handdraw","include_assets":true}`, "", uuid.NewString())
	require.Equal(t, 202, status, body)
	id := body["result"].(map[string]any)["id"].(string)
	storage := assetmocks.NewMockStorage(t)
	var written asset.Asset
	var bytes []byte
	storage.EXPECT().Stage(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, a asset.Asset, data []byte) error {
		require.Equal(t, board, a.BoardID)
		require.Equal(t, "export_artifact", a.Purpose)
		written = a
		bytes = append([]byte(nil), data...)
		return s.assets.Stage(ctx, a, data)
	}).Once()
	stopped := errors.New("worker stopped after verified object publication")
	storage.EXPECT().Finalize(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, a asset.Asset) error {
		require.Equal(t, written, a)
		require.NoError(t, s.assets.Finalize(ctx, a))
		return stopped
	}).Once()
	worker := transferservice.New(transferdb.New(s.transfer), storage, documentcodec.Codec{}, nil)
	_, err := worker.RunOne(t.Context())
	require.ErrorIs(t, err, stopped)
	var state string
	require.NoError(t, s.admin.NewRaw("SELECT status FROM handdraw.assets WHERE id=?", written.ID).Scan(t.Context(), &state))
	require.Equal(t, "pending", state)
	status, _, _ = request(t, "GET", base+"/v1/assets/"+written.ID+"/download", token(f.owner), "", "")
	require.Equal(t, 404, status)
	_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.transfer_jobs SET retry_at=clock_timestamp() WHERE id=?", id)
	require.NoError(t, err)
	_, err = s.transferWorker().RunOne(t.Context())
	require.NoError(t, err)
	status, body, _ = request(t, "GET", base+"/v1/jobs/"+id, token(f.owner), "", "")
	require.Equal(t, 200, status, body)
	require.Equal(t, "succeeded", body["result"].(map[string]any)["status"])
	require.Equal(t, written.ID, body["result"].(map[string]any)["result_asset_id"])
	require.Equal(t, bytes, rawDownload(t, base+"/v1/assets/"+written.ID+"/download", token(f.owner)))
	var count int
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.assets WHERE transfer_job_id=?", id).Scan(t.Context(), &count))
	require.Equal(t, 1, count)
}

// TestInitializingImportCanBeDeletedWithoutOpeningItsDocument permits cleanup by an editor while denying Viewers.
func (s *accessSuite) TestInitializingImportCanBeDeletedWithoutOpeningItsDocument() {
	t := s.T()
	f := s.setup(t)
	base, token := s.http(t)
	status, body, _ := request(t, "POST", base+"/v1/workspaces/"+f.workspace+"/boards", token(f.owner), `{"name":"Abandoned import","initialization":"import","project_id":null}`, "", uuid.NewString())
	require.Equal(t, 201, status, body)
	board := body["result"].(map[string]any)["id"].(string)
	status, _, _ = request(t, "DELETE", base+"/v1/boards/"+board, token(f.viewer), "", `"1"`)
	require.Equal(t, 403, status)
	status, body, _ = request(t, "DELETE", base+"/v1/boards/"+board, token(f.owner), "", `"1"`)
	require.Equal(t, 202, status, body)
	for range 20 {
		n, err := jobdb.NewWorker(s.cleanup).RunOne(t.Context())
		require.NoError(t, err)
		if n == 0 {
			break
		}
	}
}
