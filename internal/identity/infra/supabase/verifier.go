// Package supabase verifies credentials with Supabase Auth rather than accepting decoded claims as proof.
package supabase

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chai-rs/handdraw-server/internal/identity/model"
	valx "github.com/chai-rs/handdraw-server/pkg/validator"
	"github.com/golang-jwt/jwt/v5"
)

// Config pins the trusted project and bounds every provider request.
type Config struct {
	ProjectURL     string        `split_words:"true"`
	PublishableKey string        `split_words:"true" json:"-"`
	Audience       string        `default:"authenticated"`
	Timeout        time.Duration `default:"5s"`
}

// Validate allows HTTPS projects and explicitly local HTTP test/development servers.
func (c Config) Validate() error {
	if err := valx.Struct(&c, valx.Field(&c.ProjectURL, valx.Required), valx.Field(&c.PublishableKey, valx.Required), valx.Field(&c.Audience, valx.Required), valx.Field(&c.Timeout, valx.Required, valx.Min(time.Millisecond), valx.Max(30*time.Second))); err != nil {
		return errors.New("invalid Supabase configuration")
	}

	u, err := url.Parse(c.ProjectURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("invalid Supabase project URL")
	}

	local := u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return errors.New("Supabase requires HTTPS")
	}

	return nil
}

type verifier struct {
	endpoint string
	issuer   string
	key      string
	audience string
	client   *http.Client
}

var _ model.Verifier = (*verifier)(nil)

// New constructs a verifier with no redirect following or credential cache.
func New(config Config) (*verifier, error) {
	if config.Audience == "" {
		config.Audience = "authenticated"
	}

	if config.Timeout == 0 {
		config.Timeout = 5 * time.Second
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	issuer := strings.TrimRight(config.ProjectURL, "/") + "/auth/v1"

	return &verifier{endpoint: issuer + "/user", issuer: issuer, key: config.PublishableKey, audience: config.Audience, client: &http.Client{Timeout: config.Timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type accessClaims struct {
	jwt.RegisteredClaims
	Role      string `json:"role"`
	Anonymous bool   `json:"is_anonymous"`
}

type authUser struct {
	ID               string     `json:"id"`
	Email            string     `json:"email"`
	EmailConfirmedAt *time.Time `json:"email_confirmed_at"`
	Anonymous        bool       `json:"is_anonymous"`
}

// Verify delegates signature/key-rotation handling to Auth and checks claims against its trusted response.
func (v *verifier) Verify(ctx context.Context, raw model.AccessToken) (model.AuthIdentity, error) {
	if err := raw.Validate(); err != nil {
		return model.AuthIdentity{}, err
	}

	claims := new(accessClaims)
	// Parsing supplies rejection checks only; the Auth server must still accept this exact token.
	token, _, err := jwt.NewParser().ParseUnverified(string(raw), claims)
	if err != nil {
		return model.AuthIdentity{}, model.ErrUnauthenticated
	}

	switch token.Method.Alg() {
	case "HS256", "ES256", "RS256":
	default:
		return model.AuthIdentity{}, model.ErrUnauthenticated
	}

	validator := jwt.NewValidator(jwt.WithIssuer(v.issuer), jwt.WithAudience(v.audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err := validator.Validate(claims); err != nil {
		return model.AuthIdentity{}, model.ErrUnauthenticated
	}

	if claims.Role != "authenticated" || claims.Anonymous {
		return model.AuthIdentity{}, model.ErrUnauthenticated
	}

	if err := model.AuthSubject(claims.Subject).Validate(); err != nil {
		return model.AuthIdentity{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.endpoint, nil)
	if err != nil {
		return model.AuthIdentity{}, model.ErrUnavailable
	}

	req.Header.Set("apikey", v.key)
	req.Header.Set("Authorization", "Bearer "+string(raw))

	response, err := v.client.Do(req)
	if err != nil {
		return model.AuthIdentity{}, model.ErrUnavailable
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return model.AuthIdentity{}, model.ErrUnauthenticated
	}

	if response.StatusCode != http.StatusOK {
		return model.AuthIdentity{}, model.ErrUnavailable
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(body) > 65536 {
		return model.AuthIdentity{}, model.ErrUnavailable
	}

	var user authUser
	if err := json.Unmarshal(body, &user); err != nil {
		return model.AuthIdentity{}, model.ErrUnavailable
	}

	if user.ID != claims.Subject || user.Anonymous {
		return model.AuthIdentity{}, model.ErrUnauthenticated
	}

	identity := model.AuthIdentity{Subject: model.AuthSubject(user.ID), Email: user.Email, EmailVerified: user.EmailConfirmedAt != nil, ExpiresAt: claims.ExpiresAt.Time}
	if err := identity.Validate(); err != nil {
		return model.AuthIdentity{}, err
	}

	return identity, nil
}
