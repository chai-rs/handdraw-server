// Package service coordinates durable billing intentions without holding SQL locks across provider calls.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/chai-rs/handdraw-server/app/billing/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/google/uuid"
)

// Service connects actor-scoped requests and the separately credentialed worker.
type Service struct {
	repo     model.Repository
	provider model.Provider
}

// New binds ports only; production providers remain disabled until their contract tests pass.
func New(repo model.Repository, provider model.Provider) *Service {
	return &Service{repo: repo, provider: provider}
}

// Request validates the selected action; the repository locks and rechecks the current Owner.
func (s *Service) Request(ctx context.Context, w, action, key string, p model.Command) (json.RawMessage, error) {
	if resourceid.Validate(w, model.WorkspaceIDPrefix) != nil || p.Validate(action) != nil {
		return nil, model.ErrInvalid
	}

	if action == "quote" || action == "checkout" || action == "cancel" || action == "resume" {
		id, e := uuid.Parse(key)
		if e != nil || id.String() != key {
			return nil, model.ErrInvalid
		}
	} else {
		key = uuid.Nil.String()
	}

	id, err := resourceid.New(model.IDPrefix)
	if err != nil {
		return nil, err
	}

	return s.repo.Request(ctx, w, action, id, key, p)
}

// RunOne applies at most one provider observation, then advances bounded retention work.
func (s *Service) RunOne(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return 0, err
	}

	task, err := s.repo.Lease(ctx, hex.EncodeToString(secret[:]))
	if errors.Is(err, model.ErrEmpty) {
		return 0, s.repo.Maintain(ctx)
	}

	if err != nil {
		return 0, err
	}

	observation, err := s.provider.Observe(ctx, task.ID)
	if err != nil {
		return 0, err
	}

	if err = observation.Validate(); err != nil {
		return 0, err
	}

	if err = s.repo.Apply(ctx, task, observation); err != nil {
		return 0, err
	}

	return 1, s.repo.Maintain(ctx)
}
