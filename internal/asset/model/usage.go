// Package model owns workspace storage accounting projections independently of billing policy.
package model

import "errors"

// Usage tracks committed and reserved bytes, both of which consume storage capacity.
type Usage struct {
	UsedBytes     int64 `json:"used_bytes"`
	ReservedBytes int64 `json:"reserved_bytes"`
	Revision      int64 `json:"revision,string"`
}

// Validate rejects corrupt counters; quota exhaustion never removes existing content access.
func (u Usage) Validate() error {
	if u.UsedBytes < 0 || u.ReservedBytes < 0 || u.Revision < 1 {
		return errors.New("invalid workspace usage")
	}

	return nil
}
