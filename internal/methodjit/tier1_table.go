//go:build darwin && arm64

// tier1_table.go emits ARM64 templates for baseline table, field, global,
// length, and self operations. These are the highest-value native ops
// after arithmetic and control flow.
//
// Strategy:
//   - GETFIELD/SETFIELD: shape-guarded inline cache. If FieldCache[pc] has
//     a valid shapeID, do direct svals[fieldIdx] access (~10 insns).
//     Otherwise fall back to exit-resume.
//   - GETTABLE/SETTABLE: integer-key fast path with bounds check on the
//     table's array part. Non-integer keys fall back to exit-resume.
//   - LEN: for tables, load array length directly. Falls back for strings
//     and metatables.
//   - SELF: R(A+1) = R(B); R(A) = R(B)[RK(C)] using GETTABLE logic.

package methodjit

import (
	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// emitBaselineGetGlobal emits native ARM64 for OP_GETGLOBAL: R(A) = globals[K(Bx)]
// Uses a per-PC value cache stored in BaselineFunc.GlobalValCache with a
// generation-based invalidation scheme. The cache is populated by the Go slow
// path (handleGetGlobal) on first miss. SetGlobal increments the generation
// counter, causing all caches to miss on next access.
//
// Fast path (~8 instructions):
//  1. Version check: engine.globalCacheGen == bf.CachedGlobalGen
//  2. Load GlobalCache pointer from ExecContext
//  3. Load cached value at GlobalValCache[pc]
//  4. If non-zero (cached), store to R(A) and continue
//
// Slow path: standard exit-resume to handleGetGlobal in Go.
func emitBaselineGetGlobal(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	bx := vm.DecodeBx(inst)

	slowLabel := nextLabel("getglobal_slow")
	doneLabel := nextLabel("getglobal_done")

	// Version check: engine.globalCacheGen == ctx.BaselineGlobalCachedGen
	asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineGlobalGenPtr)
	asm.CBZ(jit.X0, slowLabel)                                  // no gen pointer = no cache
	asm.LDR(jit.X1, jit.X0, 0)                                  // X1 = current gen
	asm.LDR(jit.X2, mRegCtx, execCtxOffBaselineGlobalCachedGen) // X2 = cached gen
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondNE, slowLabel) // generation mismatch -> cache invalid

	// Load GlobalCache pointer from ExecContext.
	asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineGlobalCache)
	asm.CBZ(jit.X0, slowLabel) // no cache allocated

	// Load cached value at GlobalValCache[pc].
	cacheOff := pc * 8 // each entry is 8 bytes (uint64)
	if cacheOff < 4096 {
		asm.LDR(jit.X1, jit.X0, cacheOff)
	} else {
		asm.LoadImm64(jit.X1, int64(cacheOff))
		asm.ADDreg(jit.X0, jit.X0, jit.X1)
		asm.LDR(jit.X1, jit.X0, 0)
	}

	// If zero (cache miss), go to slow path.
	asm.CBZ(jit.X1, slowLabel)

	// Cache hit: store to R(A).
	storeSlot(asm, a, jit.X1)
	asm.B(doneLabel)

	// Slow path: exit-resume.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_GETGLOBAL, pc, a, bx, 0)

	asm.Label(doneLabel)
}

// emitBaselineGetField emits native ARM64 for OP_GETFIELD: R(A) = R(B).field[C]
// Uses runtime inline cache from proto.FieldCache[pc].
// Falls back to exit-resume if cache miss or non-table.
func emitBaselineGetField(asm *jit.Assembler, inst uint32, pc int, feedbackEnabled bool) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst) // constant index for field name

	slowLabel := nextLabel("getfield_slow")
	doneLabel := nextLabel("getfield_done")
	emptyMissLabel := nextLabel("getfield_empty_miss")
	polyMissLabel := nextLabel("getfield_poly_miss")

	// Load FieldCache pointer from ExecContext.
	asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineFieldCache)
	asm.CBZ(jit.X0, slowLabel) // no field cache allocated yet

	// Compute &FieldCache[pc]: X0 + pc * FieldCacheEntrySize
	if pc > 0 {
		entryOff := pc * jit.FieldCacheEntrySize
		if entryOff < 4096 {
			asm.ADDimm(jit.X0, jit.X0, uint16(entryOff))
		} else {
			asm.LoadImm64(jit.X1, int64(entryOff))
			asm.ADDreg(jit.X0, jit.X0, jit.X1)
		}
	}
	// X0 = &FieldCache[pc]

	// Load entry.ShapeID (uint32 at offset 8). Use LDRW for 32-bit.
	asm.LDRW(jit.X2, jit.X0, jit.FieldCacheEntryOffShapeID) // X2 = cached shapeID
	asm.CBZ(jit.X2, slowLabel)                              // shapeID==0 means not cached

	// Load entry.FieldIdx (int at offset 0).
	asm.LDR(jit.X3, jit.X0, jit.FieldCacheEntryOffFieldIdx) // X3 = fieldIdx

	// Load table value from R(B).
	loadSlot(asm, jit.X0, b)

	// Check it's a table pointer (tag = 0xFFFF, sub = 0).
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X4, slowLabel)

	// Extract raw *Table pointer (44-bit payload).
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, slowLabel)

	// Shape guard: table.shapeID must match cached shapeID.
	asm.LDRW(jit.X1, jit.X0, jit.TableOffShapeID) // X1 = table.shapeID
	asm.CMPreg(jit.X1, jit.X2)                    // compare with cached shapeID
	asm.BCond(jit.CondNE, polyMissLabel)

	// Bounds check: fieldIdx < len(svals)
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvalsLen) // X1 = svals.len
	asm.CMPreg(jit.X3, jit.X1)                    // fieldIdx < svals.len?
	asm.BCond(jit.CondGE, slowLabel)              // unsigned >= means out of bounds

	// Direct field access: svals[fieldIdx]
	// LDRreg uses [Xn + Xm, LSL #3] which already scales by 8 (= ValueSize),
	// so X3 must hold the raw fieldIdx (not pre-multiplied).
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvals) // X1 = svals data pointer
	asm.LDRreg(jit.X0, jit.X1, jit.X3)         // X0 = svals[fieldIdx]

	// Keep type feedback current on the field-cache fast path. The slow path
	// updates feedback in Go; without this, a site can stay stuck on the first
	// cached value's type even after later cache hits observe a different type.
	if feedbackEnabled {
		emitBaselineFeedbackResultFromValue(asm, pc, jit.X0, "getfield")
	}

	// Store result to R(A).
	storeSlot(asm, a, jit.X0)
	asm.B(doneLabel)

	// Polymorphic static-field cache for object dispatch sites that rotate
	// among a small number of stable table shapes.
	asm.Label(polyMissLabel)
	asm.CBZ(jit.X1, emptyMissLabel)
	emitBaselineFieldPolyLookup(asm, pc, a, jit.X0, jit.X1, feedbackEnabled, "getfield_poly", slowLabel, doneLabel)

	// Empty shape-less tables cannot contain any string field. This catches
	// leaf objects built from nil fields without bouncing through Go.
	asm.Label(emptyMissLabel)
	asm.CBNZ(jit.X1, slowLabel) // non-empty shape mismatch
	asm.LDR(jit.X4, jit.X0, jit.TableOffMetatable)
	asm.CBNZ(jit.X4, slowLabel) // __index may synthesize a missing field
	asm.LDR(jit.X4, jit.X0, jit.TableOffSvalsLen)
	asm.CBNZ(jit.X4, slowLabel) // shape-less but not empty
	asm.LDR(jit.X4, jit.X0, jit.TableOffSmap)
	asm.CBNZ(jit.X4, slowLabel) // large string-key table
	asm.LDR(jit.X4, jit.X0, jit.TableOffLazyTree)
	asm.CBNZ(jit.X4, slowLabel) // lazy fields must be resolved by runtime
	jit.EmitBoxNil(asm, jit.X0)
	if feedbackEnabled {
		emitBaselineFeedbackResult(asm, pc, 7, "getfield_empty")
	}
	storeSlot(asm, a, jit.X0)
	asm.B(doneLabel)

	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_GETFIELD, pc, a, b, c)

	asm.Label(doneLabel)
}

func emitBaselineFieldPolyLookup(asm *jit.Assembler, pc, dstSlot int, tableReg, shapeReg jit.Reg, feedbackEnabled bool, feedbackName, slowLabel, doneLabel string) {
	asm.LDR(jit.X5, mRegCtx, execCtxOffBaselineFieldPolyCache)
	asm.CBZ(jit.X5, slowLabel)
	entryOff := pc * runtime.FieldPolyCacheWays * jit.FieldPolyCacheEntrySize
	if entryOff > 0 {
		if entryOff <= 4095 {
			asm.ADDimm(jit.X5, jit.X5, uint16(entryOff))
		} else {
			asm.LoadImm64(jit.X6, int64(entryOff))
			asm.ADDreg(jit.X5, jit.X5, jit.X6)
		}
	}

	for i := 0; i < runtime.FieldPolyCacheWays; i++ {
		nextWayLabel := nextLabel("field_poly_next")
		asm.LDRW(jit.X6, jit.X5, jit.FieldPolyCacheEntryOffShapeID)
		asm.CMPreg(jit.X6, shapeReg)
		asm.BCond(jit.CondNE, nextWayLabel)

		asm.LDR(jit.X3, jit.X5, jit.FieldPolyCacheEntryOffFieldIdx)
		asm.LDR(jit.X6, tableReg, jit.TableOffSvalsLen)
		asm.CMPreg(jit.X3, jit.X6)
		asm.BCond(jit.CondGE, slowLabel)

		asm.LDR(jit.X6, tableReg, jit.TableOffSvals)
		asm.LDRreg(jit.X0, jit.X6, jit.X3)
		if feedbackEnabled {
			emitBaselineFeedbackResultFromValue(asm, pc, jit.X0, feedbackName)
		}
		storeSlot(asm, dstSlot, jit.X0)
		asm.B(doneLabel)

		asm.Label(nextWayLabel)
		if i+1 < runtime.FieldPolyCacheWays {
			asm.ADDimm(jit.X5, jit.X5, uint16(jit.FieldPolyCacheEntrySize))
		}
	}
	asm.B(slowLabel)
}

// emitBaselineSetField emits native ARM64 for OP_SETFIELD: R(A).field[B] = RK(C)
// Uses runtime inline cache from proto.FieldCache[pc].
func emitBaselineSetField(asm *jit.Assembler, inst uint32, pc int, feedbackEnabled bool) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst) // constant index for field name
	c := vm.DecodeC(inst) // RK(C) = value

	slowLabel := nextLabel("setfield_slow")
	doneLabel := nextLabel("setfield_done")

	// Load FieldCache pointer from ExecContext.
	asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineFieldCache)
	asm.CBZ(jit.X0, slowLabel)

	// Compute &FieldCache[pc].
	if pc > 0 {
		entryOff := pc * jit.FieldCacheEntrySize
		if entryOff < 4096 {
			asm.ADDimm(jit.X0, jit.X0, uint16(entryOff))
		} else {
			asm.LoadImm64(jit.X1, int64(entryOff))
			asm.ADDreg(jit.X0, jit.X0, jit.X1)
		}
	}
	asm.MOVreg(jit.X7, jit.X0) // X7 = &FieldCache[pc]

	// Load entry.ShapeID.
	asm.LDRW(jit.X2, jit.X0, jit.FieldCacheEntryOffShapeID)
	asm.CBZ(jit.X2, slowLabel)

	// Load entry.FieldIdx.
	asm.LDR(jit.X3, jit.X0, jit.FieldCacheEntryOffFieldIdx) // X3 = fieldIdx

	// Load table value from R(A).
	loadSlot(asm, jit.X0, a)

	// Check table pointer.
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X4, slowLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, slowLabel)

	// Shape guard.
	asm.LDRW(jit.X1, jit.X0, jit.TableOffShapeID)
	asm.CMPreg(jit.X1, jit.X2)
	appendLabel := nextLabel("setfield_append")
	asm.BCond(jit.CondNE, appendLabel)

	// Bounds check: fieldIdx < len(svals)
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvalsLen)
	asm.CMPreg(jit.X3, jit.X1)
	asm.BCond(jit.CondGE, slowLabel)

	// Load value to store: RK(C).
	loadRK(asm, jit.X4, c) // X4 = value

	// Mirror the interpreter/slow-path SETFIELD feedback for cache hits too.
	if feedbackEnabled {
		emitBaselineFeedbackResultFromValue(asm, pc, jit.X4, "setfield")
	}

	// Direct field store: svals[fieldIdx] = value.
	// STRreg uses [Xn + Xm, LSL #3] which already scales by 8 (= ValueSize),
	// so X3 must hold the raw fieldIdx (not pre-multiplied).
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvals) // X1 = svals data pointer
	asm.STRreg(jit.X4, jit.X1, jit.X3)         // svals[fieldIdx] = value

	asm.B(doneLabel)

	// Constructor-style append: the Go slow path records AppendShapeID and
	// AppendShape when this SETFIELD appends a new key to a small shaped table.
	asm.Label(appendLabel)
	asm.LDRW(jit.X5, jit.X7, fieldCacheEntryOffAppendShapeID)
	asm.CMPreg(jit.X1, jit.X5)
	asm.BCond(jit.CondNE, slowLabel)
	asm.LDR(jit.X5, jit.X7, fieldCacheEntryOffAppendShape)
	asm.CBZ(jit.X5, slowLabel)
	asm.LDR(jit.X6, jit.X0, jit.TableOffSmap)
	asm.CBNZ(jit.X6, slowLabel)
	asm.LDR(jit.X6, jit.X0, jit.TableOffLazyTree)
	asm.CBNZ(jit.X6, slowLabel)
	asm.LDR(jit.X6, jit.X0, jit.TableOffSvalsLen)
	asm.CMPreg(jit.X3, jit.X6)
	asm.BCond(jit.CondNE, slowLabel)
	asm.CMPimm(jit.X3, runtime.SmallFieldCap)
	asm.BCond(jit.CondGE, slowLabel)
	asm.LDR(jit.X8, jit.X0, jit.TableOffSvals+16)
	asm.CMPreg(jit.X3, jit.X8)
	asm.BCond(jit.CondGE, slowLabel)

	loadRK(asm, jit.X4, c)
	asm.LoadImm64(jit.X8, nb64(jit.NB_ValNil))
	asm.CMPreg(jit.X4, jit.X8)
	asm.BCond(jit.CondEQ, slowLabel)
	asm.LDR(jit.X8, jit.X0, jit.TableOffSvals)
	asm.STRreg(jit.X4, jit.X8, jit.X3)
	asm.ADDimm(jit.X6, jit.X6, 1)
	asm.STR(jit.X6, jit.X0, jit.TableOffSvalsLen)
	asm.STRW(jit.X2, jit.X0, jit.TableOffShapeID)
	asm.STR(jit.X5, jit.X0, jit.TableOffShape)
	asm.LDR(jit.X8, jit.X5, shapeOffFieldKeys)
	asm.STR(jit.X8, jit.X0, jit.TableOffSkeys)
	asm.LDR(jit.X8, jit.X5, shapeOffFieldKeysLen)
	asm.STR(jit.X8, jit.X0, jit.TableOffSkeysLen)
	asm.LDR(jit.X8, jit.X5, shapeOffFieldKeysCap)
	asm.STR(jit.X8, jit.X0, jit.TableOffSkeys+16)
	asm.MOVimm16(jit.X8, 1)
	asm.STRB(jit.X8, jit.X0, jit.TableOffKeysDirty)
	asm.B(doneLabel)

	// Slow path: exit-resume.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_SETFIELD, pc, a, b, c)

	asm.Label(doneLabel)
}

// emitBaselineGetTable emits native ARM64 for OP_GETTABLE: R(A) = R(B)[RK(C)]
// Fast path for integer keys with array bounds check.
// Supports ArrayMixed ([]Value), ArrayInt ([]int64), ArrayFloat ([]float64),
// and ArrayBool ([]byte) array kinds.
func emitBaselineGetTable(asm *jit.Assembler, inst uint32, pc int, feedbackEnabled bool) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)

	slowLabel := nextLabel("gettable_slow")
	doneLabel := nextLabel("gettable_done")
	intArrayLabel := nextLabel("gettable_intarr")
	floatArrayLabel := nextLabel("gettable_floatarr")
	boolArrayLabel := nextLabel("gettable_boolarr")
	stringKeyLabel := nextLabel("gettable_string_key")

	// Load table value from R(B).
	loadSlot(asm, jit.X0, b)

	// Check table pointer.
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, slowLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, slowLabel)

	// Check metatable is nil (offset TableOffMetatable, must be 0).
	asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
	asm.CBNZ(jit.X1, slowLabel) // has metatable -> slow path

	// Load key RK(C).
	loadRK(asm, jit.X1, cidx) // X1 = key (NaN-boxed)

	// Check if key is integer.
	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, stringKeyLabel) // not int -> try dynamic string-key cache

	// Extract integer key.
	asm.SBFX(jit.X1, jit.X1, 0, 48) // X1 = signed int key

	// Check key >= 0 (shared by all array kinds).
	asm.CMPimm(jit.X1, 0)
	asm.BCond(jit.CondLT, slowLabel)

	// Dispatch on arrayKind: 0=Mixed, 1=Int, 2=Float, 3=Bool, else=slow.
	asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, jit.AKBool)
	asm.BCond(jit.CondEQ, boolArrayLabel)
	asm.CMPimm(jit.X2, jit.AKFloat)
	asm.BCond(jit.CondEQ, floatArrayLabel)
	asm.CMPimm(jit.X2, jit.AKInt)
	asm.BCond(jit.CondEQ, intArrayLabel)
	asm.CBNZ(jit.X2, slowLabel) // not Mixed (0) -> slow

	// --- ArrayMixed fast path ---
	asm.LDR(jit.X2, jit.X0, jit.TableOffArrayLen) // X2 = array.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, slowLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffArray) // X2 = array data pointer
	if feedbackEnabled {
		emitBaselineFeedbackDenseMatrix(asm, pc, jit.X0, "mixed")
	}
	asm.LDRreg(jit.X0, jit.X2, jit.X1) // X0 = array[key] (NaN-boxed Value)
	storeSlot(asm, a, jit.X0)
	if feedbackEnabled {
		emitBaselineFeedbackResultFromValue(asm, pc, jit.X0, "mixed") // includes FBTable for table-of-tables rows
		emitBaselineFeedbackKind(asm, pc, 1, "mixed")                 // FBKindMixed=1
	}
	asm.B(doneLabel)

	// --- ArrayInt fast path ---
	asm.Label(intArrayLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffIntArrayLen) // X2 = intArray.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, slowLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffIntArray) // X2 = intArray data pointer
	asm.LDRreg(jit.X0, jit.X2, jit.X1)            // X0 = intArray[key] (raw int64)
	// NaN-box the int64: UBFX + ORR with pinned tag register.
	jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
	storeSlot(asm, a, jit.X0)
	if feedbackEnabled {
		emitBaselineFeedbackResult(asm, pc, 1, "int") // FBInt=1
		emitBaselineFeedbackKind(asm, pc, 2, "int")   // FBKindInt=2
	}
	asm.B(doneLabel)

	// --- ArrayFloat fast path ---
	asm.Label(floatArrayLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffFloatArrayLen) // X2 = floatArray.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, slowLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffFloatArray) // X2 = floatArray data pointer
	asm.LDRreg(jit.X0, jit.X2, jit.X1)              // X0 = raw float64 bits = floatArray[key]
	// Float64 bits ARE the NaN-boxed value — no conversion needed!
	storeSlot(asm, a, jit.X0)
	if feedbackEnabled {
		emitBaselineFeedbackResult(asm, pc, 2, "float") // FBFloat=2
		emitBaselineFeedbackKind(asm, pc, 3, "float")   // FBKindFloat=3
	}
	asm.B(doneLabel)

	// --- ArrayBool fast path ---
	asm.Label(boolArrayLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffBoolArrayLen) // X2 = boolArray.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, slowLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffBoolArray) // X2 = boolArray data pointer
	asm.LDRBreg(jit.X3, jit.X2, jit.X1)            // X3 = byte = boolArray[key]
	// Convert byte to NaN-boxed value: 0=nil, 1=false, 2=true
	boolNilLabel := nextLabel("gettable_bool_nil")
	boolFalseLabel := nextLabel("gettable_bool_false")
	asm.CBZ(jit.X3, boolNilLabel) // byte == 0 → nil
	asm.CMPimm(jit.X3, 1)
	asm.BCond(jit.CondEQ, boolFalseLabel) // byte == 1 → false
	// byte == 2 → true: NaN-boxed true = 0xFFFD000000000001
	asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool|1))
	storeSlot(asm, a, jit.X0)
	if feedbackEnabled {
		emitBaselineFeedbackResult(asm, pc, 4, "bool_true") // FBBool=4
		emitBaselineFeedbackKind(asm, pc, 4, "bool_true")   // FBKindBool=4
	}
	asm.B(doneLabel)
	asm.Label(boolFalseLabel)
	// NaN-boxed false = 0xFFFD000000000000
	asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool))
	storeSlot(asm, a, jit.X0)
	if feedbackEnabled {
		emitBaselineFeedbackResult(asm, pc, 4, "bool_false") // FBBool=4
		emitBaselineFeedbackKind(asm, pc, 4, "bool_false")   // FBKindBool=4
	}
	asm.B(doneLabel)
	asm.Label(boolNilLabel)
	// NaN-boxed nil = 0xFFFC000000000000
	asm.LoadImm64(jit.X0, nb64(jit.NB_ValNil))
	storeSlot(asm, a, jit.X0)
	if feedbackEnabled {
		emitBaselineFeedbackResult(asm, pc, 7, "bool_nil") // FBAny=7 for nil
		emitBaselineFeedbackKind(asm, pc, 4, "bool_nil")   // FBKindBool=4 (still a bool array)
	}
	asm.B(doneLabel)

	asm.Label(stringKeyLabel)
	emitBaselineDynamicStringGetTable(asm, a, pc, feedbackEnabled, slowLabel, doneLabel)

	// Slow path: exit-resume.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_GETTABLE, pc, a, b, cidx)

	asm.Label(doneLabel)
}

// emitBaselineSetTable emits native ARM64 for OP_SETTABLE: R(A)[RK(B)] = RK(C)
// Fast path for integer keys with array bounds check.
// Supports ArrayMixed ([]Value), ArrayInt ([]int64), ArrayFloat ([]float64),
// and ArrayBool ([]byte) array kinds.
func emitBaselineSetTable(asm *jit.Assembler, inst uint32, pc int, feedbackEnabled bool) {
	a := vm.DecodeA(inst)
	bidx := vm.DecodeB(inst) // RK(B) = key
	cidx := vm.DecodeC(inst) // RK(C) = value

	slowLabel := nextLabel("settable_slow")
	doneLabel := nextLabel("settable_done")
	intArrayLabel := nextLabel("settable_intarr")
	floatArrayLabel := nextLabel("settable_floatarr")
	boolArrayLabel := nextLabel("settable_boolarr")
	stringKeyLabel := nextLabel("settable_string_key")

	// Load table value from R(A).
	loadSlot(asm, jit.X0, a)

	// Check table pointer.
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, slowLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, slowLabel)

	// Check metatable is nil.
	asm.LDR(jit.X1, jit.X0, jit.TableOffMetatable)
	asm.CBNZ(jit.X1, slowLabel)

	// Load key RK(B).
	loadRK(asm, jit.X1, bidx) // X1 = key (NaN-boxed)

	// Check if key is integer.
	asm.LSRimm(jit.X2, jit.X1, 48)
	asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
	asm.CMPreg(jit.X2, jit.X3)
	asm.BCond(jit.CondNE, stringKeyLabel) // not int -> try dynamic string-key cache

	// Extract integer key.
	asm.SBFX(jit.X1, jit.X1, 0, 48) // X1 = signed int key

	// Check key >= 0 (shared by both array kinds).
	asm.CMPimm(jit.X1, 0)
	asm.BCond(jit.CondLT, slowLabel)

	// Dispatch on arrayKind: 0=Mixed, 1=Int, 2=Float, 3=Bool, else=slow.
	asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, jit.AKBool)
	asm.BCond(jit.CondEQ, boolArrayLabel)
	asm.CMPimm(jit.X2, jit.AKFloat)
	asm.BCond(jit.CondEQ, floatArrayLabel)
	asm.CMPimm(jit.X2, jit.AKInt)
	asm.BCond(jit.CondEQ, intArrayLabel)
	asm.CBNZ(jit.X2, slowLabel) // not Mixed (0) -> slow

	// --- ArrayMixed fast path ---
	mixedStoreLabel := nextLabel("settable_mixed_store")
	mixedAppendLabel := nextLabel("settable_mixed_append")
	emitTypedArraySetBoundsOrAppendCheck(asm, jit.X0, jit.X1, jit.X2, jit.TableOffArrayLen, mixedAppendLabel, slowLabel)
	asm.Label(mixedStoreLabel)
	loadRK(asm, jit.X4, cidx) // X4 = value (NaN-boxed)
	asm.LDR(jit.X2, jit.X0, jit.TableOffArray)
	asm.STRreg(jit.X4, jit.X2, jit.X1) // array[key] = value
	asm.MOVimm16(jit.X5, 1)
	asm.STRB(jit.X5, jit.X0, jit.TableOffKeysDirty)
	if feedbackEnabled {
		emitBaselineFeedbackKind(asm, pc, 1, "set_mixed") // FBKindMixed=1
	}
	asm.B(doneLabel)
	emitTypedArraySetAppendPath(asm, jit.X0, jit.X1, jit.X6, jit.TableOffArrayLen, jit.TableOffArrayCap, mixedAppendLabel, slowLabel, mixedStoreLabel)

	// --- ArrayInt fast path ---
	asm.Label(intArrayLabel)
	intStoreLabel := nextLabel("settable_int_store")
	intAppendLabel := nextLabel("settable_int_append")
	emitTypedArraySetBoundsOrAppendCheck(asm, jit.X0, jit.X1, jit.X2, jit.TableOffIntArrayLen, intAppendLabel, slowLabel)
	asm.Label(intStoreLabel)
	// Load value RK(C) and check it's an integer.
	loadRK(asm, jit.X4, cidx) // X4 = value (NaN-boxed)
	asm.LSRimm(jit.X5, jit.X4, 48)
	asm.MOVimm16(jit.X6, uint16(jit.NB_TagIntShr48))
	asm.CMPreg(jit.X5, jit.X6)
	asm.BCond(jit.CondNE, slowLabel) // value not int -> slow (type mismatch)
	// Unbox int64 from NaN-boxed value.
	asm.SBFX(jit.X4, jit.X4, 0, 48) // X4 = raw int64
	asm.LDR(jit.X2, jit.X0, jit.TableOffIntArray)
	asm.STRreg(jit.X4, jit.X2, jit.X1) // intArray[key] = int64
	if feedbackEnabled {
		emitBaselineFeedbackKind(asm, pc, 2, "set_int") // FBKindInt=2
	}
	asm.B(doneLabel)
	emitTypedArraySetAppendPathDirty(asm, jit.X0, jit.X1, jit.X6, jit.TableOffIntArrayLen, jit.TableOffIntArrayCap, intAppendLabel, slowLabel, intStoreLabel)

	// --- ArrayFloat fast path ---
	asm.Label(floatArrayLabel)
	floatStoreLabel := nextLabel("settable_float_store")
	floatAppendLabel := nextLabel("settable_float_append")
	emitTypedArraySetBoundsOrAppendCheck(asm, jit.X0, jit.X1, jit.X2, jit.TableOffFloatArrayLen, floatAppendLabel, slowLabel)
	asm.Label(floatStoreLabel)
	// Load value RK(C) and check it's a float.
	loadRK(asm, jit.X4, cidx) // X4 = value (NaN-boxed)
	// Float check: if top bits indicate tagged (int/bool/nil/ptr), not a float → slow.
	jit.EmitIsTagged(asm, jit.X4, jit.X5) // sets flags: EQ = tagged, NE = float
	asm.BCond(jit.CondEQ, slowLabel)      // tagged → slow (not a float)
	// Float64 bits ARE the NaN-boxed representation — store directly.
	asm.LDR(jit.X2, jit.X0, jit.TableOffFloatArray) // floatArray data pointer
	asm.STRreg(jit.X4, jit.X2, jit.X1)              // floatArray[key] = float64
	if feedbackEnabled {
		emitBaselineFeedbackKind(asm, pc, 3, "set_float") // FBKindFloat=3
	}
	asm.B(doneLabel)
	emitTypedArraySetAppendPathDirty(asm, jit.X0, jit.X1, jit.X6, jit.TableOffFloatArrayLen, jit.TableOffFloatArrayCap, floatAppendLabel, slowLabel, floatStoreLabel)

	// --- ArrayBool fast path ---
	asm.Label(boolArrayLabel)
	boolStoreLabel := nextLabel("settable_bool_store")
	boolAppendLabel := nextLabel("settable_bool_append")
	emitTypedArraySetBoundsOrAppendCheck(asm, jit.X0, jit.X1, jit.X2, jit.TableOffBoolArrayLen, boolAppendLabel, slowLabel)
	asm.Label(boolStoreLabel)
	// Load value RK(C).
	loadRK(asm, jit.X4, cidx) // X4 = value (NaN-boxed)
	// Check value type: must be bool (tag=0xFFFD) or nil (0xFFFC).
	asm.LSRimm(jit.X5, jit.X4, 48)
	asm.MOVimm16(jit.X6, uint16(jit.NB_TagBoolShr48))
	asm.CMPreg(jit.X5, jit.X6)
	boolOkLabel := nextLabel("settable_bool_isbool")
	asm.BCond(jit.CondEQ, boolOkLabel)
	// Check if nil.
	asm.MOVimm16(jit.X6, uint16(jit.NB_TagNilShr48))
	asm.CMPreg(jit.X5, jit.X6)
	asm.BCond(jit.CondNE, slowLabel) // not bool, not nil → slow
	// Nil → byte 0.
	asm.MOVimm16(jit.X4, 0)
	setByteLabel := nextLabel("settable_bool_store")
	asm.B(setByteLabel)
	asm.Label(boolOkLabel)
	// Bool: extract payload bit 0. false=0xFFFD000000000000 (payload=0) → byte 1
	//                                true=0xFFFD000000000001 (payload=1) → byte 2
	// Conversion: byte = payload + 1
	asm.LoadImm64(jit.X5, 1)
	asm.ANDreg(jit.X4, jit.X4, jit.X5) // extract bit 0 (payload)
	asm.ADDimm(jit.X4, jit.X4, 1)      // 0→1 (false), 1→2 (true)
	asm.Label(setByteLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffBoolArray) // boolArray data pointer
	asm.STRBreg(jit.X4, jit.X2, jit.X1)            // boolArray[key] = byte
	// Set keysDirty flag.
	asm.MOVimm16(jit.X5, 1)
	asm.STRB(jit.X5, jit.X0, jit.TableOffKeysDirty)
	if feedbackEnabled {
		emitBaselineFeedbackKind(asm, pc, 4, "set_bool") // FBKindBool=4
	}
	asm.B(doneLabel)
	emitTypedArraySetAppendPath(asm, jit.X0, jit.X1, jit.X6, jit.TableOffBoolArrayLen, jit.TableOffBoolArrayCap, boolAppendLabel, slowLabel, boolStoreLabel)

	asm.Label(stringKeyLabel)
	emitBaselineDynamicStringSetTable(asm, cidx, pc, feedbackEnabled, slowLabel, doneLabel)

	// Slow path: exit-resume.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_SETTABLE, pc, a, bidx, cidx)

	asm.Label(doneLabel)
}

func emitBaselineDynamicStringCacheProbe(asm *jit.Assembler, pc int, slowLabel string, hit func(fieldIdxReg jit.Reg), valueHit func(valueReg jit.Reg)) {
	// Inputs: X0 = *Table, X1 = NaN-boxed string candidate.
	// Clobbers X2-X11. Falls through to slowLabel on cache miss.
	jit.EmitCheckIsString(asm, jit.X1, jit.X2, jit.X3, slowLabel)
	jit.EmitExtractPtr(asm, jit.X4, jit.X1) // X4 = *string header
	asm.CBZ(jit.X4, slowLabel)
	asm.LDR(jit.X5, jit.X4, 0) // X5 = string data
	asm.LDR(jit.X6, jit.X4, 8) // X6 = string len

	asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineTableStringKeyCache)
	asm.CBZ(jit.X3, slowLabel)
	entryOff := pc * runtime.TableStringKeyCacheWays * tableStringKeyCacheEntrySize
	if entryOff > 0 {
		if entryOff <= 4095 {
			asm.ADDimm(jit.X3, jit.X3, uint16(entryOff))
		} else {
			asm.LoadImm64(jit.X7, int64(entryOff))
			asm.ADDreg(jit.X3, jit.X3, jit.X7)
		}
	}

	asm.LDR(jit.X8, jit.X0, jit.TableOffLazyTree)
	asm.CBNZ(jit.X8, slowLabel)
	asm.LDRW(jit.X7, jit.X0, jit.TableOffShapeID)
	smapCacheLabel := nextLabel("dyn_string_smap_cache")
	asm.CBZ(jit.X7, smapCacheLabel)

	loopLabel := nextLabel("dyn_string_cache_loop")
	nextEntryLabel := nextLabel("dyn_string_cache_next")
	asm.MOVimm16(jit.X9, 0)
	asm.Label(loopLabel)
	asm.LDR(jit.X10, jit.X3, tableStringKeyCacheEntryKeyData)
	asm.CMPreg(jit.X10, jit.X5)
	asm.BCond(jit.CondNE, nextEntryLabel)
	asm.LDR(jit.X10, jit.X3, tableStringKeyCacheEntryKeyLen)
	asm.CMPreg(jit.X10, jit.X6)
	asm.BCond(jit.CondNE, nextEntryLabel)
	asm.LDRW(jit.X10, jit.X3, tableStringKeyCacheEntryShapeID)
	asm.CMPreg(jit.X10, jit.X7)
	asm.BCond(jit.CondNE, nextEntryLabel)
	asm.LDR(jit.X11, jit.X3, tableStringKeyCacheEntryFieldIdx)
	asm.LDR(jit.X10, jit.X0, jit.TableOffSvalsLen)
	asm.CMPreg(jit.X11, jit.X10)
	asm.BCond(jit.CondGE, slowLabel)
	hit(jit.X11)

	asm.Label(nextEntryLabel)
	asm.ADDimm(jit.X3, jit.X3, uint16(tableStringKeyCacheEntrySize))
	asm.ADDimm(jit.X9, jit.X9, 1)
	asm.CMPimm(jit.X9, runtime.TableStringKeyCacheWays)
	asm.BCond(jit.CondLT, loopLabel)
	asm.B(slowLabel)

	asm.Label(smapCacheLabel)
	if valueHit == nil {
		asm.B(slowLabel)
		return
	}
	asm.LDR(jit.X8, jit.X0, jit.TableOffStringLookupCache)
	asm.CBZ(jit.X8, slowLabel)
	asm.LDR(jit.X3, jit.X8, jit.StringLookupCacheOffEntries)
	asm.CBZ(jit.X3, slowLabel)
	asm.LDR(jit.X10, jit.X8, jit.StringLookupCacheOffMask)
	hashLoopLabel := nextLabel("dyn_string_smap_hash_loop")
	hashDoneLabel := nextLabel("dyn_string_smap_hash_done")
	asm.LoadImm64(jit.X9, int64(1469598103934665603))
	asm.LoadImm64(jit.X15, int64(1099511628211))
	asm.MOVimm16(jit.X11, 0)
	asm.Label(hashLoopLabel)
	asm.CMPreg(jit.X11, jit.X6)
	asm.BCond(jit.CondGE, hashDoneLabel)
	asm.LDRBreg(jit.X14, jit.X5, jit.X11)
	asm.EORreg(jit.X9, jit.X9, jit.X14)
	asm.MUL(jit.X9, jit.X9, jit.X15)
	asm.ADDimm(jit.X11, jit.X11, 1)
	asm.B(hashLoopLabel)
	asm.Label(hashDoneLabel)
	asm.MOVreg(jit.X15, jit.X9)
	asm.ANDreg(jit.X9, jit.X9, jit.X10)

	smapLoopLabel := nextLabel("dyn_string_smap_loop")
	smapNextLabel := nextLabel("dyn_string_smap_next")
	smapFoundLabel := nextLabel("dyn_string_smap_found")
	smapByteLoopLabel := nextLabel("dyn_string_smap_bytes")
	asm.MOVimm16(jit.X13, 0)
	asm.Label(smapLoopLabel)
	asm.ADDreg(jit.X11, jit.X9, jit.X13)
	asm.ANDreg(jit.X11, jit.X11, jit.X10)
	asm.LSLimm(jit.X12, jit.X11, 6) // idx * 64
	asm.ADDreg(jit.X12, jit.X3, jit.X12)
	asm.LDRB(jit.X14, jit.X12, jit.StringLookupCacheEntryOffValid)
	asm.CBZ(jit.X14, slowLabel)
	asm.LDR(jit.X14, jit.X12, jit.StringLookupCacheEntryOffHash)
	asm.CMPreg(jit.X14, jit.X15)
	asm.BCond(jit.CondNE, smapNextLabel)
	asm.LDR(jit.X14, jit.X12, jit.StringLookupCacheEntryOffKeyLen)
	asm.CMPreg(jit.X14, jit.X6)
	asm.BCond(jit.CondNE, smapNextLabel)
	asm.LDR(jit.X14, jit.X12, jit.StringLookupCacheEntryOffKeyData)
	asm.CMPreg(jit.X14, jit.X5)
	asm.BCond(jit.CondEQ, smapFoundLabel)
	asm.CBZ(jit.X6, smapFoundLabel)
	asm.MOVimm16(jit.X15, 0)
	asm.Label(smapByteLoopLabel)
	asm.LDRBreg(jit.X16, jit.X14, jit.X15)
	asm.LDRBreg(jit.X17, jit.X5, jit.X15)
	asm.CMPreg(jit.X16, jit.X17)
	asm.BCond(jit.CondNE, smapNextLabel)
	asm.ADDimm(jit.X15, jit.X15, 1)
	asm.CMPreg(jit.X15, jit.X6)
	asm.BCond(jit.CondLT, smapByteLoopLabel)
	asm.Label(smapFoundLabel)
	asm.LDR(jit.X0, jit.X12, jit.StringLookupCacheEntryOffValue)
	valueHit(jit.X0)

	asm.Label(smapNextLabel)
	asm.ADDimm(jit.X13, jit.X13, 1)
	asm.CMPimm(jit.X13, runtime.StringLookupCacheProbeLimit)
	asm.BCond(jit.CondLT, smapLoopLabel)
	asm.B(slowLabel)
}

func emitBaselineDynamicStringGetTable(asm *jit.Assembler, a, pc int, feedbackEnabled bool, slowLabel, doneLabel string) {
	emitBaselineDynamicStringCacheProbe(asm, pc, slowLabel, func(fieldIdxReg jit.Reg) {
		asm.LDR(jit.X10, jit.X0, jit.TableOffSvals)
		asm.LDRreg(jit.X0, jit.X10, fieldIdxReg)
		if feedbackEnabled {
			emitBaselineTableStringKeyCacheHitFeedback(asm, pc, vm.TableAccessKindGet, jit.X0, "gettable_string")
			emitBaselineFeedbackResultFromValue(asm, pc, jit.X0, "gettable_string")
		}
		storeSlot(asm, a, jit.X0)
		asm.B(doneLabel)
	}, func(valueReg jit.Reg) {
		if feedbackEnabled {
			emitBaselineFeedbackResultFromValue(asm, pc, valueReg, "gettable_string_map")
		}
		storeSlot(asm, a, valueReg)
		asm.B(doneLabel)
	})
}

func emitBaselineDynamicStringSetTable(asm *jit.Assembler, cidx, pc int, feedbackEnabled bool, slowLabel, doneLabel string) {
	emitBaselineDynamicStringCacheProbe(asm, pc, slowLabel, func(fieldIdxReg jit.Reg) {
		loadRK(asm, jit.X4, cidx)
		asm.LoadImm64(jit.X12, nb64(jit.NB_ValNil))
		asm.CMPreg(jit.X4, jit.X12)
		asm.BCond(jit.CondEQ, slowLabel)
		if feedbackEnabled {
			emitBaselineTableStringKeyCacheHitFeedback(asm, pc, vm.TableAccessKindSet, jit.X4, "settable_string")
			emitBaselineFeedbackResultFromValue(asm, pc, jit.X4, "settable_string")
		}
		asm.LDR(jit.X10, jit.X0, jit.TableOffSvals)
		asm.STRreg(jit.X4, jit.X10, fieldIdxReg)
		asm.MOVimm16(jit.X5, 1)
		asm.STRB(jit.X5, jit.X0, jit.TableOffKeysDirty)
		asm.B(doneLabel)
	}, nil)
}

// emitBaselineTableStringKeyCacheHitFeedback mirrors the stable-fact portion of
// TableKeyFeedback.ObserveTableAccess for Tier 1 native dynamic string cache
// hits. Inputs are preserved for the caller:
//
//	X5 = key data pointer, X6 = key length, X7 = table shape id, X11 = field idx.
func emitBaselineTableStringKeyCacheHitFeedback(asm *jit.Assembler, pc int, accessKind uint8, valueReg jit.Reg, suffix string) {
	skipLabel := nextLabel("tkf_hit_skip_" + suffix)
	shapeSetLabel := nextLabel("tkf_hit_shape_set_" + suffix)
	fieldSetLabel := nextLabel("tkf_hit_field_set_" + suffix)
	fieldSeenLabel := nextLabel("tkf_hit_field_seen_" + suffix)
	keySeenLabel := nextLabel("tkf_hit_key_seen_" + suffix)
	keyCompareLoopLabel := nextLabel("tkf_hit_key_cmp_loop_" + suffix)
	keyPolyLabel := nextLabel("tkf_hit_key_poly_" + suffix)

	asm.LDR(jit.X12, mRegCtx, execCtxOffBaselineTableKeyFeedbackPtr)
	asm.CBZ(jit.X12, skipLabel)
	entryOff := pc * tableKeyFeedbackSize
	if entryOff > 0 {
		if entryOff <= 4095 {
			asm.ADDimm(jit.X12, jit.X12, uint16(entryOff))
		} else {
			asm.LoadImm64(jit.X13, int64(entryOff))
			asm.ADDreg(jit.X12, jit.X12, jit.X13)
		}
	}

	asm.LDRB(jit.X13, jit.X12, tableKeyFeedbackStringKeySeenOff)
	asm.CBZ(jit.X13, skipLabel)

	asm.LDR(jit.X13, jit.X12, tableKeyFeedbackStringKeyOff)
	asm.LDR(jit.X14, jit.X12, tableKeyFeedbackStringKeyOff+8)
	asm.CMPreg(jit.X14, jit.X6)
	asm.BCond(jit.CondNE, keyPolyLabel)
	asm.CMPreg(jit.X13, jit.X5)
	asm.BCond(jit.CondEQ, keySeenLabel)
	asm.CBZ(jit.X14, keySeenLabel)
	asm.MOVimm16(jit.X15, 0)
	asm.Label(keyCompareLoopLabel)
	asm.LDRBreg(jit.X16, jit.X13, jit.X15)
	asm.LDRBreg(jit.X17, jit.X5, jit.X15)
	asm.CMPreg(jit.X16, jit.X17)
	asm.BCond(jit.CondNE, keyPolyLabel)
	asm.ADDimm(jit.X15, jit.X15, 1)
	asm.CMPreg(jit.X15, jit.X14)
	asm.BCond(jit.CondLT, keyCompareLoopLabel)
	asm.B(keySeenLabel)
	asm.Label(keyPolyLabel)
	emitBaselineTableKeyFeedbackOrFlag(asm, jit.X12, vm.TableAccessKeyPolymorphic)
	asm.B(skipLabel)
	asm.Label(keySeenLabel)

	asm.LDRW(jit.X13, jit.X12, tableKeyFeedbackCountOff)
	asm.ADDimm(jit.X13, jit.X13, 1)
	asm.STRW(jit.X13, jit.X12, tableKeyFeedbackCountOff)

	emitBaselineTableKeyFeedbackObserveByte(asm, jit.X12, tableKeyFeedbackKeyTypeOff, uint8(vm.FBString), suffix+"_key")
	emitBaselineTableKeyFeedbackObserveValueType(asm, jit.X12, tableKeyFeedbackValueTypeOff, valueReg, suffix+"_value")
	emitBaselineTableKeyFeedbackMergeAccessKind(asm, jit.X12, accessKind)

	asm.LDRW(jit.X13, jit.X12, tableKeyFeedbackShapeIDOff)
	asm.CMPreg(jit.X13, jit.X7)
	asm.BCond(jit.CondEQ, fieldSeenLabel)
	asm.CBZ(jit.X13, shapeSetLabel)
	emitBaselineTableKeyFeedbackOrFlag(asm, jit.X12, vm.TableAccessShapePolymorphic)
	asm.B(fieldSeenLabel)
	asm.Label(shapeSetLabel)
	asm.STRW(jit.X7, jit.X12, tableKeyFeedbackShapeIDOff)

	asm.Label(fieldSeenLabel)
	asm.LDRB(jit.X13, jit.X12, tableKeyFeedbackFieldIdxSeenOff)
	asm.CBZ(jit.X13, fieldSetLabel)
	asm.LDR(jit.X13, jit.X12, tableKeyFeedbackFieldIdxOff)
	asm.CMPreg(jit.X13, jit.X11)
	asm.BCond(jit.CondEQ, skipLabel)
	emitBaselineTableKeyFeedbackOrFlag(asm, jit.X12, vm.TableAccessFieldPolymorphic)
	asm.B(skipLabel)
	asm.Label(fieldSetLabel)
	asm.STR(jit.X11, jit.X12, tableKeyFeedbackFieldIdxOff)
	asm.MOVimm16(jit.X13, 1)
	asm.STRB(jit.X13, jit.X12, tableKeyFeedbackFieldIdxSeenOff)

	asm.Label(skipLabel)
}

func emitBaselineTableKeyFeedbackMergeAccessKind(asm *jit.Assembler, base jit.Reg, accessKind uint8) {
	doneLabel := nextLabel("tkf_access_done")
	setLabel := nextLabel("tkf_access_set")
	asm.LDRB(jit.X13, base, tableKeyFeedbackAccessKindOff)
	asm.CMPimm(jit.X13, uint16(accessKind))
	asm.BCond(jit.CondEQ, doneLabel)
	asm.CBZ(jit.X13, setLabel)
	asm.MOVimm16(jit.X14, uint16(accessKind))
	asm.ORRreg(jit.X13, jit.X13, jit.X14)
	asm.STRB(jit.X13, base, tableKeyFeedbackAccessKindOff)
	asm.B(doneLabel)
	asm.Label(setLabel)
	asm.MOVimm16(jit.X13, uint16(accessKind))
	asm.STRB(jit.X13, base, tableKeyFeedbackAccessKindOff)
	asm.Label(doneLabel)
}

func emitBaselineTableKeyFeedbackOrFlag(asm *jit.Assembler, base jit.Reg, flag uint16) {
	asm.LDRB(jit.X15, base, tableKeyFeedbackFlagsOff)
	asm.MOVimm16(jit.X16, flag)
	asm.ORRreg(jit.X15, jit.X15, jit.X16)
	asm.STRB(jit.X15, base, tableKeyFeedbackFlagsOff)
}

func emitBaselineTableKeyFeedbackObserveByte(asm *jit.Assembler, base jit.Reg, off int, observed uint8, suffix string) {
	doneLabel := nextLabel("tkf_byte_done_" + suffix)
	setLabel := nextLabel("tkf_byte_set_" + suffix)
	asm.LDRB(jit.X13, base, off)
	asm.CMPimm(jit.X13, uint16(observed))
	asm.BCond(jit.CondEQ, doneLabel)
	asm.CMPimm(jit.X13, uint16(vm.FBAny))
	asm.BCond(jit.CondEQ, doneLabel)
	asm.CBZ(jit.X13, setLabel)
	asm.MOVimm16(jit.X13, uint16(vm.FBAny))
	asm.STRB(jit.X13, base, off)
	asm.B(doneLabel)
	asm.Label(setLabel)
	asm.MOVimm16(jit.X13, uint16(observed))
	asm.STRB(jit.X13, base, off)
	asm.Label(doneLabel)
}

func emitBaselineTableKeyFeedbackObserveValueType(asm *jit.Assembler, base jit.Reg, off int, valReg jit.Reg, suffix string) {
	floatLabel := nextLabel("tkf_val_float_" + suffix)
	intLabel := nextLabel("tkf_val_int_" + suffix)
	boolLabel := nextLabel("tkf_val_bool_" + suffix)
	ptrLabel := nextLabel("tkf_val_ptr_" + suffix)
	stringLabel := nextLabel("tkf_val_string_" + suffix)
	tableLabel := nextLabel("tkf_val_table_" + suffix)
	functionLabel := nextLabel("tkf_val_function_" + suffix)
	updateLabel := nextLabel("tkf_val_update_" + suffix)

	asm.LSRimm(jit.X14, valReg, 48)
	asm.MOVimm16(jit.X15, jit.NB_TagNilShr48)
	asm.CMPreg(jit.X14, jit.X15)
	asm.BCond(jit.CondLT, floatLabel)
	asm.MOVimm16(jit.X15, jit.NB_TagIntShr48)
	asm.CMPreg(jit.X14, jit.X15)
	asm.BCond(jit.CondEQ, intLabel)
	asm.MOVimm16(jit.X15, jit.NB_TagBoolShr48)
	asm.CMPreg(jit.X14, jit.X15)
	asm.BCond(jit.CondEQ, boolLabel)
	asm.MOVimm16(jit.X15, jit.NB_TagPtrShr48)
	asm.CMPreg(jit.X14, jit.X15)
	asm.BCond(jit.CondEQ, ptrLabel)
	asm.MOVimm16(jit.X13, uint16(vm.FBAny))
	asm.B(updateLabel)

	asm.Label(ptrLabel)
	asm.LSRimm(jit.X14, valReg, uint8(jit.NB_PtrSubShift))
	asm.LoadImm64(jit.X15, 0xF)
	asm.ANDreg(jit.X14, jit.X14, jit.X15)
	asm.CMPimm(jit.X14, 0)
	asm.BCond(jit.CondEQ, tableLabel)
	asm.CMPimm(jit.X14, 1)
	asm.BCond(jit.CondEQ, stringLabel)
	asm.CMPimm(jit.X14, 9)
	asm.BCond(jit.CondEQ, stringLabel)
	asm.CMPimm(jit.X14, 2)
	asm.BCond(jit.CondEQ, functionLabel)
	asm.CMPimm(jit.X14, 3)
	asm.BCond(jit.CondEQ, functionLabel)
	asm.CMPimm(jit.X14, 6)
	asm.BCond(jit.CondEQ, functionLabel)
	asm.CMPimm(jit.X14, 8)
	asm.BCond(jit.CondEQ, functionLabel)
	asm.MOVimm16(jit.X13, uint16(vm.FBAny))
	asm.B(updateLabel)

	asm.Label(floatLabel)
	asm.MOVimm16(jit.X13, uint16(vm.FBFloat))
	asm.B(updateLabel)
	asm.Label(intLabel)
	asm.MOVimm16(jit.X13, uint16(vm.FBInt))
	asm.B(updateLabel)
	asm.Label(boolLabel)
	asm.MOVimm16(jit.X13, uint16(vm.FBBool))
	asm.B(updateLabel)
	asm.Label(stringLabel)
	asm.MOVimm16(jit.X13, uint16(vm.FBString))
	asm.B(updateLabel)
	asm.Label(tableLabel)
	asm.MOVimm16(jit.X13, uint16(vm.FBTable))
	asm.B(updateLabel)
	asm.Label(functionLabel)
	asm.MOVimm16(jit.X13, uint16(vm.FBFunction))

	asm.Label(updateLabel)
	emitBaselineTableKeyFeedbackObserveByteReg(asm, base, off, jit.X13, suffix)
}

func emitBaselineTableKeyFeedbackObserveByteReg(asm *jit.Assembler, base jit.Reg, off int, observed jit.Reg, suffix string) {
	doneLabel := nextLabel("tkf_byter_done_" + suffix)
	setLabel := nextLabel("tkf_byter_set_" + suffix)
	asm.LDRB(jit.X14, base, off)
	asm.CMPreg(jit.X14, observed)
	asm.BCond(jit.CondEQ, doneLabel)
	asm.CMPimm(jit.X14, uint16(vm.FBAny))
	asm.BCond(jit.CondEQ, doneLabel)
	asm.CBZ(jit.X14, setLabel)
	asm.MOVimm16(jit.X14, uint16(vm.FBAny))
	asm.STRB(jit.X14, base, off)
	asm.B(doneLabel)
	asm.Label(setLabel)
	asm.STRB(observed, base, off)
	asm.Label(doneLabel)
}

// emitBaselineLen emits ARM64 for OP_LEN: R(A) = #R(B).
// String length is a fixed header load and can stay native. Table length still
// falls back because mixed/bool arrays require the runtime's trailing-nil scan
// and tables may define __len.
func emitBaselineLen(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)

	slowLabel := nextLabel("len_slow")
	doneLabel := nextLabel("len_done")

	loadSlot(asm, jit.X0, b)
	jit.EmitCheckIsString(asm, jit.X0, jit.X1, jit.X2, slowLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.LDR(jit.X1, jit.X0, 8) // Go string header length.
	jit.EmitBoxIntFast(asm, jit.X0, jit.X1, mRegTagInt)
	storeSlot(asm, a, jit.X0)
	emitBaselineFeedbackResult(asm, pc, 1, "len_string")
	asm.B(doneLabel)

	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_LEN, pc, a, b, 0)
	asm.Label(doneLabel)
}

// emitBaselineSelf emits native ARM64 for OP_SELF: R(A+1) = R(B); R(A) = R(B)[RK(C)]
// This is R(A+1) = obj, R(A) = obj.method -- used for method calls.
func emitBaselineSelf(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)

	slowLabel := nextLabel("self_slow")
	doneLabel := nextLabel("self_done")
	polyMissLabel := nextLabel("self_poly_miss")

	// R(A+1) = R(B) (copy the object reference).
	loadSlot(asm, jit.X0, b)
	asm.STR(jit.X0, mRegRegs, slotOff(a+1))

	// Now do table lookup: R(A) = R(B)[RK(C)]
	// X0 already has the table value.

	// Check table pointer.
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X2, slowLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, slowLabel)

	// Load key RK(C).
	loadRK(asm, jit.X1, cidx) // X1 = key

	// Check if key is a string (most common case for method names).
	// For SELF, the key is typically a constant string.
	// We use the generic RawGet path: check if key is string, do skeys scan.
	// For simplicity, check if it's a string and do linear scan of skeys.
	jit.EmitCheckIsString(asm, jit.X1, jit.X2, jit.X3, slowLabel)

	// It's a string. Extract string pointer from NaN-box.
	// Actually, RawGet dispatches on type. For string keys, it calls RawGetString.
	// The string comparison is complex in JIT. Let's use the FieldCache instead
	// if available, or fall back to slow path.
	//
	// For method calls, the key is always a constant. We can use FieldCache[pc].
	asm.LDR(jit.X2, mRegCtx, execCtxOffBaselineFieldCache)
	asm.CBZ(jit.X2, slowLabel) // no field cache

	// Compute &FieldCache[pc].
	if pc > 0 {
		entryOff := pc * jit.FieldCacheEntrySize
		if entryOff < 4096 {
			asm.ADDimm(jit.X2, jit.X2, uint16(entryOff))
		} else {
			asm.LoadImm64(jit.X3, int64(entryOff))
			asm.ADDreg(jit.X2, jit.X2, jit.X3)
		}
	}

	// Load entry.ShapeID.
	asm.LDRW(jit.X3, jit.X2, jit.FieldCacheEntryOffShapeID)
	asm.CBZ(jit.X3, slowLabel)

	// Shape guard.
	asm.LDRW(jit.X4, jit.X0, jit.TableOffShapeID)
	asm.CMPreg(jit.X4, jit.X3)
	asm.BCond(jit.CondNE, polyMissLabel)

	// Load FieldIdx.
	asm.LDR(jit.X3, jit.X2, jit.FieldCacheEntryOffFieldIdx)

	// Bounds check.
	asm.LDR(jit.X4, jit.X0, jit.TableOffSvalsLen)
	asm.CMPreg(jit.X3, jit.X4)
	asm.BCond(jit.CondGE, slowLabel)

	// Direct access: svals[fieldIdx].
	// LDRreg uses [Xn + Xm, LSL #3] which already scales by 8 (= ValueSize),
	// so X3 must hold the raw fieldIdx (not pre-multiplied).
	asm.LDR(jit.X4, jit.X0, jit.TableOffSvals)
	asm.LDRreg(jit.X0, jit.X4, jit.X3)

	// Store result to R(A).
	storeSlot(asm, a, jit.X0)
	asm.B(doneLabel)

	asm.Label(polyMissLabel)
	asm.CBZ(jit.X4, slowLabel)
	emitBaselineFieldPolyLookup(asm, pc, a, jit.X0, jit.X4, false, "self_poly", slowLabel, doneLabel)

	// Slow path: exit-resume.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_SELF, pc, a, b, cidx)

	asm.Label(doneLabel)
}

// emitBaselineGetUpval emits native ARM64 for OP_GETUPVAL: R(A) = Upvalues[B].ref
// Uses the Closure pointer stored in ExecContext.
func emitBaselineGetUpval(asm *jit.Assembler, inst uint32, pc int, proto *vm.FuncProto) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)

	slowLabel := nextLabel("getupval_slow")
	doneLabel := nextLabel("getupval_done")

	// Load Closure pointer from ExecContext.
	asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.CBZ(jit.X0, slowLabel) // no closure pointer

	if proto != nil && len(proto.Upvalues) == 1 && b == 0 {
		// One-upvalue closures keep Upvalues[0] inline in the closure object.
		asm.LDR(jit.X2, jit.X0, vmClosureOffInlineUpvalue0)
	} else {
		// Closure.Upvalues is a []*Upvalue slice at offset 8.
		// Load slice data pointer.
		asm.LDR(jit.X1, jit.X0, 8) // X1 = Upvalues data ptr ([]* element ptr)

		// Load Upvalue pointer: Upvalues[B] (each element is 8 bytes = *Upvalue).
		asm.LDR(jit.X2, jit.X1, b*8) // X2 = *Upvalue
	}

	asm.CBZ(jit.X2, slowLabel)

	// Upvalue.ref is at offset 0 (*runtime.Value pointer).
	asm.LDR(jit.X3, jit.X2, 0) // X3 = ref ptr
	asm.CBZ(jit.X3, slowLabel)

	// Load the value: *ref.
	asm.LDR(jit.X0, jit.X3, 0) // X0 = *ref (the actual value)

	// Store to R(A).
	storeSlot(asm, a, jit.X0)
	asm.B(doneLabel)

	// Slow path: exit-resume.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_GETUPVAL, pc, a, b, 0)

	asm.Label(doneLabel)
}

// emitBaselineSetUpval emits native ARM64 for OP_SETUPVAL: Upvalues[B].ref = R(A)
func emitBaselineSetUpval(asm *jit.Assembler, inst uint32, pc int, proto *vm.FuncProto) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)

	slowLabel := nextLabel("setupval_slow")
	doneLabel := nextLabel("setupval_done")

	// Load Closure pointer from ExecContext.
	asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.CBZ(jit.X0, slowLabel)

	if proto != nil && len(proto.Upvalues) == 1 && b == 0 {
		// One-upvalue closures keep Upvalues[0] inline in the closure object.
		asm.LDR(jit.X2, jit.X0, vmClosureOffInlineUpvalue0)
	} else {
		// Load Upvalues slice data pointer.
		asm.LDR(jit.X1, jit.X0, 8) // Closure.Upvalues data ptr

		// Load Upvalue[B] pointer.
		asm.LDR(jit.X2, jit.X1, b*8) // *Upvalue
	}
	asm.CBZ(jit.X2, slowLabel)

	// Upvalue.ref at offset 0.
	asm.LDR(jit.X3, jit.X2, 0) // ref ptr
	asm.CBZ(jit.X3, slowLabel)

	// Load value from R(A).
	loadSlot(asm, jit.X4, a)

	// Store: *ref = value.
	asm.STR(jit.X4, jit.X3, 0)

	asm.B(doneLabel)

	// Slow path: exit-resume.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_SETUPVAL, pc, a, b, 0)

	asm.Label(doneLabel)
}

// emitBaselineFeedbackResult emits ARM64 code to record the Result type
// in the TypeFeedback[pc] entry. expectedFB is the FeedbackType constant
// (e.g., FBFloat=2, FBInt=1, FBBool=4). This implements the monotonic
// Observe logic: Unobserved→concrete, concrete→concrete (same=nop, diff→Any), Any→Any.
func emitBaselineFeedbackResult(asm *jit.Assembler, pc int, expectedFB uint16, suffix string) {
	emitBaselineFeedbackFixedAt(asm, pc, 2, expectedFB, suffix)
}

// emitBaselineFeedbackFixedAt is the generalized form — R85 Option 2.
// Writes expectedFB to the (Left=0|Right=1|Result=2) field of TypeFeedback[pc]
// with monotonic-observe semantics. Used when the operand type is statically
// known (e.g., IntSpec variants of OP_EQ/LT/LE where both operands are
// known-int at compile time).
func emitBaselineFeedbackFixedAt(asm *jit.Assembler, pc int, fieldOff int, expectedFB uint16, suffix string) {
	fbSkipLabel := nextLabel("fb_skip_" + suffix)
	fbSetLabel := nextLabel("fb_set_" + suffix)

	asm.LDR(jit.X5, mRegCtx, execCtxOffBaselineFeedbackPtr)
	asm.CBZ(jit.X5, fbSkipLabel)

	fbOff := pc*4 + fieldOff
	if fbOff < 4096 {
		asm.LDRB(jit.X6, jit.X5, fbOff)
		asm.CMPimm(jit.X6, expectedFB)
		asm.BCond(jit.CondEQ, fbSkipLabel)
		asm.CMPimm(jit.X6, 7) // FBAny
		asm.BCond(jit.CondEQ, fbSkipLabel)
		asm.CBZ(jit.X6, fbSetLabel)
		// Different type → FBAny
		asm.MOVimm16(jit.X6, 7)
		asm.STRB(jit.X6, jit.X5, fbOff)
		asm.B(fbSkipLabel)
		asm.Label(fbSetLabel)
		asm.MOVimm16(jit.X6, expectedFB)
		asm.STRB(jit.X6, jit.X5, fbOff)
	} else {
		asm.LoadImm64(jit.X6, int64(fbOff))
		asm.ADDreg(jit.X5, jit.X5, jit.X6)
		asm.LDRB(jit.X6, jit.X5, 0)
		asm.CMPimm(jit.X6, expectedFB)
		asm.BCond(jit.CondEQ, fbSkipLabel)
		asm.CMPimm(jit.X6, 7) // FBAny
		asm.BCond(jit.CondEQ, fbSkipLabel)
		asm.CBZ(jit.X6, fbSetLabel)
		asm.MOVimm16(jit.X6, 7)
		asm.STRB(jit.X6, jit.X5, 0)
		asm.B(fbSkipLabel)
		asm.Label(fbSetLabel)
		asm.MOVimm16(jit.X6, expectedFB)
		asm.STRB(jit.X6, jit.X5, 0)
	}
	asm.Label(fbSkipLabel)
}

// emitBaselineFeedbackDenseMatrix records whether a GETTABLE receiver had a
// DenseMatrix descriptor. It mirrors TableKeyFeedback.ObserveDenseMatrix using
// only scratch registers X5-X7 and leaves tblPtrReg untouched.
func emitBaselineFeedbackDenseMatrix(asm *jit.Assembler, pc int, tblPtrReg jit.Reg, suffix string) {
	fbSkipLabel := nextLabel("fb_dm_skip_" + suffix)
	fbSetLabel := nextLabel("fb_dm_set_" + suffix)
	fbObservedLabel := nextLabel("fb_dm_observed_" + suffix)

	asm.LDR(jit.X5, mRegCtx, execCtxOffBaselineTableKeyFeedbackPtr)
	asm.CBZ(jit.X5, fbSkipLabel)

	asm.MOVimm16(jit.X7, uint16(vm.FBDenseMatrixNo))
	asm.LDRW(jit.X6, tblPtrReg, jit.TableOffDMStride)
	asm.CBZ(jit.X6, fbObservedLabel)
	asm.MOVimm16(jit.X7, uint16(vm.FBDenseMatrixYes))
	asm.Label(fbObservedLabel)

	fbOff := pc*tableKeyFeedbackSize + tableKeyFeedbackDenseMatrixOff
	if fbOff < 4096 {
		asm.LDRB(jit.X6, jit.X5, fbOff)
		asm.CMPreg(jit.X6, jit.X7)
		asm.BCond(jit.CondEQ, fbSkipLabel)
		asm.CMPimm(jit.X6, uint16(vm.FBDenseMatrixPolymorphic))
		asm.BCond(jit.CondEQ, fbSkipLabel)
		asm.CBZ(jit.X6, fbSetLabel)
		asm.MOVimm16(jit.X6, uint16(vm.FBDenseMatrixPolymorphic))
		asm.STRB(jit.X6, jit.X5, fbOff)
		asm.B(fbSkipLabel)
		asm.Label(fbSetLabel)
		asm.STRB(jit.X7, jit.X5, fbOff)
	} else {
		asm.LoadImm64(jit.X6, int64(fbOff))
		asm.ADDreg(jit.X5, jit.X5, jit.X6)
		asm.LDRB(jit.X6, jit.X5, 0)
		asm.CMPreg(jit.X6, jit.X7)
		asm.BCond(jit.CondEQ, fbSkipLabel)
		asm.CMPimm(jit.X6, uint16(vm.FBDenseMatrixPolymorphic))
		asm.BCond(jit.CondEQ, fbSkipLabel)
		asm.CBZ(jit.X6, fbSetLabel)
		asm.MOVimm16(jit.X6, uint16(vm.FBDenseMatrixPolymorphic))
		asm.STRB(jit.X6, jit.X5, 0)
		asm.B(fbSkipLabel)
		asm.Label(fbSetLabel)
		asm.STRB(jit.X7, jit.X5, 0)
	}
	asm.Label(fbSkipLabel)
}

// emitBaselineFeedbackResultFromValue emits ARM64 code to extract the type from
// a NaN-boxed value and record it as Result feedback for TypeFeedback[pc]. The
// value must be in valReg. It distinguishes float, int, and table; all other
// types map to FBAny. Table feedback is important for mixed table-of-tables
// array access where the outer array stores row tables.
//
// Uses scratch registers X5, X6, X7. Does not clobber valReg.
func emitBaselineFeedbackResultFromValue(asm *jit.Assembler, pc int, valReg jit.Reg, suffix string) {
	emitBaselineFeedbackFromValueAt(asm, pc, valReg, 2, suffix)
}

// emitBaselineFeedbackFromValueAt is the generalized form of the above — R85
// Option 2. fieldOff selects the byte within TypeFeedback[pc] to update:
//
//	0 = Left, 1 = Right, 2 = Result, 3 = Kind.
//
// Left/Right are consumed by OP_EQ/LT/LE; Result by GETFIELD-style ops. The
// monotonic-observe semantics are identical across fields — Unobserved→concrete,
// same→nop, different→FBAny, FBAny→nop.
func emitBaselineFeedbackFromValueAt(asm *jit.Assembler, pc int, valReg jit.Reg, fieldOff int, suffix string) {
	fbSkipLabel := nextLabel("fb_val_skip_" + suffix)
	fbFloatLabel := nextLabel("fb_val_float_" + suffix)
	fbIntLabel := nextLabel("fb_val_int_" + suffix)
	fbPtrLabel := nextLabel("fb_val_ptr_" + suffix)
	fbTableLabel := nextLabel("fb_val_table_" + suffix)
	fbSetLabel := nextLabel("fb_val_set_" + suffix)
	fbUpdateLabel := nextLabel("fb_val_update_" + suffix)

	// Load feedback pointer.
	asm.LDR(jit.X5, mRegCtx, execCtxOffBaselineFeedbackPtr)
	asm.CBZ(jit.X5, fbSkipLabel)

	// Extract type from NaN-boxed value.
	// Tag = top 16 bits. Float: tag < 0xFFFC. Int: tag == 0xFFFE.
	// Pointers need the subtype check to distinguish table from string/function.
	asm.LSRimm(jit.X7, valReg, 48) // X7 = tag
	asm.MOVimm16(jit.X6, 0xFFFC)   // NB_TagNilShr48
	asm.CMPreg(jit.X7, jit.X6)
	asm.BCond(jit.CondLT, fbFloatLabel) // tag < 0xFFFC → float
	asm.MOVimm16(jit.X6, 0xFFFE)        // NB_TagIntShr48
	asm.CMPreg(jit.X7, jit.X6)
	asm.BCond(jit.CondEQ, fbIntLabel) // tag == 0xFFFE → int
	asm.MOVimm16(jit.X6, 0xFFFF)      // NB_TagPtrShr48
	asm.CMPreg(jit.X7, jit.X6)
	asm.BCond(jit.CondEQ, fbPtrLabel) // ptr → maybe table
	// Everything else (bool, nil) → FBAny.
	asm.MOVimm16(jit.X7, 7) // FBAny
	asm.B(fbUpdateLabel)
	asm.Label(fbPtrLabel)
	asm.LSRimm(jit.X6, valReg, uint8(jit.NB_PtrSubShift))
	asm.LoadImm64(jit.X7, 0xF)
	asm.ANDreg(jit.X6, jit.X6, jit.X7)
	asm.CMPimm(jit.X6, 0) // ptrSubTable
	asm.BCond(jit.CondEQ, fbTableLabel)
	asm.MOVimm16(jit.X7, 7) // non-table pointer → FBAny
	asm.B(fbUpdateLabel)
	asm.Label(fbFloatLabel)
	asm.MOVimm16(jit.X7, 2) // FBFloat
	asm.B(fbUpdateLabel)
	asm.Label(fbIntLabel)
	asm.MOVimm16(jit.X7, 1) // FBInt
	asm.B(fbUpdateLabel)
	asm.Label(fbTableLabel)
	asm.MOVimm16(jit.X7, 5) // FBTable

	// Monotonic update: X7 = observed type.
	asm.Label(fbUpdateLabel)
	fbOff := pc*4 + fieldOff // TypeFeedback[pc] byte offset
	if fbOff < 4096 {
		asm.LDRB(jit.X6, jit.X5, fbOff) // X6 = current value
		asm.CMPreg(jit.X6, jit.X7)
		asm.BCond(jit.CondEQ, fbSkipLabel) // same type → skip
		asm.CMPimm(jit.X6, 7)              // FBAny?
		asm.BCond(jit.CondEQ, fbSkipLabel) // already megamorphic → skip
		asm.CBZ(jit.X6, fbSetLabel)        // Unobserved → set
		// Different type → FBAny
		asm.MOVimm16(jit.X6, 7)
		asm.STRB(jit.X6, jit.X5, fbOff)
		asm.B(fbSkipLabel)
		asm.Label(fbSetLabel)
		asm.STRB(jit.X7, jit.X5, fbOff) // store observed type
	} else {
		asm.LoadImm64(jit.X6, int64(fbOff))
		asm.ADDreg(jit.X5, jit.X5, jit.X6)
		asm.LDRB(jit.X6, jit.X5, 0)
		asm.CMPreg(jit.X6, jit.X7)
		asm.BCond(jit.CondEQ, fbSkipLabel)
		asm.CMPimm(jit.X6, 7)
		asm.BCond(jit.CondEQ, fbSkipLabel)
		asm.CBZ(jit.X6, fbSetLabel)
		asm.MOVimm16(jit.X6, 7)
		asm.STRB(jit.X6, jit.X5, 0)
		asm.B(fbSkipLabel)
		asm.Label(fbSetLabel)
		asm.STRB(jit.X7, jit.X5, 0)
	}
	asm.Label(fbSkipLabel)
}

// emitBaselineFeedbackKind emits ARM64 code to record the array kind
// in the TypeFeedback[pc].Kind field. Uses monotonic observe logic:
// Unobserved->concrete kind, same->nop, different->Polymorphic, Polymorphic->nop.
// expectedKind is the FBKind* constant (1=Mixed, 2=Int, 3=Float, 4=Bool).
func emitBaselineFeedbackKind(asm *jit.Assembler, pc int, expectedKind uint16, suffix string) {
	fbSkipLabel := nextLabel("fbk_skip_" + suffix)
	fbSetLabel := nextLabel("fbk_set_" + suffix)

	asm.LDR(jit.X5, mRegCtx, execCtxOffBaselineFeedbackPtr)
	asm.CBZ(jit.X5, fbSkipLabel)

	fbKindOff := pc*4 + 3 // TypeFeedback[pc].Kind offset
	if fbKindOff < 4096 {
		asm.LDRB(jit.X6, jit.X5, fbKindOff)
		asm.CMPimm(jit.X6, expectedKind)
		asm.BCond(jit.CondEQ, fbSkipLabel) // same kind -> skip
		asm.CMPimm(jit.X6, 0xFF)           // FBKindPolymorphic
		asm.BCond(jit.CondEQ, fbSkipLabel) // already poly -> skip
		asm.CBZ(jit.X6, fbSetLabel)        // unobserved -> set
		// different kind -> polymorphic
		asm.MOVimm16(jit.X6, 0xFF)
		asm.STRB(jit.X6, jit.X5, fbKindOff)
		asm.B(fbSkipLabel)
		asm.Label(fbSetLabel)
		asm.MOVimm16(jit.X6, expectedKind)
		asm.STRB(jit.X6, jit.X5, fbKindOff)
	} else {
		asm.LoadImm64(jit.X6, int64(fbKindOff))
		asm.ADDreg(jit.X5, jit.X5, jit.X6)
		asm.LDRB(jit.X6, jit.X5, 0)
		asm.CMPimm(jit.X6, expectedKind)
		asm.BCond(jit.CondEQ, fbSkipLabel)
		asm.CMPimm(jit.X6, 0xFF)
		asm.BCond(jit.CondEQ, fbSkipLabel)
		asm.CBZ(jit.X6, fbSetLabel)
		asm.MOVimm16(jit.X6, 0xFF)
		asm.STRB(jit.X6, jit.X5, 0)
		asm.B(fbSkipLabel)
		asm.Label(fbSetLabel)
		asm.MOVimm16(jit.X6, expectedKind)
		asm.STRB(jit.X6, jit.X5, 0)
	}
	asm.Label(fbSkipLabel)
}
