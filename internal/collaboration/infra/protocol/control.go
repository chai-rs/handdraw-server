// Package protocol encodes application controls inside Hocuspocus binary stateless frames.
package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/chai-rs/handdraw-server/internal/collaboration/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	yencoding "github.com/reearth/ygo/encoding"
)

var fields = map[string][]string{
	"session-activity":      {"activity"},
	"session-idle-deadline": {"idle_timeout_seconds", "idle_expires_at", "server_time", "warning_before_seconds"},
	"session-ended":         {"reason", "reconnect_policy"},
	"client-hello":          {"supported_schema_versions", "client_build"},
	"save-checkpoint":       {"id"},
	"session-ready":         {"session_id", "schema_version", "document_revision", "capabilities", "heartbeat_interval_ms", "idle_timeout_seconds", "idle_expires_at", "server_time", "warning_before_seconds"},
	"capabilities-changed":  {"capabilities", "reason"},
	"saved":                 {"id", "document_revision"},
	"session-error":         {"code", "reconnect_policy"},
}

// Decode rejects broadcast-stateless frames and all client attempts to impersonate server controls.
func Decode(room string, frame []byte, fromClient bool) (model.Control, error) {
	var control model.Control
	if !validRoom(room) || len(frame) > 8192 {
		return control, model.ErrInvalidControl
	}

	d := yencoding.NewDecoder(frame)

	name, err := d.ReadVarString()
	if err != nil || name != room {
		return control, model.ErrInvalidControl
	}

	kind, err := d.ReadVarUint()
	if err != nil || kind != 5 {
		return control, model.ErrInvalidControl
	}

	payload, err := d.ReadVarString()
	if err != nil || d.Remaining() != 0 {
		return control, model.ErrInvalidControl
	}

	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()

	if err = decoder.Decode(&control); err != nil {
		return model.Control{}, model.ErrInvalidControl
	}

	if err = decoder.Decode(new(any)); err != io.EOF {
		return model.Control{}, model.ErrInvalidControl
	}

	if err = control.Validate(fromClient); err != nil {
		return model.Control{}, err
	}

	var object map[string]json.RawMessage
	if err = json.Unmarshal([]byte(payload), &object); err != nil {
		return model.Control{}, model.ErrInvalidControl
	}

	allowed := map[string]bool{"type": true, "protocol_version": true}
	for _, key := range fields[control.Type] {
		allowed[key] = true
		if _, ok := object[key]; !ok {
			return model.Control{}, model.ErrInvalidControl
		}
	}

	for key := range object {
		if !allowed[key] {
			return model.Control{}, model.ErrInvalidControl
		}
	}

	if control.Capabilities != nil {
		var caps map[string]json.RawMessage
		if err := json.Unmarshal(object["capabilities"], &caps); err != nil || len(caps) != 5 {
			return model.Control{}, model.ErrInvalidControl
		}

		for _, key := range []string{"can_read", "can_edit_content", "can_comment", "can_export", "can_manage_guests"} {
			if v, ok := caps[key]; !ok || (!bytes.Equal(v, []byte("true")) && !bytes.Equal(v, []byte("false"))) {
				return model.Control{}, model.ErrInvalidControl
			}
		}
	}

	return control, nil
}

// Encode validates a control and emits room + message type 5 + JSON-string framing.
func Encode(room string, control model.Control, fromClient bool) ([]byte, error) {
	if err := control.Validate(fromClient); err != nil {
		return nil, err
	}

	payload, err := json.Marshal(control)
	if err != nil {
		return nil, err
	}

	e := yencoding.NewEncoder()
	e.WriteVarString(room)
	e.WriteVarUint(5)
	e.WriteVarString(string(payload))

	data := bytes.Clone(e.Bytes())
	if _, err = Decode(room, data, fromClient); err != nil {
		return nil, err
	}

	return data, nil
}

func validRoom(room string) bool {
	return strings.HasPrefix(room, "board:") && resourceid.Validate(strings.TrimPrefix(room, "board:"), model.BoardIDPrefix) == nil
}
