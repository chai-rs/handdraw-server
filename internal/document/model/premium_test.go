package model_test

import (
	"testing"

	"github.com/chai-rs/handdraw-server/internal/document/model"
	"github.com/stretchr/testify/require"
)

// TestPremiumAdmissionTracksOccurrencesRatherThanClientLabels covers the shared live/copy/import policy.
func TestPremiumAdmissionTracksOccurrencesRatherThanClientLabels(t *testing.T) {
	logo := func(id string) map[string]any {
		return map[string]any{"id": id, "isDeleted": false, "customData": map[string]any{"handdrawShape": map[string]any{"id": "aws-amazon-api-gateway", "category": "general", "paid": false}}}
	}
	snap := func(page, id string, e map[string]any) model.Snapshot {
		return model.Snapshot{Pages: map[string]model.Page{page: {Scene: model.Scene{Elements: map[string]map[string]any{id: e}}}}}
	}
	before := snap("page", "logo", logo("logo"))
	for _, tc := range []struct {
		name   string
		after  model.Snapshot
		paid   bool
		denied bool
	}{
		{"existing logo remains editable", snap("page", "logo", logo("logo")), false, false},
		{"unpaid duplicate", snap("page", "copy", logo("copy")), false, true},
		{"paid duplicate", snap("page", "copy", logo("copy")), true, false},
		{"page copy keeps native ID", snap("copied-page", "logo", logo("logo")), false, true},
		{"stripping provenance", snap("page", "logo", map[string]any{"id": "logo", "isDeleted": false}), false, true},
		{"deletion allowed", model.Snapshot{}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.after.ValidatePremiumTransition(before, model.Validation{}, tc.paid)
			if tc.denied {
				require.ErrorIs(t, err, model.ErrPremium)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestPremiumAssetCannotBeCopiedByRemovingCatalogMetadata gates immutable server provenance.
func TestPremiumAssetCannotBeCopiedByRemovingCatalogMetadata(t *testing.T) {
	s := model.Snapshot{Pages: map[string]model.Page{"p": {Scene: model.Scene{Elements: map[string]map[string]any{"copy": {"fileId": "f"}}, Files: map[string]model.Asset{"f": {AssetID: "asset"}}}}}}
	require.ErrorIs(t, s.ValidatePremiumTransition(model.Snapshot{}, model.Validation{PremiumAssets: map[string]bool{"asset": true}}, false), model.ErrPremium)
}
