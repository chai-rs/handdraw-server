// Package service runs replayable bounded transfers without publishing partial imports.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/chai-rs/handdraw-server/app/transfer/model"
	asset "github.com/chai-rs/handdraw-server/internal/asset/model"
	document "github.com/chai-rs/handdraw-server/internal/document/model"
	idem "github.com/chai-rs/handdraw-server/internal/idempotency/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// Service composes durable job state, verified private storage and the supported document codec.
type Service struct {
	repository model.Repository
	storage    asset.Storage
	codec      document.Codec
	keys       idem.Repository
}

// New is used for both authenticated request enqueue and a separately credentialed worker.
func New(repo model.Repository, storage asset.Storage, codec document.Codec, keys idem.Repository) *Service {
	return &Service{repository: repo, storage: storage, codec: codec, keys: keys}
}

// Import validates the source contract before durable enqueue.
func (s *Service) Import(ctx context.Context, board, key string, p model.Import) (model.Job, error) {
	if p.TargetSchemaVersion != 1 || resourceid.Validate(p.SourceAssetID, asset.IDPrefix) != nil {
		return model.Job{}, model.ErrInvalid
	}

	return s.enqueue(ctx, board, key, "import", p.Format, p.SourceAssetID, true)
}

// Export supports full archives and explicit single-page native files.
func (s *Service) Export(ctx context.Context, board, key string, p model.Export) (model.Job, error) {
	if p.Format == "excalidraw" && resourceid.Validate(p.PageID, document.PageIDPrefix) != nil {
		return model.Job{}, model.ErrInvalid
	}

	return s.enqueue(ctx, board, key, "export", p.Format, p.PageID, p.IncludeAssets)
}

func (s *Service) enqueue(ctx context.Context, board, key, kind, format, source string, assets bool) (model.Job, error) {
	if resourceid.Validate(board, model.BoardIDPrefix) != nil || (format != "handdraw" && format != "excalidraw") {
		return model.Job{}, model.ErrInvalid
	}

	request, err := idem.New("transfer."+kind, key, "", struct {
		Board, Format, Source string
		Assets                bool
	}{board, format, source, assets})
	if err != nil {
		return model.Job{}, err
	}

	ticket, err := s.keys.Begin(ctx, request)
	if err != nil {
		return model.Job{}, err
	}

	if ticket.Replayed {
		return s.repository.Get(ctx, ticket.Reference)
	}

	id, err := resourceid.New(model.IDPrefix)
	if err != nil {
		return model.Job{}, err
	}

	if err = s.repository.Enqueue(ctx, id, board, kind, format, source, assets); err != nil {
		return model.Job{}, err
	}

	if err = s.keys.Complete(ctx, ticket, id, 202); err != nil {
		return model.Job{}, err
	}

	return s.repository.Get(ctx, id)
}

// Get returns only the currently authorized public job projection.
func (s *Service) Get(ctx context.Context, id string) (model.Job, error) {
	if resourceid.Validate(id, model.IDPrefix) != nil {
		return model.Job{}, model.ErrInvalid
	}

	return s.repository.Get(ctx, id)
}

// RunOne leases one job, commits its object plan, then atomically publishes verified outputs.
func (s *Service) RunOne(ctx context.Context) (int, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return 0, err
	}

	task, err := s.repository.Lease(ctx, hex.EncodeToString(token))
	if errors.Is(err, model.ErrEmpty) {
		return 0, nil
	}

	if err != nil {
		return 0, err
	}

	err = s.repository.Run(ctx, task, func(ctx context.Context, p model.Payload) error {
		var (
			plans []planned
			err   error
		)
		if p.Kind == "import" {
			plans, _, err = s.importPlan(ctx, p)
		} else {
			plans, err = s.exportPlan(ctx, p)
		}

		if err != nil {
			return err
		}

		assets := make([]asset.Asset, 0, len(plans))
		for _, plan := range plans {
			assets = append(assets, plan.asset)
		}

		return s.repository.Prepare(ctx, task, assets)
	})
	if err == nil {
		err = s.repository.Run(ctx, task, func(ctx context.Context, p model.Payload) error {
			var (
				plans []planned
				state []byte
				err   error
			)
			if p.Kind == "import" {
				plans, state, err = s.importPlan(ctx, p)
			} else {
				plans, err = s.exportPlan(ctx, p)
			}

			if err != nil {
				return err
			}

			for _, plan := range plans {
				if err = s.storage.Stage(ctx, plan.asset, plan.data); err != nil {
					return err
				}

				if err = s.storage.Finalize(ctx, plan.asset); err != nil {
					return err
				}
			}

			return s.repository.Complete(ctx, task, state)
		})
	}

	if err != nil {
		code := "dependency_unavailable"
		if errors.Is(err, model.ErrInvalid) {
			code = "invalid_transfer"
		}

		if errors.Is(err, model.ErrDenied) || errors.Is(err, asset.ErrDenied) {
			code = "permission_denied"
		}

		failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		if failure := s.repository.Fail(failCtx, task, code); failure != nil {
			return 1, errors.Join(err, failure)
		}

		return 1, err
	}

	return 1, nil
}
