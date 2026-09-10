// Package service builds server-owned initial content without requiring a client bootstrap write.
package service

import (
	"github.com/chai-rs/handdraw-server/internal/document/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// InitialDocument is the complete schema-validated result for atomic board creation.
type InitialDocument struct {
	State         []byte `json:"-"`
	PageID        string `json:"page_id"`
	SchemaVersion int    `json:"schema_version"`
}

// InitialBuilder creates empty or starter C4 content through the document codec port.
type InitialBuilder struct{ codec model.Codec }

// NewInitialBuilder receives its encoding adapter from application wiring.
func NewInitialBuilder(codec model.Codec) *InitialBuilder { return &InitialBuilder{codec: codec} }

// Build creates a fresh pag_ ID, never deriving it from a brd_ or legacy UUID.
func (b *InitialBuilder) Build(boardID, mode string) (InitialDocument, error) {
	if resourceid.Validate(boardID, model.BoardIDPrefix) != nil || (mode != "empty" && mode != "get_started") || b.codec == nil {
		return InitialDocument{}, model.ErrInvalidDocument
	}

	pageID, err := resourceid.New(model.PageIDPrefix)
	if err != nil {
		return InitialDocument{}, err
	}

	scene := model.Scene{Elements: map[string]map[string]any{}, ElementOrder: []string{}, AppState: map[string]any{"viewBackgroundColor": "#ffffff"}, Files: map[string]model.Asset{}}
	snapshot := model.Snapshot{SchemaVersion: model.SchemaVersion, BoardID: boardID, Pages: map[string]model.Page{}, PageOrder: []string{pageID}, Notes: map[string]string{}, Folders: map[string]model.Folder{}, NoteFolders: map[string]string{}}

	if mode == "get_started" {
		for i, label := range []string{"Developer", "Allocation service", "Position sizing"} {
			shapeID := "c4-" + string(rune('a'+i))
			textID := shapeID + "-label"
			scene.Elements[shapeID] = map[string]any{"id": shapeID, "type": "rectangle", "x": float64(i * 300), "y": float64(100), "width": float64(220), "height": float64(120), "angle": float64(0), "version": float64(1), "versionNonce": float64(1), "isDeleted": false, "customData": map[string]any{"handdrawName": label}}
			scene.Elements[textID] = map[string]any{"id": textID, "type": "text", "x": float64(i*300 + 10), "y": float64(140), "width": float64(200), "height": float64(24), "angle": float64(0), "version": float64(1), "versionNonce": float64(1), "isDeleted": false, "text": label, "fontSize": float64(20), "fontFamily": float64(1)}
			scene.ElementOrder = append(scene.ElementOrder, shapeID, textID)
		}

		for i := 0; i < 2; i++ {
			id := "c4-flow-" + string(rune('a'+i))
			scene.Elements[id] = map[string]any{"id": id, "type": "arrow", "x": float64(i*300 + 220), "y": float64(160), "width": float64(80), "height": float64(0), "angle": float64(0), "version": float64(1), "versionNonce": float64(1), "isDeleted": false, "points": []any{[]any{float64(0), float64(0)}, []any{float64(80), float64(0)}}, "startBinding": nil, "endBinding": nil, "startArrowhead": nil, "endArrowhead": "arrow", "elbowed": false, "lastCommittedPoint": nil}
			scene.ElementOrder = append(scene.ElementOrder, id)
		}

		for _, element := range scene.Elements {
			for key, value := range map[string]any{"strokeColor": "#1e1e1e", "backgroundColor": "transparent", "fillStyle": "solid", "strokeWidth": float64(1), "strokeStyle": "solid", "roughness": float64(1), "opacity": float64(100), "groupIds": []any{}, "frameId": nil, "roundness": nil, "seed": float64(1), "boundElements": nil, "updated": float64(0), "link": nil, "locked": false} {
				element[key] = value
			}

			if element["type"] == "text" {
				element["originalText"] = element["text"]
				element["textAlign"] = "left"
				element["verticalAlign"] = "top"
				element["containerId"] = nil
				element["autoResize"] = true
				element["lineHeight"] = float64(1.25)
			}
		}

		noteID, err := resourceid.New(model.NoteIDPrefix)
		if err != nil {
			return InitialDocument{}, err
		}

		snapshot.Notes[noteID] = "# Get started\n\nDescribe the system context, then drill into containers and components.\n\n<Shape page=\"" + pageID + "\" id=\"c4-b\" label=\"Allocation service\" />\n\nAllocation becomes input to Position sizing."
	}

	snapshot.Pages[pageID] = model.Page{ID: pageID, Name: "Page 1", Scene: scene}

	state, err := b.codec.Encode(snapshot, model.Validation{BoardID: boardID})
	if err != nil {
		return InitialDocument{}, err
	}

	return InitialDocument{State: state, PageID: pageID, SchemaVersion: model.SchemaVersion}, nil
}
