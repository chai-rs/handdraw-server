// Package ygo encodes and validates isolated Yjs V1 candidates without mutating committed state.
package ygo

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/chai-rs/handdraw-server/internal/document/model"
	"github.com/reearth/ygo/crdt"
)

// Codec stages updates and preserves raw merged updates, including deletion history.
type Codec struct{}

var _ model.Codec = Codec{}

// Decode validates an entire durable state before exposing a semantic snapshot.
func (Codec) Decode(state []byte, scope model.Validation) (snapshot model.Snapshot, err error) {
	defer func() {
		if recover() != nil {
			snapshot = model.Snapshot{}
			err = model.ErrInvalidDocument
		}
	}()

	if len(state) == 0 || len(state) > model.MaxDocumentBytes {
		return snapshot, model.ErrInvalidDocument
	}

	if err = admission(state); err != nil {
		return snapshot, model.ErrInvalidDocument
	}

	doc := crdt.New(crdt.WithMaxPendingItems(1024))
	defer doc.Destroy()

	if err = crdt.ApplyUpdateV1(doc, state, nil); err != nil {
		return snapshot, model.ErrInvalidDocument
	}

	pending := doc.PendingStats()
	if pending.Items > 0 || pending.DeleteRanges > 0 {
		return snapshot, model.ErrPendingDependencies
	}

	if !validStructure(doc.GetMap("handdraw")) {
		return snapshot, model.ErrInvalidDocument
	}

	raw, err := json.Marshal(jsonNumbers(doc.GetMap("handdraw").Entries()))
	if err != nil {
		return snapshot, model.ErrInvalidDocument
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	if err = decoder.Decode(&snapshot); err != nil {
		return model.Snapshot{}, model.ErrInvalidDocument
	}

	if err = decoder.Decode(new(any)); err != io.EOF {
		return model.Snapshot{}, model.ErrInvalidDocument
	}

	if err = snapshot.Validate(scope); err != nil {
		return model.Snapshot{}, err
	}

	return snapshot, nil
}

// Apply validates a candidate and returns durable bytes only when every dependency and schema check succeeds.
func (c Codec) Apply(committed, update []byte, scope model.Validation) (state []byte, err error) {
	defer func() {
		if recover() != nil {
			state = nil
			err = model.ErrInvalidDocument
		}
	}()

	if len(update) == 0 || len(update) > model.MaxUpdateBytes {
		return nil, model.ErrInvalidDocument
	}

	if _, err = c.Decode(committed, scope); err != nil {
		return nil, err
	}

	if err = admission(update); err != nil {
		return nil, model.ErrInvalidDocument
	}

	state, err = crdt.MergeUpdatesV1(committed, update)
	if err != nil {
		return nil, model.ErrInvalidDocument
	}

	if _, err = c.Decode(state, scope); err != nil {
		return nil, err
	}

	return state, nil
}

// Encode builds nested maps for pages/elements and Y.Text notes; native element payloads stay lossless JSON.
func (c Codec) Encode(snapshot model.Snapshot, scope model.Validation) (state []byte, err error) {
	if err = snapshot.Validate(scope); err != nil {
		return nil, err
	}

	defer func() {
		if recover() != nil {
			state = nil
			err = model.ErrInvalidDocument
		}
	}()

	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}

	var value map[string]any
	if err = json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}

	doc := crdt.New()
	defer doc.Destroy()

	root := doc.GetMap("handdraw")
	doc.Transact(func(tx *crdt.Transaction) {
		for key, v := range value {
			root.Set(tx, key, nested(key, v))
		}
	})
	state = crdt.EncodeStateAsUpdateV1(doc, nil)
	_, err = c.Decode(state, scope)

	return state, err
}

func nested(key string, v any) any {
	if key == "notes" {
		m := crdt.NewMapPrelim()

		for id, body := range v.(map[string]any) {
			text := crdt.NewTextPrelim()
			text.Insert(nil, 0, body.(string), nil)
			m.Set(nil, id, text)
		}

		return m
	}

	if key == "elements" || key == "files" || key == "folders" {
		m := crdt.NewMapPrelim()
		for id, item := range v.(map[string]any) {
			m.Set(nil, id, item)
		}

		return m
	}

	switch value := v.(type) {
	case map[string]any:
		m := crdt.NewMapPrelim()
		for name, item := range value {
			m.Set(nil, name, nested(name, item))
		}

		return m
	case []any:
		a := crdt.NewArrayPrelim()
		a.Push(nil, value)

		return a
	default:
		return v
	}
}

// Yjs numbers are doubles even when the wire uses float32. Widen before JSON
// formatting so Go does not emit a shorter float32 decimal that changes the JS value.
func jsonNumbers(value any) any {
	switch v := value.(type) {
	case float32:
		return float64(v)
	case map[string]any:
		for key, item := range v {
			v[key] = jsonNumbers(item)
		}

		return v
	case []any:
		for i, item := range v {
			v[i] = jsonNumbers(item)
		}

		return v
	default:
		return value
	}
}
