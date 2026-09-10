package ws

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/chai-rs/handdraw-server/internal/collaboration/model"
	"github.com/reearth/ygo/crdt"
	yencoding "github.com/reearth/ygo/encoding"
	ysync "github.com/reearth/ygo/sync"
	"github.com/segmentio/ksuid"
)

func (p *peer) send(kind uint64, payload []byte) error {
	e := yencoding.NewEncoder()
	e.WriteVarString(p.name)
	e.WriteVarUint(kind)
	e.WriteRaw(payload)

	return p.write(bytes.Clone(e.Bytes()))
}

func (p *peer) control(c model.Control) error {
	c.ProtocolVersion = model.ProtocolVersion

	data, err := p.server.controls.Encode(p.name, c, false)
	if err != nil {
		return err
	}

	return p.write(data)
}

func (p *peer) handle(frame []byte) error {
	p.infoMu.Lock()
	authenticated, ready := p.token != "", p.ready
	p.infoMu.Unlock()

	if authenticated && len(frame) == 1 && frame[0] == 10 {
		return p.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	}

	d := yencoding.NewDecoder(frame)

	name, err := d.ReadVarString()
	if err != nil || name != p.name {
		return model.ErrSession
	}

	kind, err := d.ReadVarUint()
	if err != nil {
		return err
	}

	if kind == 2 {
		return p.authenticate(d)
	}

	if !authenticated {
		return model.ErrSession
	}

	if kind == 5 {
		return p.handleControl(frame)
	}

	if !ready {
		// The provider eagerly sends Step1/presence after its token. Discard these without disclosure;
		// the client starts sync again after receiving the versioned session-ready control.
		if kind == 0 {
			subtype, _, err := ysync.ReadSyncMessage(d.RemainingBytes())
			if err == nil && subtype == ysync.MsgSyncStep1 {
				return nil
			}
		}

		if kind == 1 && len(frame) < 16<<10 {
			return nil
		}

		return model.ErrSession
	}

	switch kind {
	case 0, 4:
		return p.sync(d.RemainingBytes())
	case 1:
		return p.awareness(d)
	case 3:
		if d.Remaining() != 0 {
			return model.ErrSession
		}

		if _, err = p.check(); err != nil {
			return err
		}

		p.room.mu.Lock()
		defer p.room.mu.Unlock()

		for other := range p.room.peers {
			if other.presenceID != 0 {
				if err = p.sendPresence(other); err != nil {
					return err
				}
			}
		}

		return nil
	case 9:
		return p.send(10, nil)
	case 10:
		return p.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	default:
		return model.ErrSession
	}
}

func (p *peer) authenticate(d *yencoding.Decoder) error {
	subtype, err := d.ReadVarUint()
	if err != nil || subtype != 0 {
		return model.ErrSession
	}

	token, err := d.ReadVarString()
	if err != nil || token == "" || len(token) > 16<<10 {
		return model.ErrSession
	}

	// Hocuspocus 4 appends its provider version to the in-band authentication message.
	if d.Remaining() != 0 {
		version, err := d.ReadVarString()
		if err != nil || version == "" || len(version) > 64 || d.Remaining() != 0 {
			return model.ErrSession
		}
	}

	p.infoMu.Lock()
	user := p.access.UserID
	p.infoMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	a, err := p.server.backend.Check(ctx, token, user, p.board)
	if err != nil {
		return err
	}

	p.infoMu.Lock()
	p.token = token

	ready := p.ready
	if !ready {
		p.access = a
	}
	p.infoMu.Unlock()

	if ready {
		if _, err = p.check(); err != nil {
			return err
		}
	}

	e := yencoding.NewEncoder()
	e.WriteVarUint(2)

	scope := "readonly"
	if a.Capabilities.CanEditContent {
		scope = "read-write"
	}

	e.WriteVarString(scope)

	return p.send(2, e.Bytes())
}

func (p *peer) handleControl(frame []byte) error {
	c, err := p.server.controls.Decode(p.name, frame, true)
	if err != nil {
		return err
	}

	p.infoMu.Lock()
	ready := p.ready
	token, user := p.token, p.access.UserID
	p.infoMu.Unlock()

	if c.Type == "client-hello" {
		if ready {
			return model.ErrSession
		}

		if !slices.Contains(c.SupportedSchemaVersions, 1) {
			_ = p.control(model.Control{Type: "session-error", Code: "schema_upgrade_required", ReconnectPolicy: "reload_client"})
			return model.ErrSession
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		doc, a, err := p.server.backend.Load(ctx, token, user, p.board)
		if err != nil {
			return err
		}

		if err = p.server.join(p, doc); err != nil {
			return err
		}

		now := time.Now().UTC()

		p.infoMu.Lock()

		p.idle, err = model.NewIdle(now, a.IdleSeconds)
		if err != nil {
			p.infoMu.Unlock()
			return err
		}

		p.access = a
		p.infoMu.Unlock()
		_ = p.conn.SetReadDeadline(now.Add(60 * time.Second))

		p.room.mu.Lock()
		defer p.room.mu.Unlock()

		if p.room.failed {
			return model.ErrSession
		}

		if err = p.idleNotice(true); err != nil {
			return err
		}

		p.infoMu.Lock()
		p.ready = true
		p.infoMu.Unlock()

		for other := range p.room.peers {
			if other != p && other.presenceID != 0 {
				if err = p.sendPresence(other); err != nil {
					return err
				}
			}
		}

		return nil
	}

	if !ready {
		return model.ErrSession
	}

	if _, err = p.check(); err != nil {
		return err
	}

	switch c.Type {
	case "session-activity":
		return p.activityNotice()
	case "save-checkpoint":
		p.room.mu.Lock()
		defer p.room.mu.Unlock()

		if p.room.failed {
			return model.ErrSession
		}

		return p.control(model.Control{Type: "saved", ID: c.ID, DocumentRevision: strconv.FormatInt(p.room.doc.Revision, 10)})
	default:
		return model.ErrSession
	}
}

func (p *peer) idleNotice(initial bool) error {
	now := time.Now().UTC()

	p.infoMu.Lock()
	deadline, a := p.idle.Deadline(), p.access
	p.infoMu.Unlock()

	c := model.Control{Type: "session-idle-deadline", IdleTimeoutSeconds: a.IdleSeconds, IdleExpiresAt: deadline.UTC().Format(time.RFC3339Nano), ServerTime: now.Format(time.RFC3339Nano), WarningBeforeSeconds: 300}

	if initial {
		revision := p.room.doc.Revision

		c.Type = "session-ready"
		c.SessionID = ksuid.New().String()
		c.SchemaVersion = 1
		c.DocumentRevision = strconv.FormatInt(revision, 10)
		c.Capabilities = &a.Capabilities
		c.HeartbeatIntervalMS = 20000
	}

	return p.control(c)
}

func (p *peer) activityNotice() error {
	now := time.Now()

	p.infoMu.Lock()
	if !p.idle.Active(now) {
		p.infoMu.Unlock()
		return model.ErrSession
	}

	if now.Sub(p.lastNotice) < time.Second {
		p.infoMu.Unlock()
		return nil
	}

	p.idle.Touch(now)
	p.lastNotice = now
	p.infoMu.Unlock()

	return p.idleNotice(false)
}

func (p *peer) sync(payload []byte) error {
	kind, update, err := ysync.ReadSyncMessage(payload)
	if err != nil {
		return err
	}

	if _, err = p.check(); err != nil {
		return err
	}

	rm := p.room
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if rm.failed {
		return model.ErrSession
	}

	if kind == ysync.MsgSyncStep1 {
		vector, err := crdt.DecodeStateVectorV1(update)
		if err != nil {
			return err
		}

		reply, err := crdt.DiffUpdateV1(rm.doc.State, vector)
		if err != nil {
			return err
		}

		return p.sendSync(ysync.MsgSyncStep2, reply)
	}

	if kind != ysync.MsgSyncStep2 && kind != ysync.MsgUpdate {
		return model.ErrSession
	}

	p.infoMu.Lock()
	token, user := p.token, p.access.UserID
	p.infoMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	doc, _, err := p.server.backend.Apply(ctx, token, user, p.board, rm.doc.Revision, update)
	if err != nil {
		if !errors.Is(err, model.ErrRejected) {
			rm.abort()
		}

		return err
	}

	changed := doc.Revision != rm.doc.Revision
	rm.doc = doc

	if changed {
		if err = p.activityNotice(); err != nil {
			return err
		}

		for other := range rm.peers {
			if other == p || !other.admitted() {
				continue
			}
			// RLS cannot protect bytes already in memory: each recipient is reauthorized before fan-out.
			if _, err = other.check(); err != nil {
				other.stop()
				continue
			}

			if err = other.sendSync(ysync.MsgUpdate, update); err != nil {
				other.stop()
			}
		}
	}

	e := yencoding.NewEncoder()
	e.WriteVarUint(1)

	return p.send(8, e.Bytes())
}

func (p *peer) sendSync(kind uint64, update []byte) error {
	e := yencoding.NewEncoder()
	e.WriteVarUint(kind)
	e.WriteVarBytes(update)

	return p.send(0, e.Bytes())
}
