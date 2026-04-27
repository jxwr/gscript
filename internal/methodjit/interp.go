// interp.go implements an interpreter for the Method JIT's CFG SSA IR.
// This is the correctness oracle: Interpret(BuildGraph(proto), args) must
// produce identical results to VM.Execute(proto, args) for all inputs.
// It is NOT performance-sensitive — clarity and correctness over speed.

package methodjit

import (
	"fmt"
	"math"
	"strings"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// maxInterpDepth limits recursive Interpret calls (for OpCall).
const maxInterpDepth = 200

// Interpret executes the CFG SSA IR of a function with the given arguments.
// Returns the function's return values, matching VM.Execute semantics exactly.
func Interpret(fn *Function, args []runtime.Value) ([]runtime.Value, error) {
	return interpretImpl(fn, args, 0)
}

// interpretImpl is the internal recursive implementation with depth tracking.
func interpretImpl(fn *Function, args []runtime.Value, depth int) ([]runtime.Value, error) {
	if depth > maxInterpDepth {
		return nil, fmt.Errorf("IR interpreter: stack overflow (depth %d)", depth)
	}

	s := &interpState{
		fn:     fn,
		values: make(map[int]runtime.Value),
		depth:  depth,
	}

	// Load function arguments into parameter LoadSlot values.
	// The entry block starts with LoadSlot instructions for each parameter.
	s.loadParams(args)

	// Start executing from the entry block.
	return s.run()
}

// interpState holds the mutable state for one IR interpretation.
type interpState struct {
	fn     *Function
	values map[int]runtime.Value // value ID → runtime value
	depth  int
	prev   *Block // previous block (for phi resolution)
}

// loadParams initializes parameter values from the LoadSlot instructions
// in the entry block.
func (s *interpState) loadParams(args []runtime.Value) {
	entry := s.fn.Entry
	paramIdx := 0
	for _, instr := range entry.Instrs {
		if instr.Op == OpLoadSlot && paramIdx < s.fn.Proto.NumParams {
			if paramIdx < len(args) {
				s.values[instr.ID] = args[paramIdx]
			} else {
				s.values[instr.ID] = runtime.NilValue()
			}
			paramIdx++
		}
	}
}

// run executes the IR starting from the entry block.
func (s *interpState) run() ([]runtime.Value, error) {
	block := s.fn.Entry

	for {
		for _, instr := range block.Instrs {
			result, done, err := s.execInstr(instr, block)
			if err != nil {
				return nil, err
			}
			if done {
				// OpReturn: result is the return values.
				return result, nil
			}
		}

		// The last instruction is a terminator; it sets up the next block.
		last := block.Instrs[len(block.Instrs)-1]
		nextBlock, err := s.resolveTerminator(last, block)
		if err != nil {
			return nil, err
		}
		if nextBlock == nil {
			// Should not happen if IR is well-formed.
			return nil, fmt.Errorf("IR interpreter: fell off end of block B%d", block.ID)
		}

		s.prev = block
		block = nextBlock

		// Resolve phi nodes at the new block entry.
		s.resolvePhis(block)
	}
}

// resolvePhis evaluates phi instructions at block entry using the predecessor.
func (s *interpState) resolvePhis(block *Block) {
	for _, instr := range block.Instrs {
		if instr.Op != OpPhi {
			break // Phis are always at the beginning.
		}
		// Find which predecessor we came from.
		predIdx := -1
		for i, pred := range block.Preds {
			if pred == s.prev {
				predIdx = i
				break
			}
		}
		if predIdx >= 0 && predIdx < len(instr.Args) {
			s.values[instr.ID] = s.val(instr.Args[predIdx])
		} else {
			// Fallback: use first arg or nil.
			if len(instr.Args) > 0 {
				s.values[instr.ID] = s.val(instr.Args[0])
			} else {
				s.values[instr.ID] = runtime.NilValue()
			}
		}
	}
}

// val looks up the runtime.Value for an SSA value.
func (s *interpState) val(v *Value) runtime.Value {
	if v == nil {
		return runtime.NilValue()
	}
	if rv, ok := s.values[v.ID]; ok {
		return rv
	}
	// If the value isn't computed yet, it might be a constant that's defined
	// in a different block. Try to evaluate it.
	if v.Def != nil {
		rv, _, _ := s.execInstr(v.Def, v.Def.Block)
		return rv[0] // constants always return one value
	}
	return runtime.NilValue()
}

// execInstr executes a single IR instruction.
// Returns (resultValues, isDone, error).
// isDone is true only for OpReturn.
// For non-return instructions, the result is stored in s.values[instr.ID].
func (s *interpState) execInstr(instr *Instr, block *Block) ([]runtime.Value, bool, error) {
	switch instr.Op {
	// ---------- Constants ----------
	case OpConstInt:
		s.values[instr.ID] = runtime.IntValue(instr.Aux)

	case OpConstFloat:
		s.values[instr.ID] = runtime.FloatValue(math.Float64frombits(uint64(instr.Aux)))

	case OpConstBool:
		s.values[instr.ID] = runtime.BoolValue(instr.Aux != 0)

	case OpConstNil:
		s.values[instr.ID] = runtime.NilValue()

	case OpConstString:
		idx := int(instr.Aux)
		if idx >= 0 && idx < len(s.fn.Proto.Constants) {
			s.values[instr.ID] = s.fn.Proto.Constants[idx]
		} else {
			s.values[instr.ID] = runtime.StringValue("")
		}

	// ---------- Slot access ----------
	case OpLoadSlot:
		// LoadSlot for non-parameter slots (e.g., uninitialized registers).
		// If already set by loadParams, don't overwrite.
		if _, ok := s.values[instr.ID]; !ok {
			s.values[instr.ID] = runtime.NilValue()
		}

	case OpStoreSlot:
		// StoreSlot writes a value. In SSA, this isn't used much.
		if len(instr.Args) > 0 {
			s.values[instr.ID] = s.val(instr.Args[0])
		}

	// ---------- Arithmetic (type-generic) ----------
	case OpAdd:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		var dst runtime.Value
		if !runtime.AddNums(&dst, &a, &b) {
			return nil, false, fmt.Errorf("IR interpreter: cannot add %s and %s", a.TypeName(), b.TypeName())
		}
		s.values[instr.ID] = dst

	case OpSub:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		var dst runtime.Value
		if !runtime.SubNums(&dst, &a, &b) {
			return nil, false, fmt.Errorf("IR interpreter: cannot sub %s and %s", a.TypeName(), b.TypeName())
		}
		s.values[instr.ID] = dst

	case OpMul:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		var dst runtime.Value
		if !runtime.MulNums(&dst, &a, &b) {
			return nil, false, fmt.Errorf("IR interpreter: cannot mul %s and %s", a.TypeName(), b.TypeName())
		}
		s.values[instr.ID] = dst

	case OpDiv:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		var dst runtime.Value
		if !runtime.DivNums(&dst, &a, &b) {
			return nil, false, fmt.Errorf("IR interpreter: cannot div %s and %s", a.TypeName(), b.TypeName())
		}
		s.values[instr.ID] = dst

	case OpMod:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		if a.IsNumber() && b.IsNumber() {
			af, bf := a.Number(), b.Number()
			if a.IsInt() && b.IsInt() {
				ai, bi := a.Int(), b.Int()
				if bi == 0 {
					return nil, false, fmt.Errorf("IR interpreter: modulo by zero")
				}
				s.values[instr.ID] = runtime.IntValue(ai % bi)
			} else {
				s.values[instr.ID] = runtime.FloatValue(math.Mod(af, bf))
			}
		} else {
			return nil, false, fmt.Errorf("IR interpreter: cannot mod %s and %s", a.TypeName(), b.TypeName())
		}

	case OpPow:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		if a.IsNumber() && b.IsNumber() {
			s.values[instr.ID] = runtime.FloatValue(math.Pow(a.Number(), b.Number()))
		} else {
			return nil, false, fmt.Errorf("IR interpreter: cannot pow %s and %s", a.TypeName(), b.TypeName())
		}

	case OpUnm:
		a := s.val(instr.Args[0])
		if a.IsInt() {
			s.values[instr.ID] = runtime.IntValue(-a.Int())
		} else if a.IsFloat() {
			s.values[instr.ID] = runtime.FloatValue(-a.Float())
		} else {
			return nil, false, fmt.Errorf("IR interpreter: cannot negate %s", a.TypeName())
		}

	case OpNot:
		a := s.val(instr.Args[0])
		s.values[instr.ID] = runtime.BoolValue(!a.Truthy())

	case OpLen:
		a := s.val(instr.Args[0])
		if a.IsString() {
			s.values[instr.ID] = runtime.IntValue(int64(len(a.Str())))
		} else if a.IsTable() {
			s.values[instr.ID] = runtime.IntValue(int64(a.Table().Length()))
		} else {
			return nil, false, fmt.Errorf("IR interpreter: cannot get length of %s", a.TypeName())
		}

	// ---------- Type-specialized arithmetic ----------
	case OpAddInt:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		s.values[instr.ID] = runtime.IntValue(a.Int() + b.Int())

	case OpSubInt:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		s.values[instr.ID] = runtime.IntValue(a.Int() - b.Int())

	case OpMulInt:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		s.values[instr.ID] = runtime.IntValue(a.Int() * b.Int())

	case OpModInt:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		s.values[instr.ID] = runtime.IntValue(a.Int() % b.Int())

	case OpDivIntExact:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		bi := b.Int()
		if bi == 0 || a.Int()%bi != 0 {
			return nil, false, fmt.Errorf("IR interpreter: non-exact integer division")
		}
		s.values[instr.ID] = runtime.IntValue(a.Int() / bi)

	case OpNegInt:
		a := s.val(instr.Args[0])
		s.values[instr.ID] = runtime.IntValue(-a.Int())

	case OpAddFloat:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		s.values[instr.ID] = runtime.FloatValue(a.Number() + b.Number())

	case OpSubFloat:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		s.values[instr.ID] = runtime.FloatValue(a.Number() - b.Number())

	case OpMulFloat:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		s.values[instr.ID] = runtime.FloatValue(a.Number() * b.Number())

	case OpDivFloat:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		s.values[instr.ID] = runtime.FloatValue(a.Number() / b.Number())

	case OpNegFloat:
		a := s.val(instr.Args[0])
		s.values[instr.ID] = runtime.FloatValue(-a.Number())

	case OpSqrt:
		a := s.val(instr.Args[0])
		s.values[instr.ID] = runtime.FloatValue(math.Sqrt(a.Number()))

	case OpFMA:
		// R47: interp fallback. OpFMA(a, b, c) → c + a*b.
		a := s.val(instr.Args[0]).Number()
		b := s.val(instr.Args[1]).Number()
		c := s.val(instr.Args[2]).Number()
		s.values[instr.ID] = runtime.FloatValue(c + a*b)

	case OpMatrixGetF:
		// R43 Phase 2 interp fallback: delegate to the builtin via Go.
		mv := s.val(instr.Args[0])
		iv := s.val(instr.Args[1])
		jv := s.val(instr.Args[2])
		if !mv.IsTable() {
			return nil, false, fmt.Errorf("OpMatrixGetF: arg 0 not a table")
		}
		m := mv.Table()
		if m.DMStride() <= 0 {
			return nil, false, fmt.Errorf("OpMatrixGetF: not a DenseMatrix")
		}
		stride := int(m.DMStride())
		i := int(iv.Int())
		j := int(jv.Int())
		backing := runtime.DenseMatrixBackingByRows(m)
		if backing == nil {
			return nil, false, fmt.Errorf("OpMatrixGetF: invalid backing")
		}
		s.values[instr.ID] = runtime.FloatValue(backing[i*stride+j])

	case OpMatrixSetF:
		mv := s.val(instr.Args[0])
		iv := s.val(instr.Args[1])
		jv := s.val(instr.Args[2])
		vv := s.val(instr.Args[3])
		if !mv.IsTable() {
			return nil, false, fmt.Errorf("OpMatrixSetF: arg 0 not a table")
		}
		m := mv.Table()
		if m.DMStride() <= 0 {
			return nil, false, fmt.Errorf("OpMatrixSetF: not a DenseMatrix")
		}
		stride := int(m.DMStride())
		i := int(iv.Int())
		j := int(jv.Int())
		row := m.RawGetInt(int64(i)).Table()
		row.RawSetInt(int64(j), vv)
		_ = stride

	case OpMatrixFlat:
		// R45: interp tunnels the Table as the "flat" SSA value; the
		// subsequent LoadFAt/StoreFAt instructions access via RawGetInt,
		// which still resolves correctly. JIT uses raw pointer for perf;
		// interp doesn't need that path for correctness.
		mv := s.val(instr.Args[0])
		if !mv.IsTable() {
			return nil, false, fmt.Errorf("OpMatrixFlat: arg 0 not a table")
		}
		if mv.Table().DMStride() <= 0 {
			return nil, false, fmt.Errorf("OpMatrixFlat: not a DenseMatrix")
		}
		s.values[instr.ID] = mv

	case OpMatrixStride:
		mv := s.val(instr.Args[0])
		if !mv.IsTable() {
			return nil, false, fmt.Errorf("OpMatrixStride: arg 0 not a table")
		}
		s.values[instr.ID] = runtime.IntValue(int64(mv.Table().DMStride()))

	case OpMatrixLoadFAt:
		mv := s.val(instr.Args[0])
		iv := s.val(instr.Args[2])
		jv := s.val(instr.Args[3])
		if !mv.IsTable() {
			return nil, false, fmt.Errorf("OpMatrixLoadFAt: arg 0 not a table")
		}
		m := mv.Table()
		i := int(iv.Int())
		j := int(jv.Int())
		row := m.RawGetInt(int64(i)).Table()
		s.values[instr.ID] = row.RawGetInt(int64(j))

	case OpMatrixStoreFAt:
		mv := s.val(instr.Args[0])
		iv := s.val(instr.Args[2])
		jv := s.val(instr.Args[3])
		vv := s.val(instr.Args[4])
		if !mv.IsTable() {
			return nil, false, fmt.Errorf("OpMatrixStoreFAt: arg 0 not a table")
		}
		m := mv.Table()
		i := int(iv.Int())
		j := int(jv.Int())
		row := m.RawGetInt(int64(i)).Table()
		row.RawSetInt(int64(j), vv)

	case OpMatrixRowPtr:
		// R46: interp tunnels (m, i) as an encoded row reference — we
		// pass the matrix + row index through so LoadFRow/StoreFRow can
		// still resolve via RawGetInt. JIT uses raw pointer for perf;
		// interp uses the functional path.
		mv := s.val(instr.Args[0])
		iv := s.val(instr.Args[2])
		if !mv.IsTable() {
			return nil, false, fmt.Errorf("OpMatrixRowPtr: arg 0 not a table")
		}
		m := mv.Table()
		i := int(iv.Int())
		row := m.RawGetInt(int64(i))
		s.values[instr.ID] = row

	case OpMatrixLoadFRow:
		rv := s.val(instr.Args[0])
		jv := s.val(instr.Args[1])
		if !rv.IsTable() {
			return nil, false, fmt.Errorf("OpMatrixLoadFRow: arg 0 not a row table")
		}
		j := int(jv.Int())
		s.values[instr.ID] = rv.Table().RawGetInt(int64(j))

	case OpMatrixStoreFRow:
		rv := s.val(instr.Args[0])
		jv := s.val(instr.Args[1])
		vv := s.val(instr.Args[2])
		if !rv.IsTable() {
			return nil, false, fmt.Errorf("OpMatrixStoreFRow: arg 0 not a row table")
		}
		j := int(jv.Int())
		rv.Table().RawSetInt(int64(j), vv)

	// ---------- Comparison (type-generic) ----------
	case OpEq:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		s.values[instr.ID] = runtime.BoolValue(a.Equal(b))

	case OpLt:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		lt, ok := a.LessThan(b)
		if !ok {
			return nil, false, fmt.Errorf("IR interpreter: cannot compare %s < %s", a.TypeName(), b.TypeName())
		}
		s.values[instr.ID] = runtime.BoolValue(lt)

	case OpLe:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		// a <= b is !(b < a)
		lt, ok := b.LessThan(a)
		if !ok {
			return nil, false, fmt.Errorf("IR interpreter: cannot compare %s <= %s", a.TypeName(), b.TypeName())
		}
		s.values[instr.ID] = runtime.BoolValue(!lt)

	// ---------- Type-specialized comparison ----------
	case OpEqInt:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		s.values[instr.ID] = runtime.BoolValue(a.Int() == b.Int())

	case OpLtInt:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		s.values[instr.ID] = runtime.BoolValue(a.Int() < b.Int())

	case OpLeInt:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		s.values[instr.ID] = runtime.BoolValue(a.Int() <= b.Int())

	case OpLtFloat:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		s.values[instr.ID] = runtime.BoolValue(a.Number() < b.Number())

	case OpLeFloat:
		a, b := s.val(instr.Args[0]), s.val(instr.Args[1])
		s.values[instr.ID] = runtime.BoolValue(a.Number() <= b.Number())

	// ---------- String ----------
	case OpConcat:
		var sb strings.Builder
		for _, arg := range instr.Args {
			sb.WriteString(s.val(arg).String())
		}
		s.values[instr.ID] = runtime.StringValue(sb.String())

	// ---------- Table operations ----------
	case OpNewTable:
		arrHint := int(instr.Aux)
		hashHint, arrayKind := unpackNewTableAux2(instr.Aux2)
		s.values[instr.ID] = runtime.TableValue(runtime.NewTableSizedKind(arrHint, hashHint, arrayKind))

	case OpGetTable:
		tbl := s.val(instr.Args[0])
		key := s.val(instr.Args[1])
		if tbl.IsTable() {
			s.values[instr.ID] = tbl.Table().RawGet(key)
		} else {
			s.values[instr.ID] = runtime.NilValue()
		}

	case OpSetTable:
		tbl := s.val(instr.Args[0])
		key := s.val(instr.Args[1])
		val := s.val(instr.Args[2])
		if tbl.IsTable() {
			tbl.Table().RawSet(key, val)
		}

	case OpGetField:
		tbl := s.val(instr.Args[0])
		idx := int(instr.Aux)
		if tbl.IsTable() && idx >= 0 && idx < len(s.fn.Proto.Constants) {
			key := s.fn.Proto.Constants[idx]
			s.values[instr.ID] = tbl.Table().RawGet(key)
		} else {
			s.values[instr.ID] = runtime.NilValue()
		}

	case OpGetFieldNumToFloat:
		tbl := s.val(instr.Args[0])
		idx := int(instr.Aux)
		if tbl.IsTable() && idx >= 0 && idx < len(s.fn.Proto.Constants) {
			key := s.fn.Proto.Constants[idx]
			val := tbl.Table().RawGet(key)
			if !val.IsNumber() {
				return nil, false, fmt.Errorf("IR interpreter: cannot convert %s field to float", val.TypeName())
			}
			s.values[instr.ID] = runtime.FloatValue(val.Number())
		} else {
			return nil, false, fmt.Errorf("IR interpreter: cannot convert missing field to float")
		}

	case OpSetField:
		tbl := s.val(instr.Args[0])
		val := s.val(instr.Args[1])
		idx := int(instr.Aux)
		if tbl.IsTable() && idx >= 0 && idx < len(s.fn.Proto.Constants) {
			key := s.fn.Proto.Constants[idx]
			tbl.Table().RawSet(key, val)
		}

	case OpSetList:
		tbl := s.val(instr.Args[0])
		if tbl.IsTable() {
			t := tbl.Table()
			for i := 1; i < len(instr.Args); i++ {
				v := s.val(instr.Args[i])
				t.RawSetInt(int64(i), v)
			}
		}

	case OpAppend:
		tbl := s.val(instr.Args[0])
		val := s.val(instr.Args[1])
		if tbl.IsTable() {
			t := tbl.Table()
			t.RawSetInt(int64(t.Length()+1), val)
		}

	// ---------- Global access ----------
	case OpGetGlobal:
		idx := int(instr.Aux)
		if idx >= 0 && idx < len(s.fn.Proto.Constants) {
			name := s.fn.Proto.Constants[idx].Str()
			// Look up global in the VM-like way. Since we don't have a VM
			// instance, we use a global lookup via the function context.
			s.values[instr.ID] = s.getGlobal(name)
		} else {
			s.values[instr.ID] = runtime.NilValue()
		}

	case OpSetGlobal:
		idx := int(instr.Aux)
		if idx >= 0 && idx < len(s.fn.Proto.Constants) && len(instr.Args) > 0 {
			// In the IR interpreter, setting globals is a no-op for now.
			// Full global support would need a shared state.
		}

	// ---------- Upvalue access ----------
	case OpGetUpval:
		// Upvalues aren't accessible without a closure context.
		s.values[instr.ID] = runtime.NilValue()

	case OpSetUpval:
		// No-op in IR interpreter.

	// ---------- Type operations ----------
	case OpBoxInt:
		a := s.val(instr.Args[0])
		s.values[instr.ID] = a // Already boxed in runtime.Value.

	case OpBoxFloat:
		a := s.val(instr.Args[0])
		s.values[instr.ID] = a

	case OpUnboxInt:
		a := s.val(instr.Args[0])
		s.values[instr.ID] = runtime.IntValue(a.Int())

	case OpUnboxFloat:
		a := s.val(instr.Args[0])
		s.values[instr.ID] = runtime.FloatValue(a.Number())

	case OpNumToFloat:
		a := s.val(instr.Args[0])
		if !a.IsNumber() {
			return nil, false, fmt.Errorf("IR interpreter: cannot convert %s to float", a.TypeName())
		}
		s.values[instr.ID] = runtime.FloatValue(a.Number())

	// ---------- Guards ----------
	case OpGuardType:
		s.values[instr.ID] = s.val(instr.Args[0])

	case OpGuardNonNil:
		s.values[instr.ID] = s.val(instr.Args[0])

	case OpGuardTruthy:
		a := s.val(instr.Args[0])
		s.values[instr.ID] = runtime.BoolValue(a.Truthy())

	// ---------- Control flow (terminators) ----------
	case OpJump, OpBranch, OpReturn:
		// Handled by resolveTerminator and the main loop.
		// OpReturn is handled below.
		if instr.Op == OpReturn {
			results := make([]runtime.Value, len(instr.Args))
			for i, arg := range instr.Args {
				results[i] = s.val(arg)
			}
			return results, true, nil
		}

	// ---------- Calls ----------
	case OpCall:
		result, err := s.execCall(instr)
		if err != nil {
			return nil, false, err
		}
		s.values[instr.ID] = result

	// ---------- Closure ----------
	case OpClosure:
		protoIdx := int(instr.Aux)
		if protoIdx >= 0 && protoIdx < len(s.fn.Proto.Protos) {
			childProto := s.fn.Proto.Protos[protoIdx]
			cl := &vm.Closure{Proto: childProto}
			s.values[instr.ID] = runtime.VMClosureFunctionValue(unsafe.Pointer(cl), cl)
		} else {
			s.values[instr.ID] = runtime.NilValue()
		}

	// ---------- Phi (resolved in resolvePhis) ----------
	case OpPhi:
		// Already handled at block entry. Skip.

	// ---------- No-op / placeholder ----------
	case OpNop:
		s.values[instr.ID] = runtime.NilValue()

	case OpClose:
		// No-op in IR interpreter.

	default:
		return nil, false, fmt.Errorf("IR interpreter: unhandled op %s", instr.Op)
	}

	return nil, false, nil
}

// resolveTerminator determines the next block based on the terminator instruction.
func (s *interpState) resolveTerminator(instr *Instr, block *Block) (*Block, error) {
	switch instr.Op {
	case OpJump:
		if len(block.Succs) > 0 {
			return block.Succs[0], nil
		}
		return nil, fmt.Errorf("IR interpreter: OpJump with no successors")

	case OpBranch:
		if len(instr.Args) == 0 || len(block.Succs) < 2 {
			return nil, fmt.Errorf("IR interpreter: OpBranch with insufficient args/succs")
		}
		cond := s.val(instr.Args[0])
		if cond.Truthy() {
			return block.Succs[0], nil
		}
		return block.Succs[1], nil

	case OpReturn:
		// Return is handled in execInstr; should not reach here.
		return nil, nil

	default:
		return nil, fmt.Errorf("IR interpreter: block B%d ends with non-terminator %s", block.ID, instr.Op)
	}
}

// execCall, callViaVM, and getGlobal are in interp_ops.go.
