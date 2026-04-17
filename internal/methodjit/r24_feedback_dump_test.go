//go:build darwin && arm64

// r24_feedback_dump_test.go — R24 diagnostic.
//
// Runs a short production-style driver of matmul-like and nbody-like
// workloads, then dumps the FeedbackVector for the hot proto: per-PC
// opcode + Kind + Left/Right/Result. Answers the R23 question:
// "at production-run feedback-collection time, what does the
// interpreter actually see at each GetTable/GetField site?"

package methodjit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// dumpFeedback walks the proto's bytecode, printing feedback for ops
// that the method JIT cares about (GETTABLE, SETTABLE, GETFIELD,
// SETFIELD, ADD, SUB, MUL, MOD). Returns a formatted string.
func dumpFeedback(t *testing.T, name string, proto *vm.FuncProto) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "=== %s ===\n", name)
	if proto.Feedback == nil {
		fmt.Fprintf(&sb, "(no feedback collected)\n")
		return sb.String()
	}
	for pc := 0; pc < len(proto.Code); pc++ {
		op := vm.DecodeOp(proto.Code[pc])
		switch op {
		case vm.OP_GETTABLE, vm.OP_SETTABLE, vm.OP_GETFIELD, vm.OP_SETFIELD,
			vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD:
			if pc >= len(proto.Feedback) {
				continue
			}
			fb := proto.Feedback[pc]
			fmt.Fprintf(&sb, "  pc=%d %s L=%s R=%s Res=%s Kind=%s\n",
				pc, vm.OpName(op),
				fbName(fb.Left), fbName(fb.Right), fbName(fb.Result),
				kindName(fb.Kind))
		}
	}
	return sb.String()
}

func fbName(f vm.FeedbackType) string {
	switch f {
	case vm.FBUnobserved:
		return "unobs"
	case vm.FBInt:
		return "int"
	case vm.FBFloat:
		return "float"
	case vm.FBString:
		return "str"
	case vm.FBBool:
		return "bool"
	case vm.FBTable:
		return "tbl"
	case vm.FBFunction:
		return "fn"
	case vm.FBAny:
		return "ANY"
	default:
		return fmt.Sprintf("?%d", f)
	}
}

func kindName(k uint8) string {
	switch k {
	case vm.FBKindUnobserved:
		return "unobs"
	case vm.FBKindMixed:
		return "Mixed"
	case vm.FBKindInt:
		return "Int"
	case vm.FBKindFloat:
		return "Float"
	case vm.FBKindBool:
		return "Bool"
	case vm.FBKindPolymorphic:
		return "POLY"
	default:
		return fmt.Sprintf("?%d", k)
	}
}

// TestFeedback_MatmulInnerLoop runs a matmul-shape workload and dumps
// the feedback for the inner proto. Goal: verify whether the inner
// GetTables have FBKindFloat (my R23 assumption) or FBKindPolymorphic
// or unobserved.
func TestFeedback_MatmulInnerLoop(t *testing.T) {
	src := `
func matmul(a, b, n) {
    c := {}
    for i := 0; i < n; i++ {
        row := {}
        ai := a[i]
        for j := 0; j < n; j++ {
            sum := 0.0
            for k := 0; k < n; k++ {
                sum = sum + ai[k] * b[k][j]
            }
            row[j] = sum
        }
        c[i] = row
    }
    return c
}

func matgen(n) {
    m := {}
    for i := 0; i < n; i++ {
        row := {}
        for j := 0; j < n; j++ {
            row[j] = (i * n + j + 1.0) / (n * n)
        }
        m[i] = row
    }
    return m
}

N := 30
a := matgen(N)
b := matgen(N)
c := matmul(a, b, N)
c = matmul(a, b, N)
`
	proto := compileProto(t, src)
	// Execute WITH the same proto so feedback is populated on THIS instance.
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("runtime error: %v", err)
	}

	// Dump top-level protos. matmul is one of them.
	for _, p := range proto.Protos {
		if p.Name == "matmul" {
			t.Log(dumpFeedback(t, p.Name, p))
			return
		}
	}
	t.Log("matmul proto not found in top-level; searching nested...")
}

// TestFeedback_NbodyEnergy similarly for nbody's energy function.
func TestFeedback_NbodyEnergy(t *testing.T) {
	src := `
bodies := {
    {x: 0.0, y: 0.0, z: 0.0, vx: 0.1, vy: 0.2, vz: 0.3, mass: 1.0},
    {x: 1.0, y: 1.0, z: 1.0, vx: 0.1, vy: 0.2, vz: 0.3, mass: 0.5},
    {x: 2.0, y: 2.0, z: 2.0, vx: 0.1, vy: 0.2, vz: 0.3, mass: 0.3},
}
func energy() {
    e := 0.0
    for i := 1; i <= 3; i++ {
        bi := bodies[i]
        e = e + 0.5 * bi.mass * (bi.vx * bi.vx + bi.vy * bi.vy + bi.vz * bi.vz)
    }
    return e
}
result := energy()
result = energy()
result = energy()
`
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("runtime error: %v", err)
	}

	for _, p := range proto.Protos {
		if p.Name == "energy" {
			t.Log(dumpFeedback(t, p.Name, p))
			return
		}
	}
}
