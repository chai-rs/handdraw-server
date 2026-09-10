package ygo

import "github.com/reearth/ygo/crdt"

// A matching JSON projection is insufficient: editable bindings require the correct shared types.
func validStructure(root *crdt.YMap) bool {
	if len(root.Keys()) != 7 {
		return false
	}

	pages, ok := sharedMap(root, "pages")
	if !ok {
		return false
	}

	if !sharedArray(root, "pageOrder") {
		return false
	}

	notes, ok := sharedMap(root, "notes")
	if !ok {
		return false
	}

	for _, id := range notes.Keys() {
		value, _ := notes.Get(id)
		if _, ok := value.(*crdt.YText); !ok {
			return false
		}
	}

	for _, name := range []string{"folders", "noteFolders"} {
		if _, ok := sharedMap(root, name); !ok {
			return false
		}
	}

	for _, id := range pages.Keys() {
		page, ok := sharedMap(pages, id)
		if !ok {
			return false
		}

		scene, ok := sharedMap(page, "scene")
		if !ok || !sharedArray(scene, "elementOrder") {
			return false
		}

		for _, name := range []string{"elements", "appState", "files"} {
			if _, ok := sharedMap(scene, name); !ok {
				return false
			}
		}
	}

	return true
}

func sharedMap(parent *crdt.YMap, key string) (*crdt.YMap, bool) {
	value, _ := parent.Get(key)
	m, ok := value.(*crdt.YMap)

	return m, ok
}

func sharedArray(parent *crdt.YMap, key string) bool {
	value, _ := parent.Get(key)
	_, ok := value.(*crdt.YArray)

	return ok
}
