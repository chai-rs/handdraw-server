// Package service authenticates identities without calling other domains.
package service

import (
	"context"

	"github.com/chai-rs/handdraw-server/internal/identity/model"
)

// Service resolves a stable profile only after token verification succeeds.
type Service struct {
	verifier model.Verifier
	profiles model.ProfileRepository
}

// New binds provider verification and the restricted profile resolver.
func New(verifier model.Verifier, profiles model.ProfileRepository) *Service {
	return &Service{verifier: verifier, profiles: profiles}
}

// Authenticate returns a current principal without storing credentials or trusting client profile IDs.
func (s *Service) Authenticate(ctx context.Context, token model.AccessToken) (model.Principal, error) {
	if err := token.Validate(); err != nil {
		return model.Principal{}, err
	}

	identity, err := s.verifier.Verify(ctx, token)
	if err != nil {
		return model.Principal{}, err
	}

	if err := identity.Validate(); err != nil {
		return model.Principal{}, err
	}

	candidate, err := model.NewProfile(model.NewProfileParams{AuthUserID: identity.Subject, DisplayName: model.DefaultDisplayName})
	if err != nil {
		return model.Principal{}, err
	}

	profile, err := s.profiles.Resolve(ctx, candidate)
	if err != nil {
		return model.Principal{}, err
	}

	if profile.AuthUserID() != string(identity.Subject) {
		return model.Principal{}, model.ErrUnauthenticated
	}

	return model.Principal{Profile: profile, Identity: identity}, nil
}
