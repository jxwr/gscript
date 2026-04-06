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
	"github.com/gscript/gscript/internal/vm"
)

// emitBaselineGetGlobal emits native ARM64 for OP_GETGLOBAL: R(A) = globals[K(Bx)]
// Uses a per-PC value cache stored in BaselineFunc.GlobalValCache with a
// generation-based invalidation scheme. The cache is populated by the Go slow
// path (handleGetGlobal) on first miss. SetGlobal increments the generation
// counter, causing all caches to miss on next access.
//
// Fast path (~8 instructions):
//   1. Version check: engine.globalCacheGen == bf.CachedGlobalGen
//   2. Load GlobalCache pointer from ExecContext
//   3. Load cached value at GlobalValCache[pc]
//   4. If non-zero (cached), store to R(A) and continue
// Slow path: standard exit-resume to handleGetGlobal in Go.
func emitBaselineGetGlobal(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	bx := vm.DecodeBx(inst)

	slowLabel := nextLabel("getglobal_slow")
	doneLabel := nextLabel("getglobal_done")

	// Version check: engine.globalCacheGen == ctx.BaselineGlobalCachedGen
	asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineGlobalGenPtr)
	asm.CBZ(jit.X0, slowLabel) // no gen pointer = no cache
	asm.LDR(jit.X1, jit.X0, 0)                                       // X1 = current gen
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
	asm.STR(jit.X1, mRegRegs, slotOff(a))
	asm.B(doneLabel)

	// Slow path: exit-resume.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_GETGLOBAL, pc, a, bx, 0)

	asm.Label(doneLabel)
}

// emitBaselineGetField emits native ARM64 for OP_GETFIELD: R(A) = R(B).field[C]
// Uses runtime inline cache from proto.FieldCache[pc].
// Falls back to exit-resume if cache miss or non-table.
func emitBaselineGetField(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	c := vm.DecodeC(inst) // constant index for field name

	slowLabel := nextLabel("getfield_slow")
	doneLabel := nextLabel("getfield_done")

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
	asm.CBZ(jit.X2, slowLabel) // shapeID==0 means not cached

	// Load entry.FieldIdx (int at offset 0).
	asm.LDR(jit.X3, jit.X0, jit.FieldCacheEntryOffFieldIdx) // X3 = fieldIdx

	// Load table value from R(B).
	asm.LDR(jit.X0, mRegRegs, slotOff(b))

	// Check it's a table pointer (tag = 0xFFFF, sub = 0).
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X4, slowLabel)

	// Extract raw *Table pointer (44-bit payload).
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, slowLabel)

	// Shape guard: table.shapeID must match cached shapeID.
	asm.LDRW(jit.X1, jit.X0, jit.TableOffShapeID) // X1 = table.shapeID
	asm.CMPreg(jit.X1, jit.X2) // compare with cached shapeID
	asm.BCond(jit.CondNE, slowLabel)

	// Bounds check: fieldIdx < len(svals)
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvalsLen) // X1 = svals.len
	asm.CMPreg(jit.X3, jit.X1) // fieldIdx < svals.len?
	asm.BCond(jit.CondGE, slowLabel) // unsigned >= means out of bounds

	// Direct field access: svals[fieldIdx]
	// LDRreg uses [Xn + Xm, LSL #3] which already scales by 8 (= ValueSize),
	// so X3 must hold the raw fieldIdx (not pre-multiplied).
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvals) // X1 = svals data pointer
	asm.LDRreg(jit.X0, jit.X1, jit.X3) // X0 = svals[fieldIdx]

	// Store result to R(A).
	asm.STR(jit.X0, mRegRegs, slotOff(a))
	asm.B(doneLabel)

	// Slow path: exit-resume.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_GETFIELD, pc, a, b, c)

	asm.Label(doneLabel)
}

// emitBaselineSetField emits native ARM64 for OP_SETFIELD: R(A).field[B] = RK(C)
// Uses runtime inline cache from proto.FieldCache[pc].
func emitBaselineSetField(asm *jit.Assembler, inst uint32, pc int) {
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

	// Load entry.ShapeID.
	asm.LDRW(jit.X2, jit.X0, jit.FieldCacheEntryOffShapeID)
	asm.CBZ(jit.X2, slowLabel)

	// Load entry.FieldIdx.
	asm.LDR(jit.X3, jit.X0, jit.FieldCacheEntryOffFieldIdx) // X3 = fieldIdx

	// Load table value from R(A).
	asm.LDR(jit.X0, mRegRegs, slotOff(a))

	// Check table pointer.
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X1, jit.X4, slowLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, slowLabel)

	// Shape guard.
	asm.LDRW(jit.X1, jit.X0, jit.TableOffShapeID)
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondNE, slowLabel)

	// Bounds check: fieldIdx < len(svals)
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvalsLen)
	asm.CMPreg(jit.X3, jit.X1)
	asm.BCond(jit.CondGE, slowLabel)

	// Load value to store: RK(C).
	loadRK(asm, jit.X4, c) // X4 = value

	// Direct field store: svals[fieldIdx] = value.
	// STRreg uses [Xn + Xm, LSL #3] which already scales by 8 (= ValueSize),
	// so X3 must hold the raw fieldIdx (not pre-multiplied).
	asm.LDR(jit.X1, jit.X0, jit.TableOffSvals) // X1 = svals data pointer
	asm.STRreg(jit.X4, jit.X1, jit.X3) // svals[fieldIdx] = value

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
func emitBaselineGetTable(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)

	slowLabel := nextLabel("gettable_slow")
	doneLabel := nextLabel("gettable_done")
	intArrayLabel := nextLabel("gettable_intarr")
	floatArrayLabel := nextLabel("gettable_floatarr")
	boolArrayLabel := nextLabel("gettable_boolarr")

	// Load table value from R(B).
	asm.LDR(jit.X0, mRegRegs, slotOff(b))

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
	asm.BCond(jit.CondNE, slowLabel) // not int -> slow

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
	asm.LDRreg(jit.X0, jit.X2, jit.X1)         // X0 = array[key] (NaN-boxed Value)
	asm.STR(jit.X0, mRegRegs, slotOff(a))
	asm.B(doneLabel)

	// --- ArrayInt fast path ---
	asm.Label(intArrayLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffIntArrayLen) // X2 = intArray.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, slowLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffIntArray)    // X2 = intArray data pointer
	asm.LDRreg(jit.X0, jit.X2, jit.X1)               // X0 = intArray[key] (raw int64)
	// NaN-box the int64: UBFX + ORR with pinned tag register.
	jit.EmitBoxIntFast(asm, jit.X0, jit.X0, mRegTagInt)
	asm.STR(jit.X0, mRegRegs, slotOff(a))
	asm.B(doneLabel)

	// --- ArrayFloat fast path ---
	asm.Label(floatArrayLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffFloatArrayLen) // X2 = floatArray.len
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, slowLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffFloatArray) // X2 = floatArray data pointer
	asm.LDRreg(jit.X0, jit.X2, jit.X1)              // X0 = raw float64 bits = floatArray[key]
	// Float64 bits ARE the NaN-boxed value — no conversion needed!
	asm.STR(jit.X0, mRegRegs, slotOff(a))
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
	asm.CBZ(jit.X3, boolNilLabel)         // byte == 0 → nil
	asm.CMPimm(jit.X3, 1)
	asm.BCond(jit.CondEQ, boolFalseLabel) // byte == 1 → false
	// byte == 2 → true: NaN-boxed true = 0xFFFD000000000001
	asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool|1))
	asm.STR(jit.X0, mRegRegs, slotOff(a))
	asm.B(doneLabel)
	asm.Label(boolFalseLabel)
	// NaN-boxed false = 0xFFFD000000000000
	asm.LoadImm64(jit.X0, nb64(jit.NB_TagBool))
	asm.STR(jit.X0, mRegRegs, slotOff(a))
	asm.B(doneLabel)
	asm.Label(boolNilLabel)
	// NaN-boxed nil = 0xFFFC000000000000
	asm.LoadImm64(jit.X0, nb64(jit.NB_ValNil))
	asm.STR(jit.X0, mRegRegs, slotOff(a))
	asm.B(doneLabel)

	// Slow path: exit-resume.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_GETTABLE, pc, a, b, cidx)

	asm.Label(doneLabel)
}

// emitBaselineSetTable emits native ARM64 for OP_SETTABLE: R(A)[RK(B)] = RK(C)
// Fast path for integer keys with array bounds check.
// Supports both ArrayMixed ([]Value) and ArrayInt ([]int64) array kinds.
func emitBaselineSetTable(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	bidx := vm.DecodeB(inst) // RK(B) = key
	cidx := vm.DecodeC(inst) // RK(C) = value

	slowLabel := nextLabel("settable_slow")
	doneLabel := nextLabel("settable_done")
	intArrayLabel := nextLabel("settable_intarr")

	// Load table value from R(A).
	asm.LDR(jit.X0, mRegRegs, slotOff(a))

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
	asm.BCond(jit.CondNE, slowLabel) // not int -> slow

	// Extract integer key.
	asm.SBFX(jit.X1, jit.X1, 0, 48) // X1 = signed int key

	// Check key >= 0 (shared by both array kinds).
	asm.CMPimm(jit.X1, 0)
	asm.BCond(jit.CondLT, slowLabel)

	// Dispatch on arrayKind: 0=Mixed, 1=Int, else=slow.
	asm.LDRB(jit.X2, jit.X0, jit.TableOffArrayKind)
	asm.CMPimm(jit.X2, jit.AKInt)
	asm.BCond(jit.CondEQ, intArrayLabel)
	asm.CBNZ(jit.X2, slowLabel) // not Mixed (0) and not Int (1) -> slow

	// --- ArrayMixed fast path ---
	asm.LDR(jit.X2, jit.X0, jit.TableOffArrayLen)
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, slowLabel)
	loadRK(asm, jit.X4, cidx) // X4 = value (NaN-boxed)
	asm.LDR(jit.X2, jit.X0, jit.TableOffArray)
	asm.STRreg(jit.X4, jit.X2, jit.X1) // array[key] = value
	asm.MOVimm16(jit.X5, 1)
	asm.STRB(jit.X5, jit.X0, jit.TableOffKeysDirty)
	asm.B(doneLabel)

	// --- ArrayInt fast path ---
	asm.Label(intArrayLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffIntArrayLen)
	asm.CMPreg(jit.X1, jit.X2)
	asm.BCond(jit.CondGE, slowLabel)
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
	asm.MOVimm16(jit.X5, 1)
	asm.STRB(jit.X5, jit.X0, jit.TableOffKeysDirty)
	asm.B(doneLabel)

	// Slow path: exit-resume.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_SETTABLE, pc, a, bidx, cidx)

	asm.Label(doneLabel)
}

// emitBaselineLen emits ARM64 for OP_LEN: R(A) = #R(B)
// Table.Length() requires a backwards scan for trailing nils which is not
// efficient in JIT code. Always falls back to exit-resume for correctness.
func emitBaselineLen(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	emitBaselineOpExitCommon(asm, vm.OP_LEN, pc, a, b, 0)
}

// emitBaselineSelf emits native ARM64 for OP_SELF: R(A+1) = R(B); R(A) = R(B)[RK(C)]
// This is R(A+1) = obj, R(A) = obj.method -- used for method calls.
func emitBaselineSelf(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)
	cidx := vm.DecodeC(inst)

	slowLabel := nextLabel("self_slow")
	doneLabel := nextLabel("self_done")

	// R(A+1) = R(B) (copy the object reference).
	asm.LDR(jit.X0, mRegRegs, slotOff(b))
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
	asm.BCond(jit.CondNE, slowLabel)

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
	asm.STR(jit.X0, mRegRegs, slotOff(a))
	asm.B(doneLabel)

	// Slow path: exit-resume.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_SELF, pc, a, b, cidx)

	asm.Label(doneLabel)
}

// emitBaselineGetUpval emits native ARM64 for OP_GETUPVAL: R(A) = Upvalues[B].ref
// Uses the Closure pointer stored in ExecContext.
func emitBaselineGetUpval(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)

	slowLabel := nextLabel("getupval_slow")
	doneLabel := nextLabel("getupval_done")

	// Load Closure pointer from ExecContext.
	asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.CBZ(jit.X0, slowLabel) // no closure pointer

	// Closure.Upvalues is a []*Upvalue slice at offset 8.
	// Load slice data pointer.
	asm.LDR(jit.X1, jit.X0, 8) // X1 = Upvalues data ptr ([]* element ptr)

	// Load Upvalue pointer: Upvalues[B] (each element is 8 bytes = *Upvalue).
	asm.LDR(jit.X2, jit.X1, b*8) // X2 = *Upvalue

	asm.CBZ(jit.X2, slowLabel)

	// Upvalue.ref is at offset 0 (*runtime.Value pointer).
	asm.LDR(jit.X3, jit.X2, 0) // X3 = ref ptr
	asm.CBZ(jit.X3, slowLabel)

	// Load the value: *ref.
	asm.LDR(jit.X0, jit.X3, 0) // X0 = *ref (the actual value)

	// Store to R(A).
	asm.STR(jit.X0, mRegRegs, slotOff(a))
	asm.B(doneLabel)

	// Slow path: exit-resume.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_GETUPVAL, pc, a, b, 0)

	asm.Label(doneLabel)
}

// emitBaselineSetUpval emits native ARM64 for OP_SETUPVAL: Upvalues[B].ref = R(A)
func emitBaselineSetUpval(asm *jit.Assembler, inst uint32, pc int) {
	a := vm.DecodeA(inst)
	b := vm.DecodeB(inst)

	slowLabel := nextLabel("setupval_slow")
	doneLabel := nextLabel("setupval_done")

	// Load Closure pointer from ExecContext.
	asm.LDR(jit.X0, mRegCtx, execCtxOffBaselineClosurePtr)
	asm.CBZ(jit.X0, slowLabel)

	// Load Upvalues slice data pointer.
	asm.LDR(jit.X1, jit.X0, 8) // Closure.Upvalues data ptr

	// Load Upvalue[B] pointer.
	asm.LDR(jit.X2, jit.X1, b*8) // *Upvalue
	asm.CBZ(jit.X2, slowLabel)

	// Upvalue.ref at offset 0.
	asm.LDR(jit.X3, jit.X2, 0) // ref ptr
	asm.CBZ(jit.X3, slowLabel)

	// Load value from R(A).
	asm.LDR(jit.X4, mRegRegs, slotOff(a))

	// Store: *ref = value.
	asm.STR(jit.X4, jit.X3, 0)

	asm.B(doneLabel)

	// Slow path: exit-resume.
	asm.Label(slowLabel)
	emitBaselineOpExitCommon(asm, vm.OP_SETUPVAL, pc, a, b, 0)

	asm.Label(doneLabel)
}
