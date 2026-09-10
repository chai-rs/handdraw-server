package ws

import (
	"bytes"
	"encoding/json"

	"github.com/chai-rs/handdraw-server/internal/collaboration/model"
	yencoding "github.com/reearth/ygo/encoding"
)

func (p *peer) awareness(d *yencoding.Decoder) error {
	data, err := d.ReadVarBytes()
	if err != nil || len(data) > 16<<10 || d.Remaining() != 0 {
		return model.ErrSession
	}

	if _, err = p.check(); err != nil {
		return err
	}

	a := yencoding.NewDecoder(data)

	count, err := a.ReadVarUint()
	if err != nil || count > 5 {
		return model.ErrSession
	}

	p.room.mu.Lock()
	defer p.room.mu.Unlock()

	if p.room.failed {
		return model.ErrSession
	}

	for range count {
		id, err := a.ReadVarUint()
		if err != nil || id == 0 || id > 1<<53-1 {
			return model.ErrSession
		}

		clock, err := a.ReadVarUint()
		if err != nil || clock > 1<<53-2 {
			return model.ErrSession
		}

		state, err := a.ReadVarString()
		if err != nil {
			return err
		}

		if state == "null" && id != p.presenceID {
			continue
		}

		if p.presenceID != 0 && id != p.presenceID {
			return model.ErrSession
		}

		for other := range p.room.peers {
			if other != p && other.presenceID == id {
				return model.ErrSession
			}
		}

		if p.presenceID != 0 && clock <= p.presenceClock {
			continue
		}

		var canonical []byte
		if state == "null" {
			canonical = []byte("null")
		} else {
			var object map[string]json.RawMessage
			if json.Unmarshal([]byte(state), &object) != nil || object == nil {
				return model.ErrSession
			}

			p.infoMu.Lock()
			user := p.access.UserID
			p.infoMu.Unlock()

			identity, _ := json.Marshal(map[string]string{"id": user})
			object["user"] = identity

			canonical, err = json.Marshal(object)
			if err != nil {
				return err
			}
		}

		p.presenceID = id
		p.presenceClock = clock
		p.presence = canonical
		p.room.broadcastPresence(p)
	}

	if a.Remaining() != 0 {
		return model.ErrSession
	}

	return nil
}

func (rm *room) broadcastPresence(source *peer) {
	for recipient := range rm.peers {
		if recipient == source || !recipient.admitted() {
			continue
		}

		if _, err := recipient.check(); err != nil {
			recipient.stop()
			continue
		}

		if err := recipient.sendPresence(source); err != nil {
			recipient.stop()
		}
	}
}

func (p *peer) sendPresence(source *peer) error {
	e := yencoding.NewEncoder()
	e.WriteVarUint(1)
	e.WriteVarUint(source.presenceID)
	e.WriteVarUint(source.presenceClock)
	e.WriteVarString(string(source.presence))

	payload := yencoding.NewEncoder()
	payload.WriteVarBytes(bytes.Clone(e.Bytes()))

	return p.send(1, payload.Bytes())
}
