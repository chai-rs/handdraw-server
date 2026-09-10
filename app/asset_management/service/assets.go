// Package service verifies uploaded bytes before publishing private assets and counting usage.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/chai-rs/handdraw-server/app/asset_management/model"
	asset "github.com/chai-rs/handdraw-server/internal/asset/model"
	idem "github.com/chai-rs/handdraw-server/internal/idempotency/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// Service keeps bytes outside SQL and never trusts a caller-supplied premium flag.
type Service struct {
	repository  model.Repository
	storage     asset.Storage
	idempotency idem.Repository
}

// New composes metadata, immutable storage and transaction-bound idempotency.
func New(repository model.Repository, storage asset.Storage, idempotency idem.Repository) *Service {
	return &Service{repository: repository, storage: storage, idempotency: idempotency}
}

// Reserve atomically allocates quota and records one asset for a request key.
func (s *Service) Reserve(ctx context.Context, board, key string, p asset.Reserve) (asset.Asset, error) {
	if resourceid.Validate(board, asset.BoardIDPrefix) != nil || p.Validate() != nil {
		return asset.Asset{}, asset.ErrInvalid
	}

	request, err := idem.New("asset.reserve", key, "", struct {
		Board  string
		Params asset.Reserve
	}{board, p})
	if err != nil {
		return asset.Asset{}, err
	}

	ticket, err := s.idempotency.Begin(ctx, request)
	if err != nil {
		return asset.Asset{}, err
	}

	if ticket.Replayed {
		return s.repository.Get(ctx, ticket.Reference)
	}

	id, err := resourceid.New(asset.IDPrefix)
	if err != nil {
		return asset.Asset{}, err
	}

	if err = s.repository.Reserve(ctx, id, board, p); err != nil {
		return asset.Asset{}, err
	}

	if err = s.idempotency.Complete(ctx, ticket, id, 201); err != nil {
		return asset.Asset{}, err
	}

	return s.repository.Get(ctx, id)
}

func (s *Service) writable(ctx context.Context, id string) (asset.Asset, error) {
	if resourceid.Validate(id, asset.IDPrefix) != nil {
		return asset.Asset{}, asset.ErrInvalid
	}

	if err := s.repository.Lock(ctx, id, true); err != nil {
		return asset.Asset{}, err
	}

	a, err := s.repository.Get(ctx, id)
	if err != nil {
		return a, err
	}

	if a.Status != "pending" || !a.ExpiresAt.After(time.Now()) {
		return a, asset.ErrConflict
	}

	return a, nil
}

// Upload accepts only exact reserved bytes and a verified supported media type.
func (s *Service) Upload(ctx context.Context, id string, data []byte) (asset.Asset, error) {
	a, err := s.writable(ctx, id)
	if err != nil {
		return a, err
	}

	sum := sha256.Sum256(data)
	if int64(len(data)) != a.Size || hex.EncodeToString(sum[:]) != a.SHA256 {
		return asset.Asset{}, asset.ErrConflict
	}

	switch a.MIME {
	case "application/json":
		if !json.Valid(data) {
			return asset.Asset{}, asset.ErrInvalid
		}
	case "image/svg+xml":
		if !a.Premium || !strings.Contains(string(data), "<svg") {
			return asset.Asset{}, asset.ErrInvalid
		}
	default:
		if http.DetectContentType(data) != a.MIME {
			return asset.Asset{}, asset.ErrInvalid
		}
	}

	if err = s.storage.Stage(ctx, a, data); err != nil {
		return asset.Asset{}, err
	}

	return a, nil
}

// Complete can recover a committed final file without counting the upload twice.
func (s *Service) Complete(ctx context.Context, id string) (asset.Asset, error) {
	if resourceid.Validate(id, asset.IDPrefix) != nil {
		return asset.Asset{}, asset.ErrInvalid
	}

	if err := s.repository.Lock(ctx, id, true); err != nil {
		return asset.Asset{}, err
	}

	a, err := s.repository.Get(ctx, id)
	if err != nil {
		return a, err
	}

	if a.Status == "available" {
		return a, nil
	}

	if a.Status != "pending" || !a.ExpiresAt.After(time.Now()) {
		return asset.Asset{}, asset.ErrConflict
	}

	if err = s.storage.Finalize(ctx, a); err != nil {
		return asset.Asset{}, err
	}

	if err = s.repository.Complete(ctx, id); err != nil {
		return asset.Asset{}, err
	}

	return s.repository.Get(ctx, id)
}

// Download reauthorizes the current caller and verifies immutable available bytes.
func (s *Service) Download(ctx context.Context, id string) (asset.Asset, []byte, error) {
	if resourceid.Validate(id, asset.IDPrefix) != nil {
		return asset.Asset{}, nil, asset.ErrInvalid
	}

	if err := s.repository.Lock(ctx, id, false); err != nil {
		return asset.Asset{}, nil, err
	}

	a, err := s.repository.Get(ctx, id)
	if err != nil {
		return a, nil, err
	}

	if a.Status != "available" || (a.Purpose != "attachment" && !a.ExpiresAt.After(time.Now())) {
		return asset.Asset{}, nil, asset.ErrNotFound
	}

	data, err := s.storage.Read(ctx, a)

	return a, data, err
}
