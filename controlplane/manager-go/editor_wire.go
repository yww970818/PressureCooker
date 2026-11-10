package main

// Large source strings are transported once and restored by the editor before
// editing. This is opt-in for editor responses; persistence and normal APIs stay full.
func editorReadPayload(value Obj) Obj {
	texts, slots := []any{}, []any{}
	indices := map[string]int{}
	var visit func(any, []any) any
	visit = func(value any, path []any) any {
		// Deep custom JSON stays verbatim instead of expanding a path per leaf.
		if len(path) > 64 {
			return value
		}
		switch v := value.(type) {
		case string:
			if len(v) < 1024 {
				return v
			}
			index, exists := indices[v]
			if !exists {
				index = len(texts)
				indices[v] = index
				texts = append(texts, v)
			}
			slots = append(slots, []any{append([]any(nil), path...), index})
			return nil
		case map[string]any:
			out := make(Obj, len(v))
			for key, item := range v {
				out[key] = visit(item, append(path, key))
			}
			return out
		case []any:
			out := make([]any, len(v))
			for index, item := range v {
				out[index] = visit(item, append(path, index))
			}
			return out
		default:
			return value
		}
	}
	data := visit(value, nil)
	if len(texts) == 0 {
		return value
	}
	return Obj{"format": "editor-text-v1", "data": data, "texts": texts, "slots": slots}
}
