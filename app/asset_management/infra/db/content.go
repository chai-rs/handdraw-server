package db

import (
	"context"

	document "github.com/chai-rs/handdraw-server/internal/document/model"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
)

// ContentScope authorizes only committed attachment assets from this board, with server-owned provenance.
func ContentScope(ctx context.Context, board string) (document.Validation, error) {
	scope := document.Validation{BoardID: board, AllowedAssets: map[string]bool{}, PremiumAssets: map[string]bool{}, AssetDigests: map[string]string{}, AssetMIMEs: map[string]string{}}

	tx, err := rlstx.Current(ctx)
	if err != nil {
		return scope, err
	}

	var rows []struct {
		ID      string `bun:"id"`
		Premium bool   `bun:"premium"`
		SHA256  string `bun:"sha256"`
		MIME    string `bun:"mime_type"`
	}
	if err = tx.NewRaw("SELECT id,premium,sha256,mime_type FROM handdraw.assets WHERE board_id=? AND status='available' AND purpose='attachment'", board).Scan(ctx, &rows); err != nil {
		return scope, err
	}

	for _, r := range rows {
		scope.AllowedAssets[r.ID] = true
		scope.PremiumAssets[r.ID] = r.Premium
		scope.AssetDigests[r.ID] = r.SHA256
		scope.AssetMIMEs[r.ID] = r.MIME
	}

	return scope, nil
}
