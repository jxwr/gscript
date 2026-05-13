package methodjit

import "github.com/gscript/gscript/internal/runtime"

const (
	tier2NewTableKindShift      = 32
	tier2NewTableDenseMixedFlag = int64(1) << 40
	tier2NewTableHashMask       = (1 << tier2NewTableKindShift) - 1
)

func packNewTableAux2(hashHint int64, kind runtime.ArrayKind) int64 {
	return (int64(kind) << tier2NewTableKindShift) | (hashHint & tier2NewTableHashMask)
}

func packNewTableAux2DenseMixed(hashHint int64) int64 {
	return packNewTableAux2(hashHint, runtime.ArrayMixed) | tier2NewTableDenseMixedFlag
}

func unpackNewTableDenseMixed(aux2 int64) bool {
	_, kind := unpackNewTableAux2(aux2)
	return kind == runtime.ArrayMixed && aux2&tier2NewTableDenseMixedFlag != 0
}

func unpackNewTableAux2(aux2 int64) (hashHint int, kind runtime.ArrayKind) {
	hashHint = int(aux2 & tier2NewTableHashMask)
	kind = runtime.ArrayKind((aux2 >> tier2NewTableKindShift) & 0xff)
	switch kind {
	case runtime.ArrayInt, runtime.ArrayFloat, runtime.ArrayBool:
		return hashHint, kind
	default:
		return hashHint, runtime.ArrayMixed
	}
}
