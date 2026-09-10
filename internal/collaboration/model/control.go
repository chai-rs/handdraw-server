// Package model defines versioned application controls carried by Hocuspocus stateless frames.
package model

import (
	"errors"
	"strconv"
	"time"

	valx "github.com/chai-rs/handdraw-server/pkg/validator"
)

// ProtocolVersion is the application protocol negotiated before any document sync.
const ProtocolVersion = 1

// BoardIDPrefix identifies the room resource independently of transport session IDs.
const BoardIDPrefix = "brd"

// ErrInvalidControl rejects malformed, unsupported or wrong-direction controls.
var ErrInvalidControl = errors.New("invalid collaboration control")

// Capabilities are server-computed permissions, never authorization supplied by clients.
type Capabilities struct {
	CanRead         bool `json:"can_read"`
	CanEditContent  bool `json:"can_edit_content"`
	CanComment      bool `json:"can_comment"`
	CanExport       bool `json:"can_export"`
	CanManageGuests bool `json:"can_manage_guests"`
}

// Control is a strict discriminated message; only the fields for its type may be present.
type Control struct {
	Type                    string        `json:"type"`
	ProtocolVersion         int           `json:"protocol_version"`
	SupportedSchemaVersions []int         `json:"supported_schema_versions,omitempty"`
	ClientBuild             string        `json:"client_build,omitempty"`
	SessionID               string        `json:"session_id,omitempty"`
	SchemaVersion           int           `json:"schema_version,omitempty"`
	DocumentRevision        string        `json:"document_revision,omitempty"`
	Capabilities            *Capabilities `json:"capabilities,omitempty"`
	HeartbeatIntervalMS     int           `json:"heartbeat_interval_ms,omitempty"`
	Reason                  string        `json:"reason,omitempty"`
	Code                    string        `json:"code,omitempty"`
	ReconnectPolicy         string        `json:"reconnect_policy,omitempty"`
	ID                      string        `json:"id,omitempty"`
	IdleTimeoutSeconds      int           `json:"idle_timeout_seconds,omitempty"`
	IdleExpiresAt           string        `json:"idle_expires_at,omitempty"`
	ServerTime              string        `json:"server_time,omitempty"`
	WarningBeforeSeconds    int           `json:"warning_before_seconds,omitempty"`
	Activity                string        `json:"activity,omitempty"`
}

// Validate checks the active protocol and required fields for the sender's direction.
func (c Control) Validate(fromClient bool) error {
	if c.ProtocolVersion != ProtocolVersion {
		return ErrInvalidControl
	}

	if err := valx.Var(c.Type, valx.Required, valx.UTF8Text, valx.Length(1, 64)); err != nil {
		return ErrInvalidControl
	}

	switch c.Type {
	case "client-hello":
		if !fromClient || len(c.SupportedSchemaVersions) < 1 || len(c.SupportedSchemaVersions) > 8 || c.ClientBuild == "" || len(c.ClientBuild) > 128 {
			return ErrInvalidControl
		}

		seen := map[int]bool{}
		for _, v := range c.SupportedSchemaVersions {
			if v < 1 || seen[v] {
				return ErrInvalidControl
			}

			seen[v] = true
		}
	case "save-checkpoint":
		if !fromClient || c.ID == "" || len(c.ID) > 128 {
			return ErrInvalidControl
		}
	case "session-activity":
		if !fromClient || (c.Activity != "edit" && c.Activity != "pan_zoom" && c.Activity != "page_change" && c.Activity != "keep_alive") {
			return ErrInvalidControl
		}
	case "session-idle-deadline":
		if fromClient || !c.validIdle() {
			return ErrInvalidControl
		}
	case "session-ended":
		if fromClient || c.Reason != "idle_timeout" || c.ReconnectPolicy != "fresh_session" {
			return ErrInvalidControl
		}
	case "session-ready":
		if fromClient || c.SessionID == "" || len(c.SessionID) > 128 || c.SchemaVersion != 1 || !revision(c.DocumentRevision) || c.Capabilities == nil || c.HeartbeatIntervalMS < 1000 || c.HeartbeatIntervalMS > 60000 || !c.validIdle() {
			return ErrInvalidControl
		}
	case "capabilities-changed":
		if fromClient || c.Capabilities == nil || c.Reason == "" || len(c.Reason) > 128 {
			return ErrInvalidControl
		}
	case "saved":
		if fromClient || c.ID == "" || len(c.ID) > 128 || !revision(c.DocumentRevision) {
			return ErrInvalidControl
		}
	case "session-error":
		if fromClient || c.Code == "" || len(c.Code) > 64 || (c.ReconnectPolicy != "fresh_session" && c.ReconnectPolicy != "reload_client" && c.ReconnectPolicy != "none") {
			return ErrInvalidControl
		}
	default:
		return ErrInvalidControl
	}

	return nil
}

func revision(s string) bool {
	n, err := strconv.ParseInt(s, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == s
}

func (c Control) validIdle() bool {
	if (c.IdleTimeoutSeconds != 7200 && c.IdleTimeoutSeconds != 14400) || c.WarningBeforeSeconds != 300 {
		return false
	}

	now, e := time.Parse(time.RFC3339Nano, c.ServerTime)
	if e != nil {
		return false
	}

	deadline, e := time.Parse(time.RFC3339Nano, c.IdleExpiresAt)
	if e != nil {
		return false
	}

	return deadline.After(now) && deadline.Sub(now) <= time.Duration(c.IdleTimeoutSeconds)*time.Second
}
