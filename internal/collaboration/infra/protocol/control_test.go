package protocol_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/chai-rs/handdraw-server/internal/collaboration/infra/protocol"
	"github.com/chai-rs/handdraw-server/internal/collaboration/model"
	yencoding "github.com/reearth/ygo/encoding"
	"github.com/stretchr/testify/require"
)

const room = "board:brd_0ujtsYcgvSTl8PAuAdqWYSMnLOv"

func frame(payload string, kind uint64) []byte {
	e := yencoding.NewEncoder()
	e.WriteVarString(room)
	e.WriteVarUint(kind)
	e.WriteVarString(payload)
	return e.Bytes()
}

// TestVersionedFixtures enforces every version-one control, sender direction and exact round-trip fields.
func TestVersionedFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../../../contracts/control-v1.json")
	require.NoError(t, err)
	var fixtures []json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fixtures))
	require.Len(t, fixtures, 9)
	for _, raw := range fixtures {
		var value model.Control
		require.NoError(t, json.Unmarshal(raw, &value))
		t.Run(value.Type, func(t *testing.T) {
			client := value.Type == "client-hello" || value.Type == "save-checkpoint" || value.Type == "session-activity"
			decoded, err := protocol.Decode(room, frame(string(raw), 5), client)
			require.NoError(t, err)
			require.Equal(t, value, decoded)
			encoded, err := protocol.Encode(room, decoded, client)
			require.NoError(t, err)
			again, err := protocol.Decode(room, encoded, client)
			require.NoError(t, err)
			require.Equal(t, value, again)
			_, err = protocol.Decode(room, encoded, !client)
			require.ErrorIs(t, err, model.ErrInvalidControl)
			_, err = protocol.Decode(room, frame(string(raw), 6), client)
			require.ErrorIs(t, err, model.ErrInvalidControl)
		})
	}
}

// TestMalformedControlsRejectBeforeBinding covers field spoofing, missing permissions, precision and idle deadlines.
func TestMalformedControlsRejectBeforeBinding(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		client        bool
	}{
		{"unknown version", `{"type":"client-hello","protocol_version":2,"supported_schema_versions":[1],"client_build":"x"}`, true},
		{"injected capability", `{"type":"client-hello","protocol_version":1,"supported_schema_versions":[1],"client_build":"x","capabilities":{}}`, true},
		{"missing capability fields", `{"type":"capabilities-changed","protocol_version":1,"capabilities":{},"reason":"changed"}`, false},
		{"numeric revision", `{"type":"saved","protocol_version":1,"id":"x","document_revision":42}`, false},
		{"overflow revision", `{"type":"saved","protocol_version":1,"id":"x","document_revision":"9223372036854775808"}`, false},
		{"leading zero", `{"type":"saved","protocol_version":1,"id":"x","document_revision":"042"}`, false},
		{"unknown field", `{"type":"save-checkpoint","protocol_version":1,"id":"x","role":"owner"}`, true},
		{"trailing JSON", `{"type":"save-checkpoint","protocol_version":1,"id":"x"}{}`, true},
		{"expired deadline", `{"type":"session-idle-deadline","protocol_version":1,"idle_timeout_seconds":7200,"idle_expires_at":"2026-09-10T00:00:00Z","server_time":"2026-09-10T00:00:00Z","warning_before_seconds":300}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := protocol.Decode(room, frame(tc.payload, 5), tc.client)
			require.ErrorIs(t, err, model.ErrInvalidControl)
		})
	}
}
