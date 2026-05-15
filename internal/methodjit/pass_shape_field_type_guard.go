package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
)

// ShapeFieldTypeGuardPass turns process-wide stable shape-field type feedback
// into one epoch guard per compiled function. After the guard, fixed-shape
// FieldLoad sites can skip their per-load tag check.
func ShapeFieldTypeGuardPass(fn *Function) (*Function, error) {
	if fn == nil || len(fn.Blocks) == 0 {
		return fn, nil
	}
	seedShapeFieldTypesFromFixedConstructors(fn)
	type key struct {
		shapeID uint32
		field   int
		typ     Type
	}
	guards := make(map[key]bool)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpFieldLoad || instr.Type != TypeFloat || len(instr.Args) == 0 || instr.Args[0] == nil {
				continue
			}
			svals := instr.Args[0].Def
			if svals == nil || svals.Op != OpFieldSvals || svals.Aux <= 0 {
				continue
			}
			fieldIdx := int(instr.Aux)
			if fieldIdx < 0 {
				continue
			}
			shapeID := uint32(svals.Aux)
			wantRuntime, ok := irTypeToRuntimeValueType(instr.Type)
			if !ok {
				continue
			}
			got, stable := runtime.ShapeFieldStableType(shapeID, fieldIdx)
			if !stable || got != wantRuntime || runtime.ShapeFieldTypeEpochPtr(shapeID, fieldIdx) == nil {
				reason := "unknown-or-mixed"
				if stable {
					reason = fmt.Sprintf("stable %v != wanted %v", got, wantRuntime)
				}
				functionRemarks(fn).Add("ShapeFieldTypeGuard", "missed", block.ID, instr.ID, instr.Op,
					fmt.Sprintf("shape %d field %d cannot elide type check: %s", shapeID, fieldIdx, reason))
				continue
			}
			k := key{shapeID: shapeID, field: fieldIdx, typ: instr.Type}
			guards[k] = true
			if fn.ShapeFieldTypeElidedLoads == nil {
				fn.ShapeFieldTypeElidedLoads = make(map[int]bool)
			}
			fn.ShapeFieldTypeElidedLoads[instr.ID] = true
			functionRemarks(fn).Add("ShapeFieldTypeGuard", "changed", block.ID, instr.ID, instr.Op,
				fmt.Sprintf("elide per-load type check for shape %d field %d as %s", shapeID, fieldIdx, instr.Type))
		}
	}
	if len(guards) == 0 {
		return fn, nil
	}
	entry := fn.Entry
	if entry == nil {
		entry = fn.Blocks[0]
	}
	for k := range guards {
		guard := &Instr{
			ID:    fn.newValueID(),
			Op:    OpGuardShapeFieldType,
			Type:  TypeBool,
			Aux:   int64(k.shapeID)<<32 | int64(uint32(k.field)),
			Aux2:  int64(k.typ),
			Block: entry,
		}
		insertBeforeTerminator(entry, guard)
		functionRemarks(fn).Add("ShapeFieldTypeGuard", "changed", entry.ID, guard.ID, guard.Op,
			fmt.Sprintf("guard shape %d field %d stable type %s", k.shapeID, k.field, k.typ))
	}
	return fn, nil
}

func seedShapeFieldTypesFromFixedConstructors(fn *Function) {
	if fn == nil || fn.Proto == nil {
		return
	}
	for _, block := range fn.Blocks {
		globalTypes := localSetGlobalTypes(block)
		for _, instr := range block.Instrs {
			if instr == nil || instr.Op != OpNewFixedTable {
				continue
			}
			shapeID, fieldCount, ok := fixedConstructorShape(fn, instr)
			if !ok || shapeID == 0 || fieldCount <= 0 || len(instr.Args) < fieldCount {
				continue
			}
			for fieldIdx := 0; fieldIdx < fieldCount; fieldIdx++ {
				arg := instr.Args[fieldIdx]
				if arg == nil || arg.Def == nil {
					continue
				}
				typ := inferFixedCtorArgType(arg.Def, globalTypes, make(map[int]bool))
				runtimeType, ok := irTypeToRuntimeValueType(typ)
				if !ok || runtimeType == runtime.TypeNil {
					continue
				}
				before, stableBefore := runtime.ShapeFieldStableType(shapeID, fieldIdx)
				runtime.ObserveShapeFieldValueType(shapeID, fieldIdx, runtimeType)
				after, stableAfter := runtime.ShapeFieldStableType(shapeID, fieldIdx)
				if !stableBefore || before != after {
					state := "mixed"
					if stableAfter {
						state = fmt.Sprintf("%v", after)
					}
					functionRemarks(fn).Add("ShapeFieldTypeGuard", "changed", block.ID, instr.ID, instr.Op,
						fmt.Sprintf("seeded shape %d field %d type from fixed constructor as %s", shapeID, fieldIdx, state))
				}
			}
		}
	}
}

func localSetGlobalTypes(block *Block) map[int64]Type {
	out := make(map[int64]Type)
	if block == nil {
		return out
	}
	for _, instr := range block.Instrs {
		if instr == nil {
			continue
		}
		if instr.Op == OpSetGlobal && len(instr.Args) > 0 && instr.Args[0] != nil && instr.Args[0].Def != nil {
			out[instr.Aux] = inferFixedCtorArgType(instr.Args[0].Def, out, make(map[int]bool))
		}
	}
	return out
}

func inferFixedCtorArgType(instr *Instr, globalTypes map[int64]Type, seen map[int]bool) Type {
	if instr == nil {
		return TypeUnknown
	}
	if instr.Type != TypeUnknown && instr.Type != TypeAny {
		return instr.Type
	}
	if seen[instr.ID] {
		return TypeUnknown
	}
	seen[instr.ID] = true
	switch instr.Op {
	case OpConstInt:
		return TypeInt
	case OpConstFloat:
		return TypeFloat
	case OpConstString:
		return TypeString
	case OpConstBool:
		return TypeBool
	case OpGetGlobal:
		if typ, ok := globalTypes[instr.Aux]; ok {
			return typ
		}
	case OpAdd, OpSub, OpMul:
		if len(instr.Args) < 2 || instr.Args[0] == nil || instr.Args[1] == nil {
			return TypeUnknown
		}
		lt := inferFixedCtorArgType(instr.Args[0].Def, globalTypes, seen)
		rt := inferFixedCtorArgType(instr.Args[1].Def, globalTypes, seen)
		if lt == TypeFloat || rt == TypeFloat {
			if (lt == TypeFloat || lt == TypeInt) && (rt == TypeFloat || rt == TypeInt) {
				return TypeFloat
			}
		}
		if lt == TypeInt && rt == TypeInt {
			return TypeInt
		}
	case OpDiv:
		if len(instr.Args) < 2 || instr.Args[0] == nil || instr.Args[1] == nil {
			return TypeUnknown
		}
		lt := inferFixedCtorArgType(instr.Args[0].Def, globalTypes, seen)
		rt := inferFixedCtorArgType(instr.Args[1].Def, globalTypes, seen)
		if (lt == TypeFloat || lt == TypeInt) && (rt == TypeFloat || rt == TypeInt) {
			return TypeFloat
		}
	case OpUnm:
		if len(instr.Args) == 0 || instr.Args[0] == nil {
			return TypeUnknown
		}
		at := inferFixedCtorArgType(instr.Args[0].Def, globalTypes, seen)
		if at == TypeInt || at == TypeFloat {
			return at
		}
	}
	return TypeUnknown
}

func fixedConstructorShape(fn *Function, instr *Instr) (uint32, int, bool) {
	if fn == nil || fn.Proto == nil || instr == nil || instr.Op != OpNewFixedTable {
		return 0, 0, false
	}
	fieldCount := int(instr.Aux2)
	if fieldCount <= 0 {
		return 0, 0, false
	}
	if fieldCount == 2 {
		ctorIdx := int(instr.Aux)
		if ctorIdx < 0 || ctorIdx >= len(fn.Proto.TableCtors2) {
			return 0, 0, false
		}
		ctor := &fn.Proto.TableCtors2[ctorIdx].Runtime
		if ctor.Shape == nil {
			return 0, 0, false
		}
		return ctor.Shape.ID, 2, true
	}
	ctorIdx := int(instr.Aux)
	if ctorIdx < 0 || ctorIdx >= len(fn.Proto.TableCtorsN) {
		return 0, 0, false
	}
	ctor := &fn.Proto.TableCtorsN[ctorIdx].Runtime
	if ctor.Shape == nil || len(ctor.Keys) != fieldCount {
		return 0, 0, false
	}
	return ctor.Shape.ID, fieldCount, true
}
