package methodjit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestDiag_NbodyProduction compiles nbody advance() through TieringManager
// (Tier 1 runs first to collect feedback, then Tier 2 compiles with feedback).
// It dumps the Tier 2 IR and reports:
//   - How many GuardType nodes exist (feedback-driven type specialization)
//   - How many arithmetic ops are typed (OpAddFloat, OpSubFloat, etc.) vs generic (OpAdd, OpSub)
//   - How many GetField results are typed vs :any
//
// This determines whether the production bottleneck is (A) typed but slow field access
// or (B) genuinely untyped arithmetic due to missing feedback.
func TestDiag_NbodyProduction(t *testing.T) {
	src := `
bodies := {
    {x: 0.0, y: 0.0, z: 0.0, vx: 0.01720209895, vy: 0.0, vz: 0.0, mass: 39.47841760435743},
    {x: 4.841431, y: -1.160320, z: -0.103622, vx: 0.00166, vy: 0.00769, vz: -0.0000690, mass: 0.00095},
    {x: 8.343367, y: 4.124799, z: -0.403523, vx: -0.00276, vy: 0.00499, vz: 0.0000230, mass: 0.000286},
}

func advance(dt) {
    n := #bodies
    for i := 1; i <= n; i++ {
        bi := bodies[i]
        for j := i + 1; j <= n; j++ {
            bj := bodies[j]
            dx := bi.x - bj.x
            dy := bi.y - bj.y
            dz := bi.z - bj.z
            dsq := dx * dx + dy * dy + dz * dz
            dist := math.sqrt(dsq)
            mag := dt / (dsq * dist)
            bi.vx = bi.vx - dx * bj.mass * mag
            bi.vy = bi.vy - dy * bj.mass * mag
            bi.vz = bi.vz - dz * bj.mass * mag
            bj.vx = bj.vx + dx * bi.mass * mag
            bj.vy = bj.vy + dy * bi.mass * mag
            bj.vz = bj.vz + dz * bi.mass * mag
        }
    }
    for i := 1; i <= n; i++ {
        b := bodies[i]
        b.x = b.x + dt * b.vx
        b.y = b.y + dt * b.vy
        b.z = b.z + dt * b.vz
    }
}

for iter := 1; iter <= 10; iter++ {
    advance(0.01)
}
`

	// Step 1: Run through TieringManager to collect Tier 1 feedback
	proto := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	tm := NewTieringManager()
	v.SetMethodJIT(tm)
	_, err := v.Execute(proto)
	if err != nil {
		t.Fatalf("runtime error: %v", err)
	}

	// Step 2: Find the advance proto
	var advanceProto *vm.FuncProto
	for _, child := range proto.Protos {
		if child.Name == "advance" {
			advanceProto = child
			break
		}
	}
	if advanceProto == nil {
		t.Fatal("could not find advance proto")
	}

	// Step 3: Report feedback state
	t.Logf("=== FEEDBACK STATE ===")
	t.Logf("advance CallCount: %d", advanceProto.CallCount)
	t.Logf("advance Tier2Promoted: %v", advanceProto.Tier2Promoted)

	if advanceProto.Feedback == nil {
		t.Logf("WARNING: Feedback is nil — Tier 1 did NOT collect type feedback for advance()")
	} else {
		floatCount, intCount, tableCount, anyCount, unobserved := 0, 0, 0, 0, 0
		for pc, fb := range advanceProto.Feedback {
			if fb.Result == vm.FBFloat {
				floatCount++
			} else if fb.Result == vm.FBInt {
				intCount++
			} else if fb.Result == vm.FBTable {
				tableCount++
			} else if fb.Result == vm.FBAny {
				anyCount++
			} else {
				unobserved++
			}
			if fb.Result != vm.FBUnobserved {
				op := vm.DecodeOp(advanceProto.Code[pc])
				t.Logf("  PC %2d (%v): Left=%v Right=%v Result=%v", pc, op, fb.Left, fb.Right, fb.Result)
			}
		}
		t.Logf("Feedback summary: Float=%d Int=%d Table=%d Any=%d Unobserved=%d",
			floatCount, intCount, tableCount, anyCount, unobserved)
	}

	// Step 4: Build Tier 2 IR with feedback
	advanceProto.EnsureFeedback()
	fn := BuildGraph(advanceProto)
	errs := Validate(fn)
	if len(errs) > 0 {
		t.Logf("Validation errors: %v", errs)
	}

	// Step 5: Run pipeline (but don't compile — just optimize IR)
	fn2, passes, pipeErr := RunTier2Pipeline(fn, nil)
	if pipeErr != nil {
		t.Logf("Pipeline error: %v (passes completed: %v)", pipeErr, passes)
		// Continue with pre-pipeline fn for analysis
		fn2 = fn
	}

	// Step 6: Analyze IR
	t.Logf("\n=== IR ANALYSIS ===")
	irStr := Print(fn2)

	// Count ops
	guardTypeCount := 0
	genericArithCount := 0
	typedArithCount := 0
	getFieldCount := 0
	getFieldTyped := 0
	totalOps := 0

	genericArithOps := map[Op]bool{OpAdd: true, OpSub: true, OpMul: true, OpDiv: true, OpMod: true}
	typedArithOps := map[Op]bool{
		OpAddInt: true, OpSubInt: true, OpMulInt: true, OpModInt: true, OpDivIntExact: true,
		OpAddFloat: true, OpSubFloat: true, OpMulFloat: true, OpDivFloat: true,
		OpSqrt: true,
	}

	for _, block := range fn2.Blocks {
		for _, instr := range block.Instrs {
			totalOps++
			if instr.Op == OpGuardType {
				guardTypeCount++
			}
			if genericArithOps[instr.Op] {
				genericArithCount++
			}
			if typedArithOps[instr.Op] {
				typedArithCount++
			}
			if instr.Op == OpGetField {
				getFieldCount++
				if instr.Type != TypeAny {
					getFieldTyped++
				}
			}
		}
	}

	t.Logf("Total IR ops: %d", totalOps)
	t.Logf("GuardType nodes: %d", guardTypeCount)
	t.Logf("Generic arithmetic (OpAdd/Sub/Mul/Div): %d", genericArithCount)
	t.Logf("Typed arithmetic (OpAddFloat/SubFloat/MulFloat/etc): %d", typedArithCount)
	t.Logf("GetField total: %d (typed: %d, any: %d)", getFieldCount, getFieldTyped, getFieldCount-getFieldTyped)

	// Determine scenario
	t.Logf("\n=== DIAGNOSIS ===")
	if typedArithCount > genericArithCount {
		t.Logf("SCENARIO A: Feedback IS working — %d typed vs %d generic arithmetic ops", typedArithCount, genericArithCount)
		t.Logf("Bottleneck is likely field access overhead, not untyped arithmetic.")
	} else if genericArithCount > 0 && typedArithCount == 0 {
		t.Logf("SCENARIO B: Feedback NOT reaching Tier 2 — ALL %d arithmetic ops are generic", genericArithCount)
		t.Logf("Need to fix feedback pipeline for nbody advance().")
	} else {
		t.Logf("MIXED: %d typed, %d generic arithmetic ops", typedArithCount, genericArithCount)
		t.Logf("Partially typed. Investigate which ops remain generic.")
	}

	// Print first few lines of IR for visual inspection
	lines := strings.Split(irStr, "\n")
	maxLines := 80
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	t.Logf("\n=== IR (first %d lines) ===\n%s", maxLines, strings.Join(lines, "\n"))

	// Print full IR to help debug
	fmt.Println("\n=== FULL IR ===")
	fmt.Println(irStr)
}
