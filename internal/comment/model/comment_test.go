package model_test

import (
	"strings"
	"testing"

	"github.com/chai-rs/handdraw-server/internal/comment/model"
	"github.com/stretchr/testify/require"
)

// TestAnchorsRejectAmbiguousTargets prevents coordinate-like or mixed target identities.
func TestAnchorsRejectAmbiguousTargets(t *testing.T) {
	for _, tc := range []struct {
		name  string
		a     model.Anchor
		valid bool
	}{
		{"board", model.Anchor{Kind: "board"}, true},
		{"shape", model.Anchor{Kind: "shape", PageID: "pag_0ujtsYcgvSTl8PAuAdqWYSMnLOv", ElementID: "native-id"}, true},
		{"shape without page", model.Anchor{Kind: "shape", ElementID: "native-id"}, false},
		{"mixed note shape", model.Anchor{Kind: "note", NoteID: "note_0ujtsYcgvSTl8PAuAdqWYSMnLOv", ElementID: "native-id"}, false},
		{"empty kind", model.Anchor{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.a.Validate()
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, model.ErrInvalid)
			}
		})
	}
}

// TestCommentBodiesBoundPlainText rejects blank, excessive and invalid Unicode input.
func TestCommentBodiesBoundPlainText(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{{"hello", true}, {"\n\t", false}, {strings.Repeat("a", 4001), false}, {string([]byte{0xff}), false}} {
		t.Run(tc.body[:min(len(tc.body), 12)], func(t *testing.T) {
			err := (model.Body{Body: tc.body}).Validate()
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, model.ErrInvalid)
			}
		})
	}
}
