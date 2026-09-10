// Package model owns verified Auth identities and stable Handdraw profiles.
package model

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	valx "github.com/chai-rs/handdraw-server/pkg/validator"
	"github.com/google/uuid"
)

const (
	// ProfileIDPrefix identifies stable Handdraw profiles independently of Auth UUIDs.
	ProfileIDPrefix = "usr"
	// MaxDisplayNameRunes bounds profile display names.
	MaxDisplayNameRunes = 200
	// DefaultDisplayName initializes a profile without trusting authorization data from user metadata.
	DefaultDisplayName = "Developer"
	// MaxAccessTokenBytes bounds bearer tokens before parsing or making a provider request.
	MaxAccessTokenBytes = 16384
)

var (
	// ErrUnauthenticated covers invalid credentials and unavailable or tombstoned identities.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrUnavailable means the authentication provider or identity store could not complete the request.
	ErrUnavailable = errors.New("identity service unavailable")
	// ErrInvalidProfile rejects invalid or inconsistent persisted profile fields.
	ErrInvalidProfile = errors.New("invalid profile")
)

// AccessToken is an opaque credential; never serialize or log its value.
type AccessToken string

// Validate rejects empty, oversized, or whitespace-bearing credentials.
func (t AccessToken) Validate() error {
	value := string(t)
	if err := valx.Var(value, valx.Required, valx.Length(1, MaxAccessTokenBytes), valx.UTF8Text); err != nil {
		return ErrUnauthenticated
	}

	if strings.ContainsAny(value, " \t\r\n") {
		return ErrUnauthenticated
	}

	return nil
}

// AuthSubject is the UUID assigned by Supabase Auth, not a Handdraw resource ID.
type AuthSubject string

// Validate requires a canonical nonzero UUID from the configured Auth provider.
func (s AuthSubject) Validate() error {
	id, err := uuid.Parse(string(s))
	if err != nil || id == uuid.Nil || id.String() != string(s) {
		return ErrUnauthenticated
	}

	return nil
}

// AuthIdentity carries provider-verified information; implementations of Verifier establish its trust.
type AuthIdentity struct {
	Subject       AuthSubject `json:"-"`
	Email         string      `json:"-"`
	EmailVerified bool        `json:"-"`
	ExpiresAt     time.Time   `json:"-"`
}

// Validate checks the verified result's shape and current lifetime before profile resolution.
func (i AuthIdentity) Validate() error {
	if err := i.Subject.Validate(); err != nil {
		return err
	}

	if err := valx.Struct(&i,
		valx.Field(&i.Email, valx.When(i.Email != "", valx.EmailFormat)),
		valx.Field(&i.ExpiresAt, valx.Required, valx.TimeGT(time.Now())),
	); err != nil {
		return ErrUnauthenticated
	}

	return nil
}

// Profile is immutable application identity; Auth owns credentials and current email.
type Profile struct {
	id          string
	authUserID  string
	displayName string
	createdAt   time.Time
	updatedAt   time.Time
}

// NewProfileParams contains the verified Auth mapping and initial display name.
type NewProfileParams struct {
	AuthUserID  AuthSubject `json:"-"`
	DisplayName string      `json:"display_name"`
}

// Normalize returns a copy with normalized display text.
func (p NewProfileParams) Normalize() NewProfileParams {
	p.DisplayName = strings.TrimSpace(p.DisplayName)
	return p
}

// Validate checks the Auth mapping and bounded display name.
func (p NewProfileParams) Validate() error {
	if err := p.AuthUserID.Validate(); err != nil {
		return err
	}

	if err := valx.Struct(&p, valx.Field(&p.DisplayName, valx.Required, valx.RuneLength(1, MaxDisplayNameRunes), valx.UTF8Text)); err != nil {
		return ErrInvalidProfile
	}

	return nil
}

// NewProfile assigns the model-owned prefix; persistence supplies timestamps.
func NewProfile(p NewProfileParams) (Profile, error) {
	p = p.Normalize()
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}

	id, err := resourceid.New(ProfileIDPrefix)
	if err != nil {
		return Profile{}, err
	}

	return Profile{id: id, authUserID: string(p.AuthUserID), displayName: p.DisplayName}, nil
}

// RehydrateProfileParams is a live profile returned by the restricted resolver.
type RehydrateProfileParams struct {
	ID          string      `json:"id"`
	AuthUserID  AuthSubject `json:"-"`
	DisplayName string      `json:"display_name"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// Validate rejects corrupt identities, noncanonical names, and invalid timestamps.
func (p RehydrateProfileParams) Validate() error {
	if err := (NewProfileParams{AuthUserID: p.AuthUserID, DisplayName: p.DisplayName}).Validate(); err != nil {
		return err
	}

	if err := valx.Struct(&p,
		valx.Field(&p.ID, valx.NewIDRule("profile", ProfileIDPrefix)),
		valx.Field(&p.DisplayName, valx.In(strings.TrimSpace(p.DisplayName))),
		valx.Field(&p.CreatedAt, valx.Required),
		valx.Field(&p.UpdatedAt, valx.Required, valx.TimeGTE(p.CreatedAt)),
	); err != nil {
		return ErrInvalidProfile
	}

	return nil
}

// RehydrateProfile restores a validated live profile without changing its identity.
func RehydrateProfile(p RehydrateProfileParams) (Profile, error) {
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}

	return Profile{id: p.ID, authUserID: string(p.AuthUserID), displayName: p.DisplayName, createdAt: p.CreatedAt, updatedAt: p.UpdatedAt}, nil
}

// ID returns the stable Handdraw user ID.
func (p Profile) ID() string { return p.id }

// AuthUserID returns the immutable Auth UUID mapping.
func (p Profile) AuthUserID() string { return p.authUserID }

// DisplayName returns the user's stored display name.
func (p Profile) DisplayName() string { return p.displayName }

// CreatedAt returns the database creation timestamp.
func (p Profile) CreatedAt() time.Time { return p.createdAt }

// UpdatedAt returns the latest database update timestamp.
func (p Profile) UpdatedAt() time.Time { return p.updatedAt }

// Principal combines a stable profile with current provider-verified identity information.
type Principal struct {
	Profile  Profile      `json:"-"`
	Identity AuthIdentity `json:"-"`
}

// Verifier authenticates a token with the configured provider before returning identity information.
//
//mockery:generate: true
type Verifier interface {
	Verify(context.Context, AccessToken) (AuthIdentity, error)
}

// ProfileRepository resolves a verified Auth subject atomically, preserving an existing profile.
//
//mockery:generate: true
type ProfileRepository interface {
	Resolve(context.Context, Profile) (Profile, error)
}
