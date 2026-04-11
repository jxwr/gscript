//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestQuicksortBytecode(t *testing.T) {
	src := `
func quicksort(arr, lo, hi) {
    if lo >= hi { return }
    pivot := arr[hi]
    i := lo
    for j := lo; j < hi; j++ {
        if arr[j] <= pivot {
            tmp := arr[i]
            arr[i] = arr[j]
            arr[j] = tmp
            i = i + 1
        }
    }
    tmp := arr[i]
    arr[i] = arr[hi]
    arr[hi] = tmp
    quicksort(arr, lo, i - 1)
    quicksort(arr, i + 1, hi)
}
`
	top := compileTop(t, src)
	qs := findProtoByName(top, "quicksort")
	if qs == nil {
		t.Fatal("quicksort not found")
	}
	
	t.Logf("quicksort: %d params, maxstack=%d, %d instructions", qs.NumParams, qs.MaxStack, len(qs.Code))
	for i, inst := range qs.Code {
		op := vm.DecodeOp(inst)
		a := vm.DecodeA(inst)
		b := vm.DecodeB(inst)
		c := vm.DecodeC(inst)
		bx := vm.DecodeBx(inst)
		t.Logf("  [%02d] %-15s A=%d B=%d C=%d Bx=%d", i, opNameStr(op), a, b, c, bx)
	}
	
	t.Logf("\nConstants:")
	for i, k := range qs.Constants {
		t.Logf("  K[%d] = %v (%T)", i, k, k)
	}
}

func opNameStr(op vm.Opcode) string {
	switch op {
	case vm.OP_GETGLOBAL: return "GETGLOBAL"
	case vm.OP_SETGLOBAL: return "SETGLOBAL"
	case vm.OP_GETTABLE: return "GETTABLE"
	case vm.OP_SETTABLE: return "SETTABLE"
	case vm.OP_GETFIELD: return "GETFIELD"
	case vm.OP_SETFIELD: return "SETFIELD"
	case vm.OP_ADD: return "ADD"
	case vm.OP_SUB: return "SUB"
	case vm.OP_MUL: return "MUL"
	case vm.OP_DIV: return "DIV"
	case vm.OP_LT: return "LT"
	case vm.OP_LE: return "LE"
	case vm.OP_EQ: return "EQ"
	case vm.OP_JMP: return "JMP"
	case vm.OP_CALL: return "CALL"
	case vm.OP_RETURN: return "RETURN"
	case vm.OP_FORPREP: return "FORPREP"
	case vm.OP_FORLOOP: return "FORLOOP"
	case vm.OP_LOADINT: return "LOADINT"
	case vm.OP_LOADK: return "LOADK"
	case vm.OP_MOVE: return "MOVE"
	case vm.OP_TEST: return "TEST"
	case vm.OP_TESTSET: return "TESTSET"
	default: return fmt.Sprintf("OP_%d", op)
	}
}
