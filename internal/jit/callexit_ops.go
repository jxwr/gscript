//go:build darwin && arm64

package jit

import (
	"fmt"
	"math"
	"strings"

	rt "github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// CallExitResult holds the outcome of executing a single call-exit opcode.
type CallExitResult struct {
	NextPC    int        // bytecode PC to resume at (usually pc+1, comparisons may yield pc+2)
	NewTop    int        // new Top value; -1 means unchanged
}

// ExecuteCallExitOp executes a single call-exit opcode in Go.
// This is the shared core used by both the method JIT (handleCallExit) and the
// trace JIT (handleTraceCallExit), as well as executeCompiledCalleeDepth.
//
// Parameters:
//   - code:      the proto's bytecode slice
//   - constants: the proto's constant pool
//   - regs:      the register array (written in place)
//   - base:      register window base offset
//   - pc:        the bytecode PC of the instruction to execute
//   - top:       the current Top value (used for B=0 variable-arg calls)
//   - callFn:    callback to execute a function call (OP_CALL slow path); may be nil
//   - globals:   accessor for GETGLOBAL/SETGLOBAL; may be nil
//
// Returns a CallExitResult and an error. On error, the result is undefined.
func ExecuteCallExitOp(
	code []uint32,
	constants []rt.Value,
	regs []rt.Value,
	base int,
	pc int,
	top int,
	callFn CallHandler,
	globals GlobalsAccessor,
) (CallExitResult, error) {
	if pc < 0 || pc >= len(code) {
		return CallExitResult{}, fmt.Errorf("jit: call-exit PC %d out of range", pc)
	}
	nextPC := pc + 1

	inst := code[pc]
	op := vm.DecodeOp(inst)

	switch op {
	case vm.OP_GETGLOBAL:
		if globals == nil {
			return CallExitResult{}, fmt.Errorf("jit: no globals accessor for GETGLOBAL")
		}
		a := vm.DecodeA(inst)
		bx := vm.DecodeBx(inst)
		name := constants[bx].Str()
		val := globals.GetGlobal(name)
		regs[base+a] = val
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	case vm.OP_SETGLOBAL:
		if globals == nil {
			return CallExitResult{}, fmt.Errorf("jit: no globals accessor for SETGLOBAL")
		}
		a := vm.DecodeA(inst)
		bx := vm.DecodeBx(inst)
		name := constants[bx].Str()
		globals.SetGlobal(name, regs[base+a])
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	case vm.OP_CALL:
		if callFn == nil {
			return CallExitResult{}, fmt.Errorf("jit: no call handler for OP_CALL")
		}
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)

		// Resolve nArgs: B=0 means variable args (from previous call's return).
		nArgs := b - 1
		if b == 0 {
			if top > 0 {
				nArgs = top - (a + 1)
				if nArgs < 0 {
					nArgs = 0
				}
			} else {
				return CallExitResult{}, fmt.Errorf("jit: variable args (B=0) without Top")
			}
		}

		// Resolve nResults: C=0 means variable returns.
		nResults := c - 1
		variableResults := c == 0

		fnVal := regs[base+a]
		args := make([]rt.Value, nArgs)
		for i := 0; i < nArgs; i++ {
			args[i] = regs[base+a+1+i]
		}

		callResults, err := callFn(fnVal, args)
		if err != nil {
			return CallExitResult{}, err
		}

		// Place results in registers.
		newTop := -1
		if variableResults {
			// C=0: store all results starting at R(A).
			for i, v := range callResults {
				regs[base+a+i] = v
			}
			newTop = a + len(callResults)
		} else {
			for i := 0; i < nResults; i++ {
				if i < len(callResults) {
					regs[base+a+i] = callResults[i]
				} else {
					regs[base+a+i] = rt.NilValue()
				}
			}
		}

		return CallExitResult{NextPC: nextPC, NewTop: newTop}, nil

	case vm.OP_GETTABLE:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		tableVal := regs[base+b]
		key := resolveRK(cidx, regs, base, constants)
		if !tableVal.IsTable() {
			return CallExitResult{}, fmt.Errorf("jit: GETTABLE on non-table")
		}
		tbl := tableVal.Table()
		if tbl.GetMetatable() != nil {
			return CallExitResult{}, fmt.Errorf("jit: GETTABLE metatable not supported")
		}
		regs[base+a] = tbl.RawGet(key)
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	case vm.OP_SETTABLE:
		a := vm.DecodeA(inst)
		bidx := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		tableVal := regs[base+a]
		key := resolveRK(bidx, regs, base, constants)
		val := resolveRK(cidx, regs, base, constants)
		if !tableVal.IsTable() {
			return CallExitResult{}, fmt.Errorf("jit: SETTABLE on non-table")
		}
		tbl := tableVal.Table()
		if tbl.GetMetatable() != nil {
			return CallExitResult{}, fmt.Errorf("jit: SETTABLE metatable not supported")
		}
		tbl.RawSet(key, val)
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	case vm.OP_GETFIELD:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		tableVal := regs[base+b]
		key := constants[c]
		if !tableVal.IsTable() {
			return CallExitResult{}, fmt.Errorf("jit: GETFIELD on non-table")
		}
		tbl := tableVal.Table()
		if tbl.GetMetatable() != nil {
			return CallExitResult{}, fmt.Errorf("jit: GETFIELD metatable not supported")
		}
		regs[base+a] = tbl.RawGet(key)
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	case vm.OP_SETFIELD:
		a := vm.DecodeA(inst)
		bidx := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		tableVal := regs[base+a]
		key := constants[bidx]
		val := resolveRK(cidx, regs, base, constants)
		if !tableVal.IsTable() {
			return CallExitResult{}, fmt.Errorf("jit: SETFIELD on non-table")
		}
		tbl := tableVal.Table()
		if tbl.GetMetatable() != nil {
			return CallExitResult{}, fmt.Errorf("jit: SETFIELD metatable not supported")
		}
		tbl.RawSet(key, val)
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	case vm.OP_NEWTABLE:
		a := vm.DecodeA(inst)
		regs[base+a] = rt.TableValue(rt.NewTable())
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	case vm.OP_SETLIST:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		tbl := regs[base+a].Table()
		offset := (c - 1) * 50
		for i := 1; i <= b; i++ {
			tbl.RawSet(rt.IntValue(int64(offset+i)), regs[base+a+i])
		}
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	case vm.OP_LEN:
		a := vm.DecodeA(inst)
		bv := regs[base+vm.DecodeB(inst)]
		if bv.IsString() {
			regs[base+a] = rt.IntValue(int64(len(bv.Str())))
		} else if bv.IsTable() {
			tbl := bv.Table()
			if tbl.GetMetatable() != nil {
				return CallExitResult{}, fmt.Errorf("jit: LEN metatable not supported")
			}
			regs[base+a] = rt.IntValue(int64(tbl.Len()))
		} else {
			return CallExitResult{}, fmt.Errorf("jit: LEN on non-table/string")
		}
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	case vm.OP_CONCAT:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		var sb strings.Builder
		for i := b; i <= c; i++ {
			sb.WriteString(regs[base+i].String())
		}
		regs[base+a] = rt.StringValue(sb.String())
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	case vm.OP_MOD:
		a := vm.DecodeA(inst)
		bidx := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		bv := resolveRK(bidx, regs, base, constants)
		cv := resolveRK(cidx, regs, base, constants)
		if bv.IsInt() && cv.IsInt() {
			bi := cv.Int()
			if bi == 0 {
				return CallExitResult{}, fmt.Errorf("attempt to perform 'n%%0'")
			}
			r := bv.Int() % bi
			if r != 0 && (r^bi) < 0 {
				r += bi
			}
			regs[base+a] = rt.IntValue(r)
		} else if bv.IsNumber() && cv.IsNumber() {
			bf := cv.Number()
			if bf == 0 {
				return CallExitResult{}, fmt.Errorf("attempt to perform 'n%%0'")
			}
			r := math.Mod(bv.Number(), bf)
			if r != 0 && (r < 0) != (bf < 0) {
				r += bf
			}
			regs[base+a] = rt.FloatValue(r)
		} else {
			return CallExitResult{}, fmt.Errorf("jit: MOD metatable not supported")
		}
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	case vm.OP_DIV:
		a := vm.DecodeA(inst)
		bidx := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		bv := resolveRK(bidx, regs, base, constants)
		cv := resolveRK(cidx, regs, base, constants)
		bn := bv.Number()
		cn := cv.Number()
		if cn == 0 {
			return CallExitResult{}, fmt.Errorf("attempt to divide by zero")
		}
		regs[base+a] = rt.FloatValue(bn / cn)
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	case vm.OP_SELF:
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		obj := regs[base+b]
		key := resolveRK(cidx, regs, base, constants)
		regs[base+a+1] = obj // R(A+1) = R(B)
		if !obj.IsTable() {
			return CallExitResult{}, fmt.Errorf("jit: SELF on non-table")
		}
		tbl := obj.Table()
		if tbl.GetMetatable() != nil {
			return CallExitResult{}, fmt.Errorf("jit: SELF metatable not supported")
		}
		regs[base+a] = tbl.RawGet(key) // R(A) = R(B)[RK(C)]
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	case vm.OP_EQ:
		aFlag := vm.DecodeA(inst)
		bidx := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		bv := resolveRK(bidx, regs, base, constants)
		cv := resolveRK(cidx, regs, base, constants)
		equal := bv.Equal(cv)
		if equal != (aFlag != 0) {
			nextPC = pc + 2 // skip the JMP
		}
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	case vm.OP_LT:
		aFlag := vm.DecodeA(inst)
		bidx := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		bv := resolveRK(bidx, regs, base, constants)
		cv := resolveRK(cidx, regs, base, constants)
		less, ok := bv.LessThan(cv)
		if !ok {
			return CallExitResult{}, fmt.Errorf("jit: LT on incomparable types")
		}
		if less != (aFlag != 0) {
			nextPC = pc + 2
		}
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	case vm.OP_LE:
		aFlag := vm.DecodeA(inst)
		bidx := vm.DecodeB(inst)
		cidx := vm.DecodeC(inst)
		bv := resolveRK(bidx, regs, base, constants)
		cv := resolveRK(cidx, regs, base, constants)
		// a <= b  <=>  !(b < a)
		less, ok := cv.LessThan(bv)
		if !ok {
			return CallExitResult{}, fmt.Errorf("jit: LE on incomparable types")
		}
		lessEq := !less
		if lessEq != (aFlag != 0) {
			nextPC = pc + 2
		}
		return CallExitResult{NextPC: nextPC, NewTop: -1}, nil

	default:
		return CallExitResult{}, fmt.Errorf("jit: unsupported call-exit opcode %s", vm.OpName(op))
	}
}
