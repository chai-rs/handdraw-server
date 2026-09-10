// Package model owns public billing projections; only verified billing workflows may update their source.
package model

import "time"

// Entitlement exposes effective access without provider identifiers or payment credentials.
type Entitlement struct {
	Plan            string     `json:"-"`
	Mode            string     `json:"mode"`
	Reason          string     `json:"reason"`
	GraceEndsAt     *time.Time `json:"grace_ends_at"`
	AccessExpiresAt *time.Time `json:"access_expires_at"`
	RetentionEndsAt *time.Time `json:"retention_ends_at"`
	QuotaBytes      int64      `json:"-"`
}

// Editable describes the trusted projection's content-access state, not the actor's role.
func (e Entitlement) Editable() bool { return e.Mode == "editable" }

// Readable includes the read-only retention window.
func (e Entitlement) Readable() bool { return e.Mode == "editable" || e.Mode == "read_only" }
