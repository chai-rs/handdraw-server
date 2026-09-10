// Package ws serves the bounded Hocuspocus/Yjs transport through Fiber.
package ws

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/chai-rs/handdraw-server/internal/collaboration/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
)

// Config requires explicit browser origins; limits count sockets, including unauthenticated ones.
type Config struct {
	Origins        []string
	MaxConnections int
	MaxPeers       int
}

// Server holds one serial room per board; durable state is owned by the authenticated backend.
type Server struct {
	backend  model.Backend
	controls model.Controls
	config   Config
	mu       sync.Mutex
	peers    map[*peer]bool
	rooms    map[string]*room
	closed   bool
	wg       sync.WaitGroup
}

type room struct {
	mu     sync.Mutex
	doc    model.Document
	peers  map[*peer]bool
	failed bool
}

type peer struct {
	server        *Server
	conn          *websocket.Conn
	room          *room
	name          string
	board         string
	writeMu       sync.Mutex
	infoMu        sync.Mutex
	access        model.Access
	token         string
	ready         bool
	idle          model.Idle
	lastNotice    time.Time
	presenceID    uint64
	presenceClock uint64
	presence      []byte
	done          chan struct{}
	once          sync.Once
}

// New refuses wildcard origins and guards the beta process at five connections per board.
func New(backend model.Backend, controls model.Controls, config Config) (*Server, error) {
	if backend == nil || controls == nil || len(config.Origins) == 0 {
		return nil, errors.New("collaboration requires backend and explicit origins")
	}

	for _, origin := range config.Origins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(origin, "*") {
			return nil, errors.New("invalid collaboration origin")
		}
	}

	if config.MaxConnections == 0 {
		config.MaxConnections = 25
	}

	if config.MaxPeers == 0 {
		config.MaxPeers = 5
	}

	if config.MaxConnections < 1 || config.MaxConnections > 25 || config.MaxPeers < 1 || config.MaxPeers > 5 {
		return nil, errors.New("invalid collaboration connection limits")
	}

	return &Server{backend: backend, controls: controls, config: config, peers: map[*peer]bool{}, rooms: map[string]*room{}}, nil
}

// Register mounts the authenticated in-band protocol; bearer tokens never belong in URLs.
func (s *Server) Register(router fiber.Router) {
	router.Get("/collaboration", func(c fiber.Ctx) error {
		name := c.Query("room")
		if !strings.HasPrefix(name, "board:") || resourceid.Validate(strings.TrimPrefix(name, "board:"), model.BoardIDPrefix) != nil {
			return fiber.ErrBadRequest
		}

		if !websocket.IsWebSocketUpgrade(c) {
			return fiber.ErrUpgradeRequired
		}

		return c.Next()
	}, websocket.New(s.serve, websocket.Config{Origins: s.config.Origins, HandshakeTimeout: 10 * time.Second}))
}

func (s *Server) serve(conn *websocket.Conn) {
	p := &peer{server: s, conn: conn, name: conn.Query("room"), done: make(chan struct{})}
	p.board = strings.TrimPrefix(p.name, "board:")

	s.mu.Lock()
	if s.closed || len(s.peers) >= s.config.MaxConnections {
		s.mu.Unlock()

		_ = conn.Close()

		return
	}

	s.peers[p] = true
	s.wg.Add(1)

	s.mu.Unlock()
	defer s.wg.Done()
	defer s.leave(p)

	conn.SetReadLimit((2 << 20) + 1024)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	maintained := make(chan struct{})
	go func() { defer close(maintained); p.maintain() }()

	defer func() { p.stop(); <-maintained }()

	for {
		kind, frame, err := conn.ReadMessage()
		if err != nil {
			return
		}

		if kind != websocket.BinaryMessage {
			p.fail("invalid_frame")
			return
		}

		if err = p.handle(frame); err != nil {
			p.fail("session_unavailable")
			return
		}
	}
}

func (s *Server) join(p *peer, doc model.Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return model.ErrSession
	}

	rm := s.rooms[p.board]
	if rm == nil {
		rm = &room{doc: doc, peers: map[*peer]bool{}}
		s.rooms[p.board] = rm
	}

	rm.mu.Lock()
	defer rm.mu.Unlock()

	if rm.failed || len(rm.peers) >= s.config.MaxPeers {
		return model.ErrSession
	}

	if rm.doc.Revision < doc.Revision {
		rm.abort()
		return model.ErrConflict
	}

	rm.peers[p] = true
	p.room = rm

	return nil
}

func (s *Server) leave(p *peer) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.peers, p)

	if p.room == nil {
		return
	}

	rm := p.room
	rm.mu.Lock()
	defer rm.mu.Unlock()

	delete(rm.peers, p)

	if p.presenceID != 0 {
		p.presenceClock++
		p.presence = []byte("null")
		rm.broadcastPresence(p)
	}

	if len(rm.peers) == 0 {
		delete(s.rooms, p.board)
	}
}

// Close prevents new sockets and joins every handler before its database authority is released.
func (s *Server) Close() {
	s.mu.Lock()

	s.closed = true
	for p := range s.peers {
		p.stop()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (p *peer) stop() { p.once.Do(func() { close(p.done); _ = p.conn.Close() }) }

func (p *peer) fail(code string) {
	_ = p.control(model.Control{Type: "session-error", Code: code, ReconnectPolicy: "fresh_session"})
	p.stop()
}

func (rm *room) abort() {
	rm.failed = true
	for p := range rm.peers {
		p.stop()
	}
}

func (p *peer) write(data []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	select {
	case <-p.done:
		return model.ErrSession
	default:
	}

	if err := p.conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}

	return p.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (p *peer) check() (model.Access, error) {
	p.infoMu.Lock()
	token, user := p.token, p.access.UserID
	p.infoMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	a, err := p.server.backend.Check(ctx, token, user, p.board)
	if err != nil {
		return model.Access{}, err
	}

	p.infoMu.Lock()
	previous := p.access
	p.access = a

	ready := p.ready
	if a.IdleSeconds != previous.IdleSeconds && ready {
		if !p.idle.SetTier(time.Now(), a.IdleSeconds) {
			p.infoMu.Unlock()
			return model.Access{}, model.ErrSession
		}
	}

	expired := ready && !p.idle.Active(time.Now())
	p.infoMu.Unlock()

	if expired {
		return model.Access{}, model.ErrSession
	}

	if ready && a.Capabilities != previous.Capabilities {
		if err = p.control(model.Control{Type: "capabilities-changed", Capabilities: &a.Capabilities, Reason: "access_changed"}); err != nil {
			return model.Access{}, err
		}
	}

	if ready && a.IdleSeconds != previous.IdleSeconds {
		if err = p.idleNotice(false); err != nil {
			return model.Access{}, err
		}
	}

	return a, nil
}

func (p *peer) maintain() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	nextCheck := time.Now().Add(15 * time.Second)
	nextHeartbeat := time.Now().Add(20 * time.Second)

	for {
		select {
		case <-p.done:
			return
		case now := <-ticker.C:
			p.infoMu.Lock()
			ready, deadline := p.ready, p.idle.Deadline()
			p.infoMu.Unlock()

			if !ready {
				continue
			}

			if !now.Before(deadline) {
				_ = p.control(model.Control{Type: "session-ended", Reason: "idle_timeout", ReconnectPolicy: "fresh_session"})
				p.stop()

				return
			}

			if !now.Before(nextCheck) {
				if _, err := p.check(); err != nil {
					p.fail("access_revoked")
					return
				}

				nextCheck = now.Add(15 * time.Second)
			}

			if !now.Before(nextHeartbeat) {
				if err := p.write([]byte{9}); err != nil {
					p.stop()
					return
				}

				nextHeartbeat = now.Add(20 * time.Second)
			}
		}
	}
}

func (p *peer) admitted() bool { p.infoMu.Lock(); defer p.infoMu.Unlock(); return p.ready }
