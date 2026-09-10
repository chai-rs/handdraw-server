// Package api serves authenticated ephemeral sharing admission and a separate Local WebSocket namespace.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	"github.com/chai-rs/handdraw-server/app/local_sharing/model"
	"github.com/chai-rs/handdraw-server/app/local_sharing/service"
	identity "github.com/chai-rs/handdraw-server/internal/identity/model"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
)

// Server uses identity verification for admission, with no board or object-store dependency.
type Server struct {
	auth    access.Authenticator
	relay   *service.Relay
	origins []string
	mu      sync.Mutex
	sockets map[*websocket.Conn]bool
	closed  bool
	stop    chan struct{}
	wg      sync.WaitGroup
	once    sync.Once
}

// New requires explicit origins and starts only a bounded in-memory deadline sweeper.
func New(auth access.Authenticator, relay *service.Relay, origins []string) (*Server, error) {
	if auth == nil || relay == nil || len(origins) == 0 {
		return nil, model.ErrInvalid
	}

	for _, origin := range origins {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(origin, "*") {
			return nil, model.ErrInvalid
		}
	}

	s := &Server{auth: auth, relay: relay, origins: append([]string(nil), origins...), sockets: map[*websocket.Conn]bool{}, stop: make(chan struct{})}
	s.wg.Go(func() {
		timer := time.NewTicker(time.Second)
		defer timer.Stop()

		for {
			select {
			case <-s.stop:
				return
			case <-timer.C:
				relay.Sweep()
			}
		}
	})

	return s, nil
}

// Register mounts Local-only admission endpoints and its in-band WebSocket protocol.
func (s *Server) Register(r fiber.Router) {
	r.Post("/local-share-sessions", s.create)
	r.Post("/local-share-sessions/:session_id/join", s.join)
	r.Post("/local-share-sessions/:session_id/tokens", s.renew)
	r.Delete("/local-share-sessions/:session_id", s.revoke)
	r.Get("/local-collaboration", func(c fiber.Ctx) error {
		room := c.Query("room")
		if !strings.HasPrefix(room, "local:") || resourceid.Validate(strings.TrimPrefix(room, "local:"), model.IDPrefix) != nil {
			return publicError(model.ErrInvalid)
		}

		if c.Query("token") != "" || c.Query("invite_token") != "" {
			return publicError(model.ErrInvalid)
		}

		if !websocket.IsWebSocketUpgrade(c) {
			return fiber.ErrUpgradeRequired
		}

		return c.Next()
	}, websocket.New(s.socket, websocket.Config{Origins: s.origins, HandshakeTimeout: 10 * time.Second}))
}

func (s *Server) actor(c fiber.Ctx) (string, error) {
	headers := c.Request().Header.PeekAll("Authorization")
	if len(headers) != 1 {
		return "", identity.ErrUnauthenticated
	}

	parts := strings.Fields(string(headers[0]))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", identity.ErrUnauthenticated
	}

	ctx, cancel := context.WithTimeout(c.Context(), 10*time.Second)
	defer cancel()

	p, err := s.auth.Authenticate(ctx, identity.AccessToken(parts[1]))
	if err != nil {
		return "", err
	}

	return p.Profile.ID(), nil
}

func publicError(err error) error {
	status, code := 503, "dependency_unavailable"

	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		status, code = 401, "unauthenticated"
	case errors.Is(err, model.ErrDenied):
		status, code = 403, "permission_denied"
	case errors.Is(err, model.ErrEnded):
		status, code = 410, "share_session_ended"
	case errors.Is(err, model.ErrInvalid):
		status, code = 400, "invalid_request"
	case errors.Is(err, model.ErrBudget):
		status, code = 429, "share_capacity_exceeded"
	}

	return fx.RequestError(status, code, strings.ReplaceAll(code, "_", " "), err)
}

func body(c fiber.Ctx, dst any) error {
	if !strings.HasPrefix(c.Get("Content-Type"), "application/json") || len(c.Body()) > 4096 {
		return model.ErrInvalid
	}

	if err := json.Unmarshal(c.Body(), dst); err != nil {
		return model.ErrInvalid
	}

	return nil
}

func (s *Server) create(c fiber.Ctx) error {
	actor, err := s.actor(c)
	if err != nil {
		return publicError(err)
	}

	var input model.CreateRequest
	if body(c, &input) != nil || input.Validate() != nil {
		return publicError(model.ErrInvalid)
	}

	a, err := s.relay.Create(actor)
	if err != nil {
		return publicError(err)
	}

	c.Set("Cache-Control", "no-store")
	c.Status(201)

	return fx.Success(c, a)
}

func (s *Server) join(c fiber.Ctx) error {
	actor, err := s.actor(c)
	if err != nil {
		return publicError(err)
	}

	var input struct {
		Invite string `json:"invite_token"`
	}
	if body(c, &input) != nil {
		return publicError(model.ErrInvalid)
	}

	a, err := s.relay.Join(c.Params("session_id"), actor, input.Invite)
	if err != nil {
		return publicError(err)
	}

	c.Set("Cache-Control", "no-store")

	return fx.Success(c, a)
}

func (s *Server) renew(c fiber.Ctx) error {
	actor, err := s.actor(c)
	if err != nil {
		return publicError(err)
	}

	a, err := s.relay.Renew(c.Params("session_id"), actor)
	if err != nil {
		return publicError(err)
	}

	c.Set("Cache-Control", "no-store")

	return fx.Success(c, a)
}

func (s *Server) revoke(c fiber.Ctx) error {
	actor, err := s.actor(c)
	if err != nil {
		return publicError(err)
	}

	if err = s.relay.Revoke(c.Params("session_id"), actor); err != nil {
		return publicError(err)
	}

	return c.SendStatus(204)
}

type frame struct {
	Type     string          `json:"type"`
	Token    string          `json:"token"`
	Protocol int             `json:"protocol_version"`
	Kind     string          `json:"kind"`
	Snapshot json.RawMessage `json:"snapshot"`
	Presence json.RawMessage `json:"presence"`
}

func read(conn *websocket.Conn) (frame, error) {
	kind, data, err := conn.ReadMessage()
	if err != nil {
		return frame{}, err
	}

	if kind != websocket.TextMessage {
		return frame{}, model.ErrInvalid
	}

	var f frame

	err = json.Unmarshal(data, &f)

	return f, err
}

func (s *Server) socket(conn *websocket.Conn) {
	s.mu.Lock()
	if s.closed || len(s.sockets) >= model.MaxConnections {
		s.mu.Unlock()

		_ = conn.Close()

		return
	}

	s.sockets[conn] = false
	s.wg.Add(1)
	s.mu.Unlock()

	defer func() { _ = conn.Close(); s.mu.Lock(); delete(s.sockets, conn); s.mu.Unlock(); s.wg.Done() }()

	conn.SetReadLimit(4096)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	auth, err := read(conn)
	if err != nil || auth.Type != "auth" || auth.Protocol != 1 {
		return
	}

	sub, err := s.relay.Attach(strings.TrimPrefix(conn.Query("room"), "local:"), auth.Token)
	if err != nil {
		_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		_ = conn.WriteJSON(model.Event{Type: "session-ended", Reason: "admission_denied"})

		return
	}

	s.mu.Lock()
	s.sockets[conn] = true

	s.mu.Unlock()

	done := make(chan struct{})
	writerDone := make(chan struct{})

	defer func() { s.relay.Detach(sub.ID); close(done); _ = conn.Close(); <-writerDone }()

	go func() {
		defer close(writerDone)
		defer func() { _ = conn.Close() }()

		var revision uint64

		for {
			select {
			case <-done:
				return
			case reason := <-sub.Ended:
				_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
				_ = conn.WriteJSON(model.Event{Type: "session-ended", Reason: reason})

				return
			case event := <-sub.Events:
				if event.Type == "session-state" {
					if event.Revision == revision {
						event.Snapshot = nil
					}

					revision = event.Revision
				}

				_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
				if conn.WriteJSON(event) != nil {
					return
				}
			}
		}
	}()

	if sub.Role == "host" {
		conn.SetReadLimit(model.MaxSnapshotBytes + 4096)
	}

	_ = conn.SetReadDeadline(time.Now().Add(model.TokenTTL + time.Second))
	for {
		f, err := read(conn)
		if err != nil {
			return
		}

		switch f.Type {
		case "snapshot":
			err = s.relay.Publish(sub.ID, f.Snapshot)
		case "session-activity":
			err = s.relay.Activity(sub.ID, f.Kind)
		case "reauth":
			err = s.relay.Reauthenticate(sub.ID, f.Token)
			if err == nil {
				_ = conn.SetReadDeadline(time.Now().Add(model.TokenTTL + time.Second))
			}
		case "presence":
			err = s.relay.Presence(sub.ID, f.Presence)
		default:
			err = model.ErrInvalid
		}

		if err != nil {
			return
		}
	}
}

// Close terminates connected and unauthenticated sockets and clears every retained source snapshot.
func (s *Server) Close() {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true

		connections := make([]*websocket.Conn, 0, len(s.sockets))
		for c, admitted := range s.sockets {
			if !admitted {
				connections = append(connections, c)
			}
		}
		s.mu.Unlock()
		s.relay.Close()
		close(s.stop)

		for _, c := range connections {
			_ = c.Close()
		}

		s.wg.Wait()
	})
}
