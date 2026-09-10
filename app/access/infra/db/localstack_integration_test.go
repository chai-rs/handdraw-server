//go:build integration

package db_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	boardinput "github.com/chai-rs/handdraw-server/app/board_management/model"
	jobdb "github.com/chai-rs/handdraw-server/internal/job/infra/db"
	jobservice "github.com/chai-rs/handdraw-server/internal/job/service"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const localPassword = "Handdraw-local-only-2026!"

var localProviderURL string

// localAuth exists only in integration-test binaries and only accepts disposable fixture accounts.
func localAuth(w http.ResponseWriter, r *http.Request, accounts map[string]user, key []byte) bool {
	if r.Header.Get("Origin") == "http://127.0.0.1:5175" {
		w.Header().Set("Access-Control-Allow-Origin", "http://127.0.0.1:5175")
		w.Header().Set("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Headers", "authorization,apikey,content-type,x-client-info,x-supabase-api-version")
	w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
	if r.Method == "OPTIONS" {
		w.WriteHeader(204)
		return true
	}
	if r.URL.Path == "/auth/v1/logout" {
		w.WriteHeader(204)
		return true
	}
	if r.URL.Path != "/auth/v1/token" {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	var input struct {
		Email        string `json:"email"`
		Password     string `json:"password"`
		RefreshToken string `json:"refresh_token"`
	}
	if r.Header.Get("apikey") != "local-public-key" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil {
		w.WriteHeader(400)
		return true
	}
	email := input.Email
	if r.URL.Query().Get("grant_type") == "refresh_token" {
		for e, u := range accounts {
			if input.RefreshToken == u.subject {
				email = e
			}
		}
	} else if input.Password != localPassword {
		email = ""
	}
	u, ok := accounts[email]
	if !ok {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "Invalid local demo credentials"})
		return true
	}
	now := time.Now()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": "http://" + r.Host + "/auth/v1", "aud": "authenticated", "role": "authenticated", "sub": u.subject, "email": email, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}).SignedString(key)
	if err != nil {
		w.WriteHeader(500)
		return true
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": token, "token_type": "bearer", "expires_in": 3600, "expires_at": now.Add(time.Hour).Unix(), "refresh_token": u.subject, "user": map[string]any{"id": u.subject, "aud": "authenticated", "role": "authenticated", "email": email, "app_metadata": map[string]string{"provider": "email"}, "user_metadata": map[string]string{}, "created_at": now.Format(time.RFC3339)}})
	return true
}

// TestLocalCloudStack runs the real API/RLS workflows for manual browser acceptance on a disposable local database.
func TestLocalCloudStack(t *testing.T) {
	if os.Getenv("HANDDRAW_LOCAL_STACK") != "1" {
		t.Skip("explicit local browser fixture only")
	}
	s := new(accessSuite)
	s.SetT(t)
	s.SetupSuite()
	f := s.setup(t)
	accounts := map[string]user{"owner@handdraw.test": f.owner, "editor@handdraw.test": f.editor, "viewer@handdraw.test": f.viewer, "outsider@handdraw.test": f.outsider}
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
		_, err := s.boards().Create(ctx, f.workspace, uuid.NewString(), boardinput.CreateBoard{Name: "Get Started", Initialization: "get_started"})
		return err
	}))
	api, _ := s.http(t, accounts)
	config := map[string]any{"api_url": api, "supabase_url": localProviderURL, "publishable_key": "local-public-key", "password": localPassword, "accounts": []string{"owner@handdraw.test", "editor@handdraw.test", "viewer@handdraw.test", "outsider@handdraw.test"}, "workspace_id": f.workspace}
	raw, err := json.MarshalIndent(config, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile("/private/tmp/handdraw-localstack.json", raw, 0o600))
	t.Cleanup(func() { _ = os.Remove("/private/tmp/handdraw-localstack.json") })
	ctx, cancel := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	workerCtx, stopWorker := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		jobservice.NewWorker(jobdb.NewWorker(s.cleanup)).Run(workerCtx, func(err error) { t.Log(err) })
	}()
	defer func() { stopWorker(); <-done }()
	fmt.Println("Local cloud fixture ready: /private/tmp/handdraw-localstack.json (disposable accounts; no live Supabase)")
	select {
	case <-ctx.Done():
	case <-time.After(90 * time.Minute):
	}
}
