package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/chai-rs/handdraw-server/app/transfer/model"
	asset "github.com/chai-rs/handdraw-server/internal/asset/model"
	document "github.com/chai-rs/handdraw-server/internal/document/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/segmentio/ksuid"
)

type planned struct {
	asset asset.Asset
	data  []byte
}

func derived(job, prefix, source string) (string, error) {
	if resourceid.Validate(job, model.IDPrefix) != nil {
		return "", model.ErrInvalid
	}

	seed, err := ksuid.Parse(strings.TrimPrefix(job, model.IDPrefix+"_"))
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256([]byte(prefix + ":" + job + ":" + source))

	id, err := ksuid.FromParts(seed.Time(), sum[:16])
	if err != nil {
		return "", err
	}

	return prefix + "_" + id.String(), nil
}

func metadata(p model.Payload, id string, a model.Attachment) asset.Asset {
	sum := sha256.Sum256(a.Data)
	return asset.Asset{ID: id, BoardID: p.BoardID, WorkspaceID: p.WorkspaceID, UploadedBy: p.Actor, Purpose: "attachment", Status: "pending", Size: int64(len(a.Data)), MIME: a.MIME, SHA256: hex.EncodeToString(sum[:])}
}

func scope(board string, assets []asset.Asset) document.Validation {
	s := document.Validation{BoardID: board, AllowedAssets: map[string]bool{}, PremiumAssets: map[string]bool{}, AssetDigests: map[string]string{}, AssetMIMEs: map[string]string{}}
	for _, a := range assets {
		s.AllowedAssets[a.ID] = true
		s.PremiumAssets[a.ID] = a.Premium
		s.AssetDigests[a.ID] = a.SHA256
		s.AssetMIMEs[a.ID] = a.MIME
	}

	return s
}

func (s *Service) decodeImport(p model.Payload, data []byte) (model.Archive, error) {
	if len(data) > int(asset.MaxImportBytes) {
		return model.Archive{}, model.ErrInvalid
	}

	var archive model.Archive

	if p.Format == "excalidraw" {
		return nativeArchive(p, data)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if decoder.Decode(&archive) != nil || archive.Format != "handdraw" || archive.Version != 1 || archive.Assets == nil || len(archive.Assets) > model.MaxAssets {
		return archive, model.ErrInvalid
	}
	// Validate original IDs and references before remapping; import never fetches source URLs.
	var originals []asset.Asset

	for id, a := range archive.Assets {
		m := metadata(p, id, a)

		m.Premium = document.IsPremiumDigest(m.SHA256)
		if m.Size > asset.MaxAttachmentBytes || !validMedia(m, a.Data) {
			return archive, model.ErrInvalid
		}

		originals = append(originals, m)
	}

	if err := archive.Snapshot.Validate(scope(archive.Snapshot.BoardID, originals)); err != nil {
		return archive, model.ErrInvalid
	}

	return archive, nil
}

func validMedia(a asset.Asset, data []byte) bool {
	if a.Size < 1 || a.Size > asset.MaxAttachmentBytes {
		return false
	}

	if a.MIME == "image/svg+xml" {
		return document.IsPremiumDigest(a.SHA256) && strings.Contains(string(data), "<svg")
	}

	return a.MIME == http.DetectContentType(data) && (a.MIME == "image/png" || a.MIME == "image/jpeg" || a.MIME == "image/webp" || a.MIME == "image/gif")
}

func (s *Service) importPlan(ctx context.Context, p model.Payload) ([]planned, []byte, error) {
	if p.Source == nil || p.Source.Status != "available" {
		return nil, nil, model.ErrInvalid
	}

	data, err := s.storage.Read(ctx, *p.Source)
	if err != nil {
		return nil, nil, err
	}

	archive, err := s.decodeImport(p, data)
	if err != nil {
		return nil, nil, err
	}

	remap := func(prefix, id string) string { v, _ := derived(p.ID, prefix, id); return v }
	target := document.Snapshot{SchemaVersion: 1, BoardID: p.BoardID, Pages: map[string]document.Page{}, PageOrder: []string{}, Notes: map[string]string{}, Folders: map[string]document.Folder{}, NoteFolders: map[string]string{}}
	plans := []planned{}
	assets := []asset.Asset{}

	for source, a := range archive.Assets {
		id := remap(asset.IDPrefix, source)
		m := metadata(p, id, a)
		m.Premium = document.IsPremiumDigest(m.SHA256)
		// A copied catalog asset keeps provenance by its pinned bytes, independent of client labels.
		for _, page := range archive.Snapshot.Pages {
			for _, element := range page.Scene.Elements {
				custom, _ := element["customData"].(map[string]any)
				logo, _ := custom["handdrawShape"].(map[string]any)

				catalog, _ := logo["id"].(string)
				if digest, ok := document.CatalogDigest(catalog); ok && (digest == m.SHA256 || (page.Scene.Files[elementFileID(element)].AssetID == source && m.MIME != "image/svg+xml")) {
					m.Premium = true
				}
			}
		}

		if !validMedia(m, a.Data) {
			return nil, nil, model.ErrInvalid
		}

		plans = append(plans, planned{asset: m, data: a.Data})
		assets = append(assets, m)
	}
	// SQL's prepared metadata is authoritative for hashes that originated in the catalog without labels.
	for i := range assets {
		for _, trusted := range p.Prepared {
			if assets[i].ID == trusted.ID {
				assets[i].Premium = trusted.Premium
				plans[i].asset.Premium = trusted.Premium
			}
		}
	}

	for _, old := range archive.Snapshot.PageOrder {
		page := archive.Snapshot.Pages[old]

		page.ID = remap(document.PageIDPrefix, old)
		for file, a := range page.Scene.Files {
			a.AssetID = remap(asset.IDPrefix, a.AssetID)
			page.Scene.Files[file] = a
		}

		target.Pages[page.ID] = page
		target.PageOrder = append(target.PageOrder, page.ID)
	}

	for id, body := range archive.Snapshot.Notes {
		target.Notes[remap(document.NoteIDPrefix, id)] = remapNote(body, func(id string) string { return remap(document.PageIDPrefix, id) })
	}

	for id, folder := range archive.Snapshot.Folders {
		folder.ID = remap(document.FolderIDPrefix, id)
		if folder.ParentID != nil {
			v := remap(document.FolderIDPrefix, *folder.ParentID)
			folder.ParentID = &v
		}

		target.Folders[folder.ID] = folder
	}

	for note, folder := range archive.Snapshot.NoteFolders {
		target.NoteFolders[remap(document.NoteIDPrefix, note)] = remap(document.FolderIDPrefix, folder)
	}

	validation := scope(p.BoardID, assets)
	if err = target.ValidatePremiumTransition(document.Snapshot{}, validation, p.Premium); err != nil {
		return nil, nil, model.ErrDenied
	}

	state, err := s.codec.Encode(target, validation)
	if err != nil {
		return nil, nil, model.ErrInvalid
	}

	return plans, state, nil
}

var (
	pageReference  = regexp.MustCompile(`pag_[0-9A-Za-z]{27}`)
	shapeReference = regexp.MustCompile(`<Shape\b[^>]*>`)
	pageAttribute  = regexp.MustCompile(`\bpage="(pag_[0-9A-Za-z]{27})"`)
	inlineCode     = regexp.MustCompile("`[^`]*`")
)

// Only structural page references change; labels, prose and code examples retain their source text.
func remapNote(body string, remap func(string) string) string {
	lines := strings.SplitAfter(body, "\n")
	frontmatter := len(lines) > 0 && strings.TrimSpace(lines[0]) == "---"
	field, fence := "", ""

	var output, prose strings.Builder

	flush := func() {
		text := prose.String()
		spans := inlineCode.FindAllStringIndex(text, -1)
		cursor := 0

		rewrite := func(v string) string {
			return shapeReference.ReplaceAllStringFunc(v, func(tag string) string {
				return pageAttribute.ReplaceAllStringFunc(tag, func(attr string) string { return `page="` + remap(pageAttribute.FindStringSubmatch(attr)[1]) + `"` })
			})
		}
		for _, span := range spans {
			output.WriteString(rewrite(text[cursor:span[0]]))
			output.WriteString(text[span[0]:span[1]])
			cursor = span[1]
		}

		output.WriteString(rewrite(text[cursor:]))
		prose.Reset()
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if frontmatter {
			if i > 0 && trimmed == "---" {
				frontmatter = false
			} else if i > 0 {
				if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && !strings.HasPrefix(line, "-") {
					field, _, _ = strings.Cut(line, ":")
				}

				if field == "pages" || field == "elements" {
					line = pageReference.ReplaceAllStringFunc(line, remap)
				}
			}

			output.WriteString(line)

			continue
		}

		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			flush()

			if fence == "" {
				fence = trimmed[:3]
			} else if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}

			output.WriteString(line)

			continue
		}

		if fence != "" {
			output.WriteString(line)
		} else {
			prose.WriteString(line)
		}
	}

	flush()

	return output.String()
}

func nativeArchive(p model.Payload, data []byte) (model.Archive, error) {
	var file struct {
		Type     string           `json:"type"`
		Version  int              `json:"version"`
		Source   string           `json:"source"`
		Elements []map[string]any `json:"elements"`
		AppState map[string]any   `json:"appState"`
		Files    map[string]struct {
			ID            string `json:"id"`
			MIME          string `json:"mimeType"`
			Data          string `json:"dataURL"`
			Created       int64  `json:"created"`
			LastRetrieved int64  `json:"lastRetrieved"`
		} `json:"files"`
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if decoder.Decode(&file) != nil || file.Type != "excalidraw" || file.Version != 2 || len(file.Files) > model.MaxAssets {
		return model.Archive{}, model.ErrInvalid
	}

	pageID, _ := derived(p.ID, document.PageIDPrefix, "native-source")
	boardID := p.BoardID

	page := document.Page{ID: pageID, Name: "Imported page", Scene: document.Scene{Elements: map[string]map[string]any{}, ElementOrder: []string{}, AppState: map[string]any{}, Files: map[string]document.Asset{}}}
	for _, e := range file.Elements {
		id, _ := e["id"].(string)
		if id == "" || page.Scene.Elements[id] != nil {
			return model.Archive{}, model.ErrInvalid
		}

		page.Scene.Elements[id] = e
		page.Scene.ElementOrder = append(page.Scene.ElementOrder, id)
	}

	for _, key := range []string{"viewBackgroundColor", "canvasBackground", "gridModeEnabled", "gridSize", "gridStep"} {
		if v, ok := file.AppState[key]; ok {
			page.Scene.AppState[key] = v
		}
	}

	archive := model.Archive{Format: "handdraw", Version: 1, Assets: map[string]model.Attachment{}, Snapshot: document.Snapshot{SchemaVersion: 1, BoardID: boardID, Pages: map[string]document.Page{}, PageOrder: []string{pageID}, Notes: map[string]string{}, Folders: map[string]document.Folder{}, NoteFolders: map[string]string{}}}

	for id, f := range file.Files {
		prefix := "data:" + f.MIME + ";base64,"
		if !strings.HasPrefix(f.Data, prefix) {
			return archive, model.ErrInvalid
		}

		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(f.Data, prefix))
		if err != nil {
			return archive, model.ErrInvalid
		}

		assetID, _ := derived(p.ID, asset.IDPrefix, "native:"+id)
		archive.Assets[assetID] = model.Attachment{MIME: f.MIME, Data: raw}
		page.Scene.Files[id] = document.Asset{AssetID: assetID, MIMEType: f.MIME}
	}

	archive.Snapshot.Pages[pageID] = page

	return archive, nil
}

func (s *Service) exportPlan(ctx context.Context, p model.Payload) ([]planned, error) {
	snapshot, err := s.codec.Decode(p.State, scope(p.BoardID, p.Assets))
	if err != nil {
		return nil, model.ErrInvalid
	}

	archive := model.Archive{Format: "handdraw", Version: 1, Snapshot: snapshot, Assets: map[string]model.Attachment{}}
	referenced := map[string]bool{}

	for _, page := range snapshot.Pages {
		for _, file := range page.Scene.Files {
			referenced[file.AssetID] = true
		}
	}

	if len(referenced) > model.MaxAssets {
		return nil, model.ErrInvalid
	}

	var decodedBytes int64

	for _, a := range p.Assets {
		if !referenced[a.ID] {
			continue
		}

		if !p.IncludeAssets {
			continue
		}

		decodedBytes += a.Size
		if decodedBytes > asset.MaxImportBytes*3/4 {
			return nil, model.ErrInvalid
		}

		data, err := s.storage.Read(ctx, a)
		if err != nil {
			return nil, err
		}

		archive.Assets[a.ID] = model.Attachment{MIME: a.MIME, Data: data}
	}

	var output any = archive

	if p.Format == "excalidraw" {
		page, ok := snapshot.Pages[p.PageID]
		if !ok {
			return nil, model.ErrInvalid
		}

		elements := []map[string]any{}
		for _, id := range page.Scene.ElementOrder {
			elements = append(elements, page.Scene.Elements[id])
		}

		files := map[string]any{}

		for id, asset := range page.Scene.Files {
			if !p.IncludeAssets {
				continue
			}

			a, ok := archive.Assets[asset.AssetID]
			if !ok {
				return nil, model.ErrInvalid
			}

			files[id] = map[string]any{"id": id, "mimeType": a.MIME, "dataURL": "data:" + a.MIME + ";base64," + base64.StdEncoding.EncodeToString(a.Data), "created": p.CreatedAt.UnixMilli()}
		}

		output = map[string]any{"type": "excalidraw", "version": 2, "source": "handdraw", "elements": elements, "appState": page.Scene.AppState, "files": files}
	}

	data, err := json.Marshal(output)
	if err != nil || len(data) > int(asset.MaxImportBytes) {
		return nil, model.ErrInvalid
	}

	id, err := derived(p.ID, asset.IDPrefix, "export")
	if err != nil {
		return nil, err
	}

	a := metadata(p, id, model.Attachment{MIME: "application/json", Data: data})
	a.Purpose = "export_artifact"

	return []planned{{asset: a, data: data}}, nil
}

func elementFileID(element map[string]any) string { id, _ := element["fileId"].(string); return id }
