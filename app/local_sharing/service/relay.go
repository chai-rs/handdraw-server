// Package service maintains bounded ephemeral sessions without a persistence dependency.
package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/chai-rs/handdraw-server/app/local_sharing/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// Relay serializes activity, admission, source replacement and termination against one server clock.
type Relay struct {
	mu     sync.Mutex
	now    func() time.Time
	rooms  map[string]*room
	peers  map[string]*peer
	bytes  int
	closed bool
}
type grant struct {
	actor, role string
	token       [32]byte
	expires     time.Time
}
type room struct {
	revision      uint64
	id, host      string
	invite        [32]byte
	idle, pending time.Time
	online        bool
	snapshot      []byte
	grants        map[string]*grant
	peers         map[string]*peer
}
type peer struct {
	id          string
	room        *room
	actor, role string
	expires     time.Time
	events      chan model.Event
	ended       chan string
}

// New accepts an injectable server clock for deadline and race tests.
func New(now func() time.Time) *Relay {
	if now == nil {
		now = time.Now
	}

	return &Relay{now: now, rooms: map[string]*room{}, peers: map[string]*peer{}}
}

func secret() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)

	return hex.EncodeToString(b), err
}
func hash(v string) [32]byte { return sha256.Sum256([]byte(v)) }
func (r *Relay) live(id string) (*room, error) {
	if resourceid.Validate(id, model.IDPrefix) != nil {
		return nil, model.ErrInvalid
	}

	r.sweep()

	s := r.rooms[id]
	if r.closed || s == nil {
		return nil, model.ErrEnded
	}

	return s, nil
}

func (r *Relay) issue(s *room, actor, role string) (model.Admission, error) {
	token, err := secret()
	if err != nil {
		return model.Admission{}, err
	}

	now := r.now()
	s.grants[actor] = &grant{actor: actor, role: role, token: hash(token), expires: now.Add(model.TokenTTL)}

	return model.Admission{ID: s.id, Token: token, Role: role, TokenExpiresAt: now.Add(model.TokenTTL), IdleExpiresAt: s.idle, ServerTime: now}, nil
}

// Create allocates an empty room; the host must connect in ten seconds before any Viewer can join.
func (r *Relay) Create(actor string) (model.Admission, error) {
	if resourceid.Validate(actor, model.UserIDPrefix) != nil {
		return model.Admission{}, model.ErrDenied
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.sweep()

	if r.closed {
		return model.Admission{}, model.ErrEnded
	}

	if len(r.rooms) >= 100 {
		return model.Admission{}, model.ErrBudget
	}

	count := 0

	for _, s := range r.rooms {
		if s.host == actor {
			count++
		}
	}

	if count >= 2 {
		return model.Admission{}, model.ErrBudget
	}

	id, err := resourceid.New(model.IDPrefix)
	if err != nil {
		return model.Admission{}, err
	}

	invite, err := secret()
	if err != nil {
		return model.Admission{}, err
	}

	s := &room{id: id, host: actor, invite: hash(invite), idle: r.now().Add(model.IdleTimeout), pending: r.now().Add(10 * time.Second), grants: map[string]*grant{}, peers: map[string]*peer{}}

	admission, err := r.issue(s, actor, "host")
	if err != nil {
		return admission, err
	}

	admission.InviteToken = invite
	r.rooms[id] = s

	return admission, nil
}

// Join checks both the invite capability and signed-in actor, and never promotes a Viewer to host.
func (r *Relay) Join(id, actor, invite string) (model.Admission, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, err := r.live(id)
	if err != nil {
		return model.Admission{}, err
	}

	if resourceid.Validate(actor, model.UserIDPrefix) != nil || len(invite) != 64 || hash(invite) != s.invite {
		return model.Admission{}, model.ErrDenied
	}

	if !s.online {
		return model.Admission{}, model.ErrEnded
	}

	if actor == s.host {
		return r.issue(s, actor, "host")
	}

	if len(s.grants) >= 25 && s.grants[actor] == nil {
		return model.Admission{}, model.ErrBudget
	}

	return r.issue(s, actor, "viewer")
}

// Renew requires an existing in-memory grant and does not count as activity.
func (r *Relay) Renew(id, actor string) (model.Admission, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, err := r.live(id)
	if err != nil {
		return model.Admission{}, err
	}

	g := s.grants[actor]
	if g == nil {
		return model.Admission{}, model.ErrDenied
	}

	return r.issue(s, actor, g.role)
}

// Revoke is host-only and clears all content before returning.
func (r *Relay) Revoke(id, actor string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, err := r.live(id)
	if err != nil {
		return err
	}

	if s.host != actor {
		return model.ErrDenied
	}

	r.end(s, "host_revoked")

	return nil
}

func (r *Relay) admitted(s *room, token string) *grant {
	if len(token) != 64 {
		return nil
	}

	h := hash(token)
	for _, g := range s.grants {
		if g.token == h && g.expires.After(r.now()) {
			return g
		}
	}

	return nil
}

// Attach admits one fixed principal/role per socket and provides the current snapshot to late Viewers.
func (r *Relay) Attach(id, token string) (model.Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, err := r.live(id)
	if err != nil {
		return model.Subscription{}, err
	}

	g := r.admitted(s, token)
	if g == nil {
		return model.Subscription{}, model.ErrDenied
	}

	if len(r.peers) >= model.MaxConnections || len(s.peers) >= model.MaxPeers {
		return model.Subscription{}, model.ErrBudget
	}

	if (g.role == "host" && s.online) || (g.role == "viewer" && !s.online) {
		return model.Subscription{}, model.ErrDenied
	}

	id, err = secret()
	if err != nil {
		return model.Subscription{}, err
	}

	p := &peer{id: id, room: s, actor: g.actor, role: g.role, expires: g.expires, events: make(chan model.Event, 1), ended: make(chan string, 1)}
	s.peers[id] = p

	r.peers[id] = p
	if p.role == "host" {
		s.online = true
	}

	r.broadcast(s)

	return model.Subscription{ID: id, Role: p.role, Events: p.events, Ended: p.ended}, nil
}

// Reauthenticate changes only a socket's token deadline, never its actor or role.
func (r *Relay) Reauthenticate(id, token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.sweep()

	p := r.peers[id]
	if p == nil {
		return model.ErrEnded
	}

	g := r.admitted(p.room, token)
	if g == nil || g.actor != p.actor || g.role != p.role {
		return model.ErrDenied
	}

	p.expires = g.expires

	return nil
}

// Publish replaces only the host's immutable in-memory source; late joins always receive the latest full snapshot.
func (r *Relay) Publish(id string, source []byte) error {
	if len(source) == 0 || len(source) > model.MaxSnapshotBytes || !json.Valid(source) {
		return model.ErrInvalid
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.sweep()

	p := r.peers[id]
	if p == nil {
		return model.ErrEnded
	}

	if p.role != "host" {
		return model.ErrDenied
	}

	s := p.room
	if r.bytes-len(s.snapshot)+len(source) > model.MaxStoredBytes {
		return model.ErrBudget
	}

	r.bytes += len(source) - len(s.snapshot)
	s.snapshot = append([]byte(nil), source...)
	s.revision++
	s.idle = r.now().Add(model.IdleTimeout)
	r.broadcast(s)

	return nil
}

// Activity accepts only foreground interaction kinds and cannot resurrect an expired room.
func (r *Relay) Activity(id, kind string) error {
	switch kind {
	case "edit", "page", "pan_zoom", "keep_alive":
	default:
		return model.ErrInvalid
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.sweep()

	p := r.peers[id]
	if p == nil {
		return model.ErrEnded
	}

	p.room.idle = r.now().Add(model.IdleTimeout)
	r.broadcast(p.room)

	return nil
}

// Presence admits bounded ephemeral cursor metadata from either role without resetting the idle deadline.
func (r *Relay) Presence(id string, value []byte) error {
	if len(value) > 2048 || !json.Valid(value) {
		return model.ErrInvalid
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.sweep()

	p := r.peers[id]
	if p == nil {
		return model.ErrEnded
	}

	for _, other := range p.room.peers {
		if other.id != id {
			select {
			case other.events <- model.Event{Type: "presence", PeerID: id, Presence: append([]byte(nil), value...), ServerTime: r.now(), IdleExpiresAt: p.room.idle}:
			default:
			}
		}
	}

	return nil
}

func (r *Relay) offer(p *peer, event model.Event) {
	select {
	case p.events <- event:
	default:
		select {
		case <-p.events:
		default:
		}

		p.events <- event
	}
}

func (r *Relay) broadcast(s *room) {
	for _, p := range s.peers {
		snapshot := s.snapshot
		if p.role == "host" {
			snapshot = nil
		}

		r.offer(p, model.Event{Type: "session-state", ProtocolVersion: 1, Role: p.role, PeerID: p.id, Peers: len(s.peers), Snapshot: snapshot, Revision: s.revision, ServerTime: r.now(), IdleExpiresAt: s.idle})
	}
}

func (r *Relay) end(s *room, reason string) {
	for id, p := range s.peers {
		select {
		case <-p.events:
		default:
		}

		p.ended <- reason

		delete(r.peers, id)
	}

	r.bytes -= len(s.snapshot)
	s.snapshot = nil
	s.grants = nil
	s.peers = nil
	delete(r.rooms, s.id)
}

func (r *Relay) detach(p *peer, reason string) {
	if p.role == "host" {
		r.end(p.room, reason)
		return
	}

	delete(p.room.peers, p.id)
	delete(r.peers, p.id)

	select {
	case <-p.events:
	default:
	}

	p.ended <- reason

	r.broadcast(p.room)
}

// Detach ends the whole session on host disconnect; a Viewer leaving only removes its presence.
func (r *Relay) Detach(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if p := r.peers[id]; p != nil {
		r.detach(p, "host_disconnected")
	}
}

func (r *Relay) sweep() {
	now := r.now()
	for _, s := range r.rooms {
		if !s.idle.After(now) {
			r.end(s, "idle_timeout")
		} else if !s.online && !s.pending.After(now) {
			r.end(s, "host_absent")
		}
	}

	for _, p := range r.peers {
		if !p.expires.After(now) {
			r.detach(p, "token_expired")
		}
	}
}

// Sweep expires idle rooms and stale tokens under the same lock as activity and publication.
func (r *Relay) Sweep() { r.mu.Lock(); defer r.mu.Unlock(); r.sweep() }

// Close ends every session; a process restart has no recovery source.
func (r *Relay) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.closed = true
	for _, s := range r.rooms {
		r.end(s, "relay_stopped")
	}
}

// Stats returns bounded resource counts for readiness tests and metrics.
func (r *Relay) Stats() model.Stats {
	r.mu.Lock()
	defer r.mu.Unlock()

	return model.Stats{Sessions: len(r.rooms), Connections: len(r.peers), StoredBytes: r.bytes}
}
