// Command app runs the Handdraw Fiber bootstrap.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	billingapi "github.com/chai-rs/handdraw-server/app/billing/inbound/api"
	billingdb "github.com/chai-rs/handdraw-server/app/billing/infra/db"
	billinglocal "github.com/chai-rs/handdraw-server/app/billing/infra/local"
	billingpolar "github.com/chai-rs/handdraw-server/app/billing/infra/polar"
	billingmodel "github.com/chai-rs/handdraw-server/app/billing/model"
	billingservice "github.com/chai-rs/handdraw-server/app/billing/service"
	localapi "github.com/chai-rs/handdraw-server/app/local_sharing/inbound/api"
	localservice "github.com/chai-rs/handdraw-server/app/local_sharing/service"

	boardapi "github.com/chai-rs/handdraw-server/app/board_management/inbound/api"
	boardquery "github.com/chai-rs/handdraw-server/app/board_management/infra/db"
	boardworkflow "github.com/chai-rs/handdraw-server/app/board_management/service"
	collabdb "github.com/chai-rs/handdraw-server/app/collaboration/infra/db"
	collabservice "github.com/chai-rs/handdraw-server/app/collaboration/service"
	boarddb "github.com/chai-rs/handdraw-server/internal/board/infra/db"
	boardservice "github.com/chai-rs/handdraw-server/internal/board/service"
	collabws "github.com/chai-rs/handdraw-server/internal/collaboration/inbound/ws"
	controlcodec "github.com/chai-rs/handdraw-server/internal/collaboration/infra/protocol"
	documentcodec "github.com/chai-rs/handdraw-server/internal/document/infra/ygo"
	documentservice "github.com/chai-rs/handdraw-server/internal/document/service"
	jobdb "github.com/chai-rs/handdraw-server/internal/job/infra/db"
	jobservice "github.com/chai-rs/handdraw-server/internal/job/service"

	accessdb "github.com/chai-rs/handdraw-server/app/access/infra/db"
	accessservice "github.com/chai-rs/handdraw-server/app/access/service"
	assetapi "github.com/chai-rs/handdraw-server/app/asset_management/inbound/api"
	assetdb "github.com/chai-rs/handdraw-server/app/asset_management/infra/db"
	assetservice "github.com/chai-rs/handdraw-server/app/asset_management/service"
	discussionapi "github.com/chai-rs/handdraw-server/app/discussion/inbound/api"
	discussiondb "github.com/chai-rs/handdraw-server/app/discussion/infra/db"
	discussionservice "github.com/chai-rs/handdraw-server/app/discussion/service"
	membershipapi "github.com/chai-rs/handdraw-server/app/membership/inbound/api"
	membershipdb "github.com/chai-rs/handdraw-server/app/membership/infra/db"
	membershipservice "github.com/chai-rs/handdraw-server/app/membership/service"
	onboardingdb "github.com/chai-rs/handdraw-server/app/onboarding/infra/db"
	onboardingservice "github.com/chai-rs/handdraw-server/app/onboarding/service"
	transferapi "github.com/chai-rs/handdraw-server/app/transfer/inbound/api"
	transferdb "github.com/chai-rs/handdraw-server/app/transfer/infra/db"
	transferservice "github.com/chai-rs/handdraw-server/app/transfer/service"
	workspaceapi "github.com/chai-rs/handdraw-server/app/workspace_management/inbound/api"
	workspaceservice "github.com/chai-rs/handdraw-server/app/workspace_management/service"
	assets3 "github.com/chai-rs/handdraw-server/internal/asset/infra/s3"
	idemdb "github.com/chai-rs/handdraw-server/internal/idempotency/infra/db"
	workspacedb "github.com/chai-rs/handdraw-server/internal/workspace/infra/db"
	workspacedomain "github.com/chai-rs/handdraw-server/internal/workspace/service"
	"github.com/chai-rs/handdraw-server/pkg/cursor"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"

	identityapi "github.com/chai-rs/handdraw-server/internal/identity/inbound/api"
	identitydb "github.com/chai-rs/handdraw-server/internal/identity/infra/db"
	"github.com/chai-rs/handdraw-server/internal/identity/infra/supabase"
	identityservice "github.com/chai-rs/handdraw-server/internal/identity/service"
	bunx "github.com/chai-rs/handdraw-server/pkg/bun"
	configx "github.com/chai-rs/handdraw-server/pkg/config"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	logx "github.com/chai-rs/handdraw-server/pkg/logger"
	"github.com/gofiber/fiber/v3"
)

type configuration struct {
	HTTP          fx.Config
	Log           logx.Config
	Identity      identityConfig
	Workspace     workspaceConfig
	Board         boardConfig
	Cleanup       cleanupConfig
	Membership    membershipConfig
	Collaboration collaborationConfig
	Asset         assetConfig
	Billing       billingConfig
	Transfer      transferConfig
	LocalShare    localShareConfig `split_words:"true"`
}

type billingConfig struct {
	Enabled     bool
	DatabaseURL string `split_words:"true"`
	Provider    string `default:"local"`
	Polar       billingpolar.Config
}

func main() {
	conf, err := configx.New[configuration]("APP")
	if err != nil {
		logx.Error().Msg("invalid application configuration")
		os.Exit(1)
	}

	logx.Bind(&conf.Log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, *conf); err != nil {
		logx.Error().Err(err).Msg("handdraw-server stopped")
		os.Exit(1)
	}
}

type localShareConfig struct {
	Enabled bool
	Origins []string
}

type transferConfig struct {
	Enabled     bool   `default:"false"`
	DatabaseURL string `split_words:"true" json:"-"`
}

type assetConfig struct {
	Enabled bool `default:"false"`
	S3      assets3.Config
}

type identityConfig struct {
	Enabled        bool          `default:"false"`
	DatabaseURL    string        `split_words:"true" json:"-"`
	SupabaseURL    string        `split_words:"true"`
	PublishableKey string        `split_words:"true" json:"-"`
	Audience       string        `default:"authenticated"`
	Timeout        time.Duration `default:"5s"`
}

type membershipConfig struct {
	Enabled  bool   `default:"false"`
	TokenKey string `split_words:"true" json:"-"`
}

type collaborationConfig struct {
	Enabled bool     `default:"false"`
	Origins []string `split_words:"true"`
}

type boardConfig struct {
	Enabled bool `default:"false"`
}
type cleanupConfig struct {
	Enabled     bool   `default:"false"`
	DatabaseURL string `split_words:"true" json:"-"`
}

type workspaceConfig struct {
	Enabled     bool   `default:"false"`
	DatabaseURL string `split_words:"true" json:"-"`
	CursorKey   string `split_words:"true" json:"-"`
}

func run(ctx context.Context, config configuration) error {
	if config.LocalShare.Enabled && !config.Identity.Enabled {
		return errors.New("Local sharing requires identity verification")
	}

	if config.Billing.Enabled && (!config.Board.Enabled || !config.Cleanup.Enabled || !config.Asset.Enabled) {
		return errors.New("billing requires board, Garage assets and cleanup")
	}

	if config.Billing.Enabled && config.Billing.Provider != "local" && config.Billing.Provider != "polar" {
		return errors.New("billing provider must be local or polar")
	}

	if config.Transfer.Enabled && !config.Asset.Enabled {
		return errors.New("transfers require private assets")
	}

	var storage *assets3.Storage

	if config.Asset.Enabled {
		if !config.Board.Enabled || !config.Cleanup.Enabled {
			return errors.New("assets require board routes and cleanup worker")
		}

		var err error

		storage, err = assets3.New(config.Asset.S3)
		if err != nil {
			return err
		}

		if err = storage.Check(ctx); err != nil {
			return err
		}

		config.HTTP.BodyLimit = 64_000_000
	}

	if config.Collaboration.Enabled && !config.Board.Enabled {
		return errors.New("collaboration requires board routes")
	}

	if config.Membership.Enabled && !config.Board.Enabled {
		return errors.New("membership routes require board routes")
	}

	var membershipTokens *membershipservice.Tokens

	if config.Membership.Enabled {
		if config.Membership.TokenKey == config.Workspace.CursorKey {
			return errors.New("membership token key must be independent of the cursor key")
		}

		var err error

		membershipTokens, err = membershipservice.NewTokens([]byte(config.Membership.TokenKey))
		if err != nil {
			return errors.New("membership requires an independent token key of at least 32 bytes")
		}
	}

	if config.Board.Enabled && !config.Workspace.Enabled {
		return errors.New("board routes require workspace routes")
	}

	if config.Cleanup.Enabled && !config.Board.Enabled {
		return errors.New("cleanup requires board routes")
	}

	if config.Workspace.Enabled && !config.Identity.Enabled {
		return errors.New("workspace routes require identity")
	}

	var (
		localHandler         *localapi.Server
		collaborationHandler *collabws.Server
		boardHandler         *boardapi.Handler
		billingHandler       *billingapi.Handler
		billingWebhook       *billingapi.PolarWebhook
		transferHandler      *transferapi.Handler
		assetHandler         *assetapi.Handler
		discussionHandler    *discussionapi.Handler
		membershipHandler    *membershipapi.Handler
		workspaceHandler     *workspaceapi.Handler
		handler              *identityapi.Handler
		checks               []fx.Check
	)

	if config.Identity.Enabled {
		verifier, err := supabase.New(supabase.Config{ProjectURL: config.Identity.SupabaseURL, PublishableKey: config.Identity.PublishableKey, Audience: config.Identity.Audience, Timeout: config.Identity.Timeout})
		if err != nil {
			return err
		}

		db, err := (bunx.PGConfig{URL: config.Identity.DatabaseURL}).New(ctx)
		if err != nil {
			return err
		}

		defer func() { _ = db.Close() }()

		if err := bunx.CheckSchema(ctx, db, 16); err != nil {
			return err
		}

		profiles := identitydb.NewProfileRepository(db)
		if err := profiles.Check(ctx); err != nil {
			return err
		}

		identity := identityservice.New(verifier, profiles)

		handler = identityapi.New(identity)
		if config.LocalShare.Enabled {
			localHandler, err = localapi.New(identity, localservice.New(nil), config.LocalShare.Origins)
			if err != nil {
				return err
			}
			defer localHandler.Close()
		}

		checks = []fx.Check{fx.NewCheck("identity_store", profiles.Check), fx.NewCheck("database_schema", func(ctx context.Context) error { return bunx.CheckSchema(ctx, db, 16) })}

		if config.Workspace.Enabled {
			cursors, err := cursor.New([]byte(config.Workspace.CursorKey))
			if err != nil {
				return errors.New("workspace cursor key must contain at least 32 bytes")
			}

			requestDB, err := (bunx.PGConfig{URL: config.Workspace.DatabaseURL}).New(ctx)
			if err != nil {
				return err
			}

			defer func() { _ = requestDB.Close() }()

			if err = accessdb.CheckRequestPool(ctx, requestDB); err != nil {
				return err
			}

			if err = bunx.CheckSchema(ctx, requestDB, 16); err != nil {
				return err
			}

			access := accessservice.New(accessdb.New())
			session := accessservice.NewSession(identity, rlstx.NewRunner(requestDB))
			workflow := workspaceservice.New(workspacedomain.New(workspacedb.New()), access)
			workspaceHandler = workspaceapi.New(session, workflow, cursors).WithOnboarding(onboardingservice.New(onboardingdb.New(), idemdb.New(), access))

			if config.Board.Enabled {
				if config.Cleanup.Enabled {
					cleanupDB, err := (bunx.PGConfig{URL: config.Cleanup.DatabaseURL}).New(ctx)
					if err != nil {
						return err
					}
					defer func() { _ = cleanupDB.Close() }()

					worker := jobdb.NewWorker(cleanupDB)
					if err = worker.Check(ctx); err != nil {
						return err
					}

					if err = bunx.CheckSchema(ctx, cleanupDB, 16); err != nil {
						return err
					}

					if storage != nil {
						assetCtx, assetCancel := context.WithCancel(ctx)

						assetDone := make(chan struct{})
						go func() {
							defer close(assetDone)

							jobservice.NewWorker(assetdb.NewWorker(cleanupDB, storage)).Run(assetCtx, func(err error) { logx.Error().Err(err).Msg("asset cleanup pass failed") })
						}()

						defer func() { assetCancel(); <-assetDone }()
					}

					checks = append(checks, fx.NewCheck("cleanup_store", worker.Check))
					workerCtx, cancel := context.WithCancel(ctx)

					done := make(chan struct{})
					go func() {
						defer close(done)

						jobservice.NewWorker(worker).Run(workerCtx, func(err error) { logx.Error().Err(err).Msg("board cleanup pass failed") })
					}()

					defer func() { cancel(); <-done }()
				}

				if config.Collaboration.Enabled {
					authority, err := collabdb.Acquire(ctx, requestDB)
					if err != nil {
						return err
					}
					defer func() {
						closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()

						_ = authority.Close(closeCtx)
					}()

					collaborationHandler, err = collabws.New(collabservice.New(identity, authority, access, collabdb.NewDocuments(), documentcodec.Codec{}), controlcodec.Codec{}, collabws.Config{Origins: config.Collaboration.Origins})
					if err != nil {
						return err
					}
					defer collaborationHandler.Close()

					checks = append(checks, fx.NewCheck("collaboration_authority", authority.Check))
				}

				boards := boardworkflow.New(boardservice.NewBoardService(boarddb.NewBoardRepository()), boardservice.NewProjectService(boarddb.NewProjectRepository()), documentservice.NewInitialBuilder(documentcodec.Codec{}), access, boardquery.New(), idemdb.New(), jobdb.New())

				if config.Billing.Enabled {
					billingDB, err := (bunx.PGConfig{URL: config.Billing.DatabaseURL}).New(ctx)
					if err != nil {
						return err
					}
					defer func() { _ = billingDB.Close() }()

					repo := billingdb.New(billingDB)
					if err = repo.Check(ctx); err != nil {
						return err
					}

					if err = bunx.CheckSchema(ctx, billingDB, 16); err != nil {
						return err
					}

					checks = append(checks, fx.NewCheck("billing_store", repo.Check))
					billingCtx, billingCancel := context.WithCancel(ctx)

					var provider billingmodel.Provider

					switch config.Billing.Provider {
					case "local":
						provider = billinglocal.New(billingDB)
					case "polar":
						polarProvider, providerErr := billingpolar.New(billingDB, nil, config.Billing.Polar)
						if providerErr != nil {
							return providerErr
						}

						provider = polarProvider

						decoder, decoderErr := billingpolar.NewWebhook(config.Billing.Polar.WebhookSecret)
						if decoderErr != nil {
							return decoderErr
						}

						billingWebhook = billingapi.NewPolarWebhook(decoder, billingdb.NewPolarWebhookQueue(billingDB))
						billingHandler = billingapi.New(session, billingservice.New(billingdb.New(nil), nil)).WithPortal(billingservice.NewPortal(billingdb.New(nil), polarProvider))
					}

					billingDone := make(chan struct{})
					go func() {
						defer close(billingDone)

						jobservice.NewWorker(billingservice.New(repo, provider)).Run(billingCtx, func(err error) { logx.Error().Msg("billing pass failed") })
					}()

					defer func() { billingCancel(); <-billingDone }()

					if billingHandler == nil {
						billingHandler = billingapi.New(session, billingservice.New(billingdb.New(nil), nil))
					}
				}

				if config.Transfer.Enabled {
					transferDB, err := (bunx.PGConfig{URL: config.Transfer.DatabaseURL}).New(ctx)
					if err != nil {
						return err
					}
					defer func() { _ = transferDB.Close() }()

					repo := transferdb.New(transferDB)
					if err = repo.Check(ctx); err != nil {
						return err
					}

					checks = append(checks, fx.NewCheck("transfer_store", repo.Check))
					transferCtx, transferCancel := context.WithCancel(ctx)

					transferDone := make(chan struct{})
					go func() {
						defer close(transferDone)

						jobservice.NewWorker(transferservice.New(repo, storage, documentcodec.Codec{}, nil)).WithTimeout(90*time.Second).Run(transferCtx, func(err error) { logx.Error().Msg("transfer pass failed") })
					}()

					defer func() { transferCancel(); <-transferDone }()

					transferHandler = transferapi.New(session, transferservice.New(transferdb.New(nil), storage, documentcodec.Codec{}, idemdb.New()))
				}

				if storage != nil {
					assetHandler = assetapi.New(session, assetservice.New(assetdb.New(), storage, idemdb.New()))
				}

				discussionHandler = discussionapi.New(session, discussionservice.New(discussiondb.New(), documentcodec.Codec{}), cursors)

				boardHandler = boardapi.New(session, boards, cursors, config.Cleanup.Enabled).WithImports(config.Transfer.Enabled)
				if config.Membership.Enabled {
					members := membershipservice.New(membershipdb.New(), access, membershipTokens, idemdb.New())
					membershipHandler = membershipapi.New(session, members, cursors)
					boardHandler.WithSharedBoards(members)
				}
			}

			checks = append(checks, fx.NewCheck("workspace_store", func(ctx context.Context) error {
				if err := bunx.CheckSchema(ctx, requestDB, 16); err != nil {
					return err
				}

				return accessdb.CheckRequestPool(ctx, requestDB)
			}))
		}
	}

	server, err := fx.New(fx.Params{Config: config.HTTP, ReadinessChecks: checks, Routes: func(router fiber.Router) {
		if localHandler != nil {
			localHandler.Register(router.Group("/v1"))
		}

		if collaborationHandler != nil {
			collaborationHandler.Register(router.Group("/v1"))
		}

		if handler != nil {
			handler.Register(router.Group("/v1"))
		}

		if boardHandler != nil {
			boardHandler.Register(router.Group("/v1"))
		}

		if billingHandler != nil {
			billingHandler.Register(router.Group("/v1"))
		}

		if billingWebhook != nil {
			billingWebhook.Register(router.Group("/v1"))
		}

		if transferHandler != nil {
			transferHandler.Register(router.Group("/v1"))
		}

		if assetHandler != nil {
			assetHandler.Register(router.Group("/v1"))
		}

		if discussionHandler != nil {
			discussionHandler.Register(router.Group("/v1"))
		}

		if membershipHandler != nil {
			membershipHandler.Register(router.Group("/v1"))
		}

		if workspaceHandler != nil {
			workspaceHandler.Register(router.Group("/v1"))
		}

		router.Get("/healthz", func(c fiber.Ctx) error { return c.JSON(fiber.Map{"service": "handdraw-server", "status": "ok"}) })
	}})
	if err != nil {
		return err
	}

	logx.Info().Str("address", config.HTTP.Address).Msg("handdraw-server starting")

	if err := server.Run(ctx); err != nil {
		return err
	}

	logx.Info().Msg("handdraw-server stopped")

	return nil
}
