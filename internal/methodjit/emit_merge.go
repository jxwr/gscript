//go:build darwin && arm64

// emit_merge.go: helpers for merging per-predecessor verification state
// at multi-predecessor block boundaries. Extracted from emit_compile.go
// to keep the file under the 800-line guard (CLAUDE.md rule 13).

package methodjit

// mergeBoolVerified returns the intersection of per-predecessor bool maps:
// a key is in the result if and only if every predecessor has the same
// value for that key. If any predecessor hasn't been compiled yet (e.g.,
// back-edge at a loop header; handled separately by isHeader check), the
// merge result is empty.
func mergeBoolVerified(perBlock map[int]map[int]bool, preds []*Block) map[int]bool {
	if len(preds) == 0 {
		return make(map[int]bool)
	}
	first, ok := perBlock[preds[0].ID]
	if !ok {
		return make(map[int]bool)
	}
	out := make(map[int]bool, len(first))
	for k, v := range first {
		out[k] = v
	}
	for i := 1; i < len(preds); i++ {
		m, ok := perBlock[preds[i].ID]
		if !ok {
			return make(map[int]bool)
		}
		for k, v := range out {
			if mv, inM := m[k]; !inM || mv != v {
				delete(out, k)
			}
		}
	}
	return out
}

func mergeKindVerified(perBlock map[int]map[int]uint16, preds []*Block) map[int]uint16 {
	if len(preds) == 0 {
		return make(map[int]uint16)
	}
	first, ok := perBlock[preds[0].ID]
	if !ok {
		return make(map[int]uint16)
	}
	out := make(map[int]uint16, len(first))
	for k, v := range first {
		out[k] = v
	}
	for i := 1; i < len(preds); i++ {
		m, ok := perBlock[preds[i].ID]
		if !ok {
			return make(map[int]uint16)
		}
		for k, v := range out {
			if mv, inM := m[k]; !inM || mv != v {
				delete(out, k)
			}
		}
	}
	return out
}

func mergeShapeVerified(perBlock map[int]map[int]uint32, preds []*Block) map[int]uint32 {
	if len(preds) == 0 {
		return make(map[int]uint32)
	}
	first, ok := perBlock[preds[0].ID]
	if !ok {
		return make(map[int]uint32)
	}
	out := make(map[int]uint32, len(first))
	for k, v := range first {
		out[k] = v
	}
	for i := 1; i < len(preds); i++ {
		m, ok := perBlock[preds[i].ID]
		if !ok {
			return make(map[int]uint32)
		}
		for k, v := range out {
			if mv, inM := m[k]; !inM || mv != v {
				delete(out, k)
			}
		}
	}
	return out
}
