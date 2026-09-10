package model

import (
	_ "embed"
	"encoding/json"
	"errors"
)

// ErrPremium prevents new catalog occurrences without a current paid insertion entitlement.
var ErrPremium = errors.New("premium insertion requires entitlement")

//go:embed catalog/premium.json
var premiumCatalogJSON []byte

var premiumCatalog = func() map[string]string {
	var entries map[string]string
	if err := json.Unmarshal(premiumCatalogJSON, &entries); err != nil {
		panic(err)
	}

	return entries
}()

// CatalogDigest returns the pinned server catalog's SHA-256, never a client category or paid flag.
func CatalogDigest(id string) (string, bool) { v, ok := premiumCatalog[id]; return v, ok }

// ValidatePremiumTransition preserves existing occurrences while gating insertion, copy, page duplication and asset replacement.
func (s Snapshot) ValidatePremiumTransition(before Snapshot, scope Validation, allowed bool) error {
	for pageID, p := range s.Pages {
		for id, e := range p.Scene.Elements {
			if e["isDeleted"] == true {
				continue
			}

			prior := before.Pages[pageID].Scene.Elements[id]

			currentCatalog, valid := catalogID(e)
			if !valid {
				return ErrPremium
			}

			oldCatalog, _ := catalogID(prior)
			currentAsset := elementAsset(p.Scene, e)
			oldAsset := elementAsset(before.Pages[pageID].Scene, prior)
			// Removing provenance from a surviving catalog occurrence cannot make its copies free.
			if oldCatalog != "" && currentCatalog != oldCatalog {
				return ErrPremium
			}

			premium := currentCatalog != "" || scope.PremiumAssets[currentAsset]

			existing := prior != nil && prior["isDeleted"] != true && currentCatalog == oldCatalog && currentAsset == oldAsset
			if premium && !existing && !allowed {
				return ErrPremium
			}
		}
	}

	return nil
}

func catalogID(e map[string]any) (string, bool) {
	c, _ := e["customData"].(map[string]any)

	raw, ok := c["handdrawShape"]
	if !ok {
		return "", true
	}

	m, ok := raw.(map[string]any)
	if !ok {
		return "", false
	}

	id, _ := m["id"].(string)
	_, known := CatalogDigest(id)
	// General shapes use a separate native representation and never carry a catalog logo ID.
	return id, known
}

func elementAsset(s Scene, e map[string]any) string {
	f, _ := e["fileId"].(string)
	return s.Files[f].AssetID
}
