//go:build integration

package s3_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"

	storage "github.com/chai-rs/handdraw-server/internal/asset/infra/s3"
	"github.com/chai-rs/handdraw-server/internal/asset/model"
	"github.com/chai-rs/handdraw-server/internal/testsupport"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type garageSuite struct {
	suite.Suite
	store  *storage.Storage
	config storage.Config
}

func TestGarageSuite(t *testing.T) { suite.Run(t, new(garageSuite)) }

// SetupSuite starts the actual pinned Garage image rather than a simulated S3 HTTP server.
func (s *garageSuite) SetupSuite() {
	s.config = testsupport.Garage(s.T())
	var err error
	s.store, err = storage.New(s.config)
	require.NoError(s.T(), err)
}

// TestImmutableFinalizationAndPrivateReads verifies external object semantics and complete cleanup.
func (s *garageSuite) TestImmutableFinalizationAndPrivateReads() {
	t := s.T()
	data := bytes.Repeat([]byte("Garage exact bytes\n"), 40000)
	sum := sha256.Sum256(data)
	id, e := resourceid.New(model.IDPrefix)
	require.NoError(t, e)
	a := model.Asset{ID: id, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), MIME: "image/png"}
	require.NoError(t, s.store.Stage(t.Context(), a, data))
	require.NoError(t, s.store.Stage(t.Context(), a, data))
	_, e = s.store.Read(t.Context(), a)
	require.Error(t, e)
	require.ErrorIs(t, s.store.Stage(t.Context(), a, []byte("substituted bytes")), model.ErrConflict)
	require.NoError(t, s.store.Finalize(t.Context(), a))
	require.NoError(t, s.store.Finalize(t.Context(), a))
	actual, e := s.store.Read(t.Context(), a)
	require.NoError(t, e)
	require.Equal(t, data, actual)
	response, e := http.Get(s.config.Endpoint + "/" + s.config.Bucket + "/" + id + "/" + a.SHA256 + ".final")
	require.NoError(t, e)
	defer response.Body.Close()
	require.NotEqual(t, 200, response.StatusCode)
	require.NoError(t, s.store.Remove(t.Context(), id))
	require.NoError(t, s.store.Remove(t.Context(), id))
	_, e = s.store.Read(t.Context(), a)
	require.Error(t, e)
}
