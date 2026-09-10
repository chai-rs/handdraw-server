//go:build integration

package db_test

import (
	"context"
	_ "embed"
	"errors"
	"net/netip"
	"net/url"
	"sync"
	"testing"
	"time"

	boarddb "github.com/chai-rs/handdraw-server/internal/board/infra/db"
	"github.com/chai-rs/handdraw-server/internal/board/model"
	"github.com/chai-rs/handdraw-server/internal/board/service"
	"github.com/chai-rs/handdraw-server/internal/testsupport"
	bunx "github.com/chai-rs/handdraw-server/pkg/bun"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/chai-rs/handdraw-server/pkg/txer"
	"github.com/google/uuid"
	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/uptrace/bun"
)

//go:embed testdata/schema.sql
var fixture string

// repositorySuite owns its database pools and never accepts an application DSN.
// Suite methods run sequentially; individual concurrency scenarios coordinate their own goroutines.
type repositorySuite struct {
	suite.Suite
	adminDB    *bun.DB
	requestDB  *bun.DB
	requestDSN string
}

func TestRepositorySuite(t *testing.T) { suite.Run(t, new(repositorySuite)) }

// SetupSuite starts a disposable database and registers cleanup before any assertion can abort setup.
func (suite *repositorySuite) SetupSuite() {
	t := suite.T()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	database, err := postgres.Run(ctx, "postgres:17",
		postgres.WithDatabase("handdraw_board_test"),
		postgres.WithUsername("handdraw_test_admin"),
		postgres.WithPassword("local_test_only"),
		postgres.BasicWaitStrategies(),
		testcontainers.WithHostConfigModifier(func(config *container.HostConfig) {
			for port, bindings := range config.PortBindings {
				for i := range bindings {
					bindings[i].HostIP = netip.MustParseAddr("127.0.0.1")
				}
				config.PortBindings[port] = bindings
			}
		}),
	)
	testcontainers.CleanupContainer(t, database)
	require.NoError(t, err)
	dsn, err := database.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	suite.adminDB = openDB(t, dsn)
	// CREATE SCHEMA refuses to overlay an existing application schema.
	_, err = suite.adminDB.ExecContext(ctx, fixture)
	require.NoError(t, err)
	testsupport.Migrate(t, dsn, "up")
	_, err = suite.adminDB.ExecContext(ctx, "CREATE ROLE handdraw_board_request LOGIN PASSWORD 'local_test_only' IN ROLE handdraw_request")
	require.NoError(t, err)
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.User = url.UserPassword("handdraw_board_request", "local_test_only")
	suite.requestDSN = u.String()
	suite.requestDB = openDB(t, suite.requestDSN)
}

// SetupTest prevents persisted rows from one scenario affecting another.
func (suite *repositorySuite) SetupTest() {
	_, err := suite.adminDB.ExecContext(suite.T().Context(), "TRUNCATE handdraw.profiles, handdraw.workspaces, auth.users CASCADE")
	suite.Require().NoError(err)
}

func openDB(t *testing.T, dsn string) *bun.DB {
	t.Helper()
	db, err := (bunx.PGConfig{URL: dsn, MaxOpenConns: 4}).New(t.Context())
	require.NoError(t, err)
	// Registered after container cleanup, so pools close before the container is removed.
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func runTransaction(ctx context.Context, db *bun.DB, actor string, fn txer.TransactionFn) error {
	var runner txer.Transactioner = bunx.NewTransactioner(db, actor)
	return runner.RunInTx(ctx, fn)
}

type scenario struct{ ws, owner, editor, viewer, outsider string }

func id(t *testing.T, prefix string) string {
	t.Helper()
	value, err := resourceid.New(prefix)
	require.NoError(t, err)
	return value
}

func (suite *repositorySuite) setup(t *testing.T) scenario {
	t.Helper()
	s := scenario{ws: id(t, "ws"), owner: id(t, "usr"), editor: id(t, "usr"), viewer: id(t, "usr"), outsider: id(t, "usr")}
	for _, user := range []string{s.owner, s.editor, s.viewer, s.outsider} {
		subject := uuid.NewString()
		_, err := suite.adminDB.ExecContext(t.Context(), "INSERT INTO auth.users(id) VALUES (?::uuid)", subject)
		require.NoError(t, err)
		_, err = suite.adminDB.ExecContext(t.Context(), "INSERT INTO handdraw.profiles(id,auth_user_id,display_name) VALUES (?,?::uuid,'Developer')", user, subject)
		require.NoError(t, err)
	}
	tx, err := suite.adminDB.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(t.Context(), "INSERT INTO handdraw.workspaces(id,kind,name,owner_user_id) VALUES (?,'team','Architecture',?)", s.ws, s.owner)
	require.NoError(t, err)
	for user, role := range map[string]string{s.owner: "owner", s.editor: "editor", s.viewer: "viewer"} {
		_, err = tx.ExecContext(t.Context(), "INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES (?,?,?)", s.ws, user, role)
		require.NoError(t, err)
	}
	_, err = tx.ExecContext(t.Context(), "INSERT INTO handdraw.subscriptions(workspace_id,plan,billing_interval,status,paid_seats,trial_ends_at) VALUES (?,'team','month','trialing',5,clock_timestamp()+interval '7 days')", s.ws)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return s
}

func (suite *repositorySuite) project(t *testing.T, s scenario) model.Project {
	t.Helper()
	var p model.Project
	err := runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		var err error
		p, err = service.NewProjectService(boarddb.NewProjectRepository()).Create(ctx, model.NewProjectParams{WorkspaceID: s.ws, CreatedBy: s.owner, Name: "Architecture"})
		return err
	})
	require.NoError(t, err)
	return p
}

func (suite *repositorySuite) board(t *testing.T, s scenario, projectID string) model.Board {
	t.Helper()
	var b model.Board
	err := runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		var err error
		b, err = service.NewBoardService(boarddb.NewBoardRepository()).Create(ctx, model.NewBoardParams{WorkspaceID: s.ws, CreatedBy: s.owner, ProjectID: projectID, Name: "Allocation", Status: model.StatusActive}, model.InitialDocument{State: []byte{0, 0}, SchemaVersion: 1})
		return err
	})
	require.NoError(t, err)
	return b
}

func (suite *repositorySuite) TestRepositoriesRequireTransactionAndRLSHidesOtherWorkspaces() {
	t := suite.T()
	s := suite.setup(t)
	p := suite.project(t, s)
	repo := boarddb.NewProjectRepository()
	_, err := repo.Get(t.Context(), s.ws, p.ID())
	require.ErrorIs(t, err, rlstx.ErrMissingTransaction)
	err = runTransaction(t.Context(), suite.requestDB, s.outsider, func(ctx context.Context) error { _, err := repo.Get(ctx, s.ws, p.ID()); return err })
	require.ErrorIs(t, err, model.ErrNotFound)
	err = runTransaction(t.Context(), suite.requestDB, s.viewer, func(ctx context.Context) error {
		got, err := repo.Get(ctx, s.ws, p.ID())
		if err == nil {
			require.Equal(t, p.ID(), got.ID())
		}
		return err
	})
	require.NoError(t, err)
	err = runTransaction(t.Context(), suite.requestDB, s.viewer, func(ctx context.Context) error { _, err := repo.Rename(ctx, s.ws, p.ID(), "Forbidden", 1); return err })
	require.Error(t, err)
	err = runTransaction(t.Context(), suite.requestDB, s.editor, func(ctx context.Context) error {
		_, err := repo.Rename(ctx, s.ws, p.ID(), "Editor rename", 1)
		return err
	})
	require.NoError(t, err)
	other := suite.setup(t)
	err = runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error { _, err := repo.Get(ctx, other.ws, p.ID()); return err })
	require.ErrorIs(t, err, model.ErrNotFound)
}

func (suite *repositorySuite) TestViewerInsertMapsRLSFailureToDomainPermissionError() {
	t := suite.T()
	s := suite.setup(t)
	value, err := model.NewProject(model.NewProjectParams{WorkspaceID: s.ws, CreatedBy: s.viewer, Name: "Denied"})
	require.NoError(t, err)
	err = runTransaction(t.Context(), suite.requestDB, s.viewer, func(ctx context.Context) error {
		_, err := boarddb.NewProjectRepository().Create(ctx, value)
		return err
	})
	require.ErrorIs(t, err, model.ErrPermissionDenied)
}

func (suite *repositorySuite) TestBoardCreationPersistsDocumentAndRollsBackBothOnFailure() {
	t := suite.T()
	s := suite.setup(t)
	b := suite.board(t, s, "")
	var document struct {
		State         []byte
		Revision      int64
		SchemaVersion int
		UpdatedBy     string
	}
	err := suite.adminDB.NewRaw("SELECT state, revision, schema_version, updated_by FROM handdraw.board_documents WHERE board_id = ?", b.ID()).Scan(t.Context(), &document)
	require.NoError(t, err)
	require.Equal(t, []byte{0, 0}, document.State)
	require.Equal(t, int64(1), document.Revision)
	require.Equal(t, s.owner, document.UpdatedBy)
	require.False(t, b.CreatedAt().IsZero())
	require.Equal(t, int64(1), b.Revision())
	sentinel := errors.New("workflow failed after creation")
	var rolledBack model.Board
	err = runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		var err error
		rolledBack, err = service.NewBoardService(boarddb.NewBoardRepository()).Create(ctx, model.NewBoardParams{WorkspaceID: s.ws, CreatedBy: s.owner, Name: "Rollback", Status: model.StatusActive}, model.InitialDocument{State: []byte{0, 0}, SchemaVersion: 1})
		if err != nil {
			return err
		}
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	for _, table := range []string{"boards", "board_documents"} {
		column := "id"
		if table == "board_documents" {
			column = "board_id"
		}
		var count int
		err = suite.adminDB.NewRaw("SELECT count(*) FROM ? WHERE ? = ?", bun.Ident("handdraw."+table), bun.Ident(column), rolledBack.ID()).Scan(t.Context(), &count)
		require.NoError(t, err)
		require.Zero(t, count)
	}
}

func (suite *repositorySuite) TestDocumentInsertFailureLeavesNoOrphanBoard() {
	t := suite.T()
	s := suite.setup(t)
	// Fail only this workspace's document INSERT after metadata INSERT has succeeded.
	_, err := suite.adminDB.ExecContext(t.Context(), "ALTER TABLE handdraw.board_documents ADD CONSTRAINT test_document_failure CHECK(workspace_id <> ?)", s.ws)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := suite.adminDB.ExecContext(context.Background(), "ALTER TABLE handdraw.board_documents DROP CONSTRAINT test_document_failure")
		require.NoError(t, err)
	})
	value, err := model.NewBoard(model.NewBoardParams{WorkspaceID: s.ws, CreatedBy: s.owner, Name: "Failure", Status: model.StatusActive})
	require.NoError(t, err)
	err = runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		_, err := boarddb.NewBoardRepository().Create(ctx, value, model.InitialDocument{State: []byte{0, 0}, SchemaVersion: 1})
		return err
	})
	require.Error(t, err)
	var count int
	err = suite.adminDB.NewRaw("SELECT count(*) FROM handdraw.boards WHERE id = ?", value.ID()).Scan(t.Context(), &count)
	require.NoError(t, err)
	require.Zero(t, count)
}

func (suite *repositorySuite) TestBoardPatchPreservesOmittedFieldsAndDocumentRevision() {
	t := suite.T()
	s := suite.setup(t)
	p := suite.project(t, s)
	b := suite.board(t, s, p.ID())
	svc := service.NewBoardService(boarddb.NewBoardRepository())
	name := "Renamed"
	err := runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		updated, err := svc.Update(ctx, s.ws, b.ID(), model.BoardPatch{Name: &name}, 1)
		if err == nil {
			require.Equal(t, p.ID(), updated.ProjectID())
			require.Equal(t, int64(2), updated.Revision())
			require.Equal(t, b.CreatedAt(), updated.CreatedAt())
			require.Equal(t, b.CreatedBy(), updated.CreatedBy())
		}
		return err
	})
	require.NoError(t, err)
	err = runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		_, err := svc.Update(ctx, s.ws, b.ID(), model.BoardPatch{Name: &name}, 1)
		return err
	})
	require.ErrorIs(t, err, model.ErrRevisionConflict)
	err = runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		updated, err := svc.Update(ctx, s.ws, b.ID(), model.BoardPatch{Project: model.ProjectAssignment{Set: true}}, 2)
		if err == nil {
			require.Empty(t, updated.ProjectID())
			require.Equal(t, name, updated.Name())
		}
		return err
	})
	require.NoError(t, err)
	var revision int64
	err = suite.adminDB.NewRaw("SELECT revision FROM handdraw.board_documents WHERE board_id = ?", b.ID()).Scan(t.Context(), &revision)
	require.NoError(t, err)
	require.Equal(t, int64(1), revision)
}

func (suite *repositorySuite) TestConcurrentRenamesAllowExactlyOneExpectedRevision() {
	t := suite.T()
	s := suite.setup(t)
	b := suite.board(t, s, "")
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"First", "Second"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- runTransaction(context.Background(), suite.requestDB, s.editor, func(ctx context.Context) error {
				_, err := service.NewBoardService(boarddb.NewBoardRepository()).Update(ctx, s.ws, b.ID(), model.BoardPatch{Name: &name}, 1)
				return err
			})
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, model.ErrRevisionConflict)
			conflicts++
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
}

func (suite *repositorySuite) TestProjectDeletionRejectsBoardsAndCrossWorkspaceAssignments() {
	t := suite.T()
	s := suite.setup(t)
	p := suite.project(t, s)
	b := suite.board(t, s, p.ID())
	projects := service.NewProjectService(boarddb.NewProjectRepository())
	boards := service.NewBoardService(boarddb.NewBoardRepository())
	err := runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error { return projects.DeleteEmpty(ctx, s.ws, p.ID(), 1) })
	require.ErrorIs(t, err, model.ErrProjectNotEmpty)
	other := suite.setup(t)
	foreign := suite.project(t, other)
	err = runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		_, err := boards.Update(ctx, s.ws, b.ID(), model.BoardPatch{Project: model.ProjectAssignment{Set: true, ID: foreign.ID()}}, 1)
		return err
	})
	require.ErrorIs(t, err, model.ErrInvalidProject)
	err = runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		_, err := boards.Update(ctx, s.ws, b.ID(), model.BoardPatch{Project: model.ProjectAssignment{Set: true}}, 1)
		return err
	})
	require.NoError(t, err)
	err = runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error { return projects.DeleteEmpty(ctx, s.ws, p.ID(), 1) })
	require.NoError(t, err)
	err = runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		_, err := boards.Update(ctx, s.ws, b.ID(), model.BoardPatch{Project: model.ProjectAssignment{Set: true, ID: p.ID()}}, 2)
		return err
	})
	require.ErrorIs(t, err, model.ErrInvalidProject)
}

func (suite *repositorySuite) TestKeysetPaginationAndUngroupedFilter() {
	t := suite.T()
	s := suite.setup(t)
	p := suite.project(t, s)
	grouped := suite.board(t, s, p.ID())
	ungrouped := suite.board(t, s, "")
	svc := service.NewBoardService(boarddb.NewBoardRepository())
	var stamp time.Time
	require.NoError(t, suite.adminDB.NewRaw("SELECT clock_timestamp()").Scan(t.Context(), &stamp))
	_, err := suite.adminDB.ExecContext(t.Context(), "UPDATE handdraw.boards SET updated_at = ?, metadata_revision=metadata_revision+1 WHERE workspace_id = ?", stamp, s.ws)
	require.NoError(t, err)
	err = runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		first, err := svc.List(ctx, s.ws, nil, model.PageRequest{Limit: 1})
		if err != nil {
			return err
		}
		require.Len(t, first.Items, 1)
		require.NotNil(t, first.Next)
		second, err := svc.List(ctx, s.ws, nil, model.PageRequest{Limit: 1, After: first.Next})
		if err != nil {
			return err
		}
		require.Len(t, second.Items, 1)
		require.Nil(t, second.Next)
		require.NotEqual(t, first.Items[0].ID(), second.Items[0].ID())
		empty := ""
		result, err := svc.List(ctx, s.ws, &empty, model.PageRequest{})
		if err != nil {
			return err
		}
		require.Len(t, result.Items, 1)
		require.Equal(t, ungrouped.ID(), result.Items[0].ID())
		projectID := p.ID()
		result, err = svc.List(ctx, s.ws, &projectID, model.PageRequest{})
		if err == nil {
			require.Len(t, result.Items, 1)
			require.Equal(t, grouped.ID(), result.Items[0].ID())
		}
		return err
	})
	require.NoError(t, err)
}

func (suite *repositorySuite) TestRevocationAndExpiredEntitlementPreventFurtherMutations() {
	t := suite.T()
	s := suite.setup(t)
	b := suite.board(t, s, "")
	repo := boarddb.NewBoardRepository()
	name := "Rejected"
	_, err := suite.adminDB.ExecContext(t.Context(), "DELETE FROM handdraw.workspace_members WHERE workspace_id = ? AND user_id = ?", s.ws, s.editor)
	require.NoError(t, err)
	err = runTransaction(t.Context(), suite.requestDB, s.editor, func(ctx context.Context) error {
		_, err := repo.Update(ctx, s.ws, b.ID(), model.BoardPatch{Name: &name}, 1)
		return err
	})
	require.Error(t, err)
	_, err = suite.adminDB.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET status='ended',access_expires_at=clock_timestamp() WHERE workspace_id=?", s.ws)
	require.NoError(t, err)
	err = runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		_, err := repo.Update(ctx, s.ws, b.ID(), model.BoardPatch{Name: &name}, 1)
		return err
	})
	require.Error(t, err)
	err = runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error { _, err := repo.Get(ctx, s.ws, b.ID()); return err })
	require.NoError(t, err)
}

func (suite *repositorySuite) TestTransactionContextDoesNotLeakAfterCommitRollbackOrPanic() {
	t := suite.T()
	s := suite.setup(t)
	pool := openDB(t, suite.requestDSN)
	pool.SetMaxOpenConns(1)
	assertCleared := func() {
		t.Helper()
		var current string
		err := pool.NewRaw("SELECT COALESCE(current_setting('handdraw.user_id',true),'')").Scan(t.Context(), &current)
		require.NoError(t, err)
		require.Empty(t, current)
	}
	require.NoError(t, runTransaction(t.Context(), pool, s.owner, func(ctx context.Context) error {
		current, err := rlstx.Actor(ctx)
		require.Equal(t, s.owner, current)
		return err
	}))
	assertCleared()
	sentinel := errors.New("rollback")
	require.ErrorIs(t, runTransaction(t.Context(), pool, s.owner, func(context.Context) error { return sentinel }), sentinel)
	assertCleared()
	require.Panics(t, func() {
		_ = runTransaction(t.Context(), pool, s.owner, func(context.Context) error { panic("abort workflow") })
	})
	assertCleared()
	require.NoError(t, runTransaction(t.Context(), pool, s.owner, func(ctx context.Context) error {
		require.ErrorIs(t, runTransaction(ctx, pool, s.viewer, func(context.Context) error { return nil }), rlstx.ErrNestedTransaction)
		return nil
	}))
	var superuser, bypass bool
	err := pool.NewRaw("SELECT rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user").Scan(t.Context(), &superuser, &bypass)
	require.NoError(t, err)
	require.False(t, superuser)
	require.False(t, bypass)
	var count int
	err = pool.NewRaw("SELECT count(*) FROM handdraw.boards").Scan(t.Context(), &count)
	require.NoError(t, err)
	require.Zero(t, count)
}

func (suite *repositorySuite) TestDeletingBoardCannotBeEditedAgain() {
	t := suite.T()
	s := suite.setup(t)
	b := suite.board(t, s, "")
	svc := service.NewBoardService(boarddb.NewBoardRepository())
	require.NoError(t, runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		value, err := svc.MarkDeleting(ctx, s.ws, b.ID(), 1)
		if err == nil {
			require.Equal(t, model.StatusDeleting, value.Status())
			require.Equal(t, int64(2), value.Revision())
		}
		return err
	}))
	name := "No"
	err := runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		_, err := svc.Update(ctx, s.ws, b.ID(), model.BoardPatch{Name: &name}, 2)
		return err
	})
	require.ErrorIs(t, err, model.ErrInvalidState)
}

func (suite *repositorySuite) TestProjectDeleteAndBoardCreateCannotBothCommit() {
	t := suite.T()
	s := suite.setup(t)
	p := suite.project(t, s)
	start := make(chan struct{})
	created := make(chan error, 1)
	deleted := make(chan error, 1)
	go func() {
		<-start
		created <- runTransaction(context.Background(), suite.requestDB, s.owner, func(ctx context.Context) error {
			_, err := service.NewBoardService(boarddb.NewBoardRepository()).Create(ctx, model.NewBoardParams{WorkspaceID: s.ws, CreatedBy: s.owner, ProjectID: p.ID(), Name: "Concurrent", Status: model.StatusActive}, model.InitialDocument{State: []byte{0, 0}, SchemaVersion: 1})
			return err
		})
	}()
	go func() {
		<-start
		deleted <- runTransaction(context.Background(), suite.requestDB, s.owner, func(ctx context.Context) error {
			return service.NewProjectService(boarddb.NewProjectRepository()).DeleteEmpty(ctx, s.ws, p.ID(), 1)
		})
	}()
	close(start)
	createErr, deleteErr := <-created, <-deleted
	if createErr == nil {
		require.ErrorIs(t, deleteErr, model.ErrProjectNotEmpty)
	} else {
		require.ErrorIs(t, createErr, model.ErrInvalidProject)
		require.NoError(t, deleteErr)
	}
	var count int
	err := suite.adminDB.NewRaw("SELECT count(*) FROM handdraw.boards b JOIN handdraw.projects p ON p.id=b.project_id WHERE b.workspace_id=? AND b.deleted_at IS NULL AND p.deleted_at IS NOT NULL", s.ws).Scan(t.Context(), &count)
	require.NoError(t, err)
	require.Zero(t, count)
}
