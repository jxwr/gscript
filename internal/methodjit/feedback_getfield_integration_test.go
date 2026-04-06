// feedback_getfield_integration_test.go verifies the full GETFIELD feedback →
// GuardType → TypeSpecialize cascade end-to-end through the Tier 2 pipeline.
// Split from graph_builder_test.go to respect the 1000-line file size limit.

package methodjit

import (
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// TestFeedbackGuards_GetField_Integration verifies that feedback-typed guard
// insertion works end-to-end for GETFIELD (dot-notation field access): compile
// a function that accesses float fields via dot notation, execute it via the
// interpreter to collect real type feedback, then build the IR graph and verify:
//   - OpGuardType appears after OpGetField (from FBFloat feedback)
//   - TypeSpecialize cascades the float type, producing OpMulFloat/OpAddFloat
//   - The IR interpreter produces the correct result
func TestFeedbackGuards_GetField_Integration(t *testing.T) {
	// Source: a function that computes the squared magnitude of a 2D point
	// using dot-notation field access. The GETFIELD results will be float,
	// so the interpreter should record FBFloat feedback.
	src := `
func f(p) {
	return p.x * p.x + p.y * p.y
}
`
	// Step 1: Compile the full program and get the inner function proto.
	topProto := compileTop(t, src)
	if len(topProto.Protos) == 0 {
		t.Fatal("expected inner function proto")
	}
	innerProto := topProto.Protos[0]

	// Step 2: Initialize feedback and execute via VM to collect real feedback.
	innerProto.EnsureFeedback()

	globals := make(map[string]runtime.Value)
	v := vm.New(globals)
	defer v.Close()

	// Execute top-level to register function f in globals.
	if _, err := v.Execute(topProto); err != nil {
		t.Fatalf("VM execute top-level error: %v", err)
	}

	// Build a table with float fields: {x=3.5, y=4.2}.
	tbl := runtime.NewTable()
	tbl.RawSetString("x", runtime.FloatValue(3.5))
	tbl.RawSetString("y", runtime.FloatValue(4.2))

	// Call f(p) via the VM to collect type feedback.
	fnVal := v.GetGlobal("f")
	if fnVal.IsNil() {
		t.Fatal("function 'f' not found in globals after execution")
	}
	vmResult, err := v.CallValue(fnVal, []runtime.Value{
		runtime.TableValue(tbl),
	})
	if err != nil {
		t.Fatalf("VM call error: %v", err)
	}
	t.Logf("VM result: %v", vmResult)

	// Step 3: Verify that feedback was collected on GETFIELD instructions.
	getfieldPCs := []int{}
	for pc, inst := range innerProto.Code {
		if vm.DecodeOp(inst) == vm.OP_GETFIELD {
			getfieldPCs = append(getfieldPCs, pc)
		}
	}
	if len(getfieldPCs) == 0 {
		t.Fatal("no GETFIELD instruction found in inner proto bytecode")
	}
	for _, pc := range getfieldPCs {
		fb := innerProto.Feedback[pc]
		if fb.Result != vm.FBFloat {
			t.Errorf("GETFIELD at PC %d: expected FBFloat feedback, got %d", pc, fb.Result)
		}
	}

	// Step 4: Build the IR graph (feedback is now populated).
	fn := BuildGraph(innerProto)
	irBefore := Print(fn)
	t.Logf("IR before optimization:\n%s", irBefore)

	// Verify that OpGuardType appears after OpGetField in the IR.
	hasGuardAfterGetField := false
	for _, blk := range fn.Blocks {
		for i, instr := range blk.Instrs {
			if instr.Op == OpGetField && i+1 < len(blk.Instrs) {
				next := blk.Instrs[i+1]
				if next.Op == OpGuardType && len(next.Args) > 0 && next.Args[0].ID == instr.ID {
					hasGuardAfterGetField = true
					if next.Type != TypeFloat {
						t.Errorf("GuardType after GetField has Type=%v, want TypeFloat", next.Type)
					}
				}
			}
		}
	}
	if !hasGuardAfterGetField {
		t.Fatal("expected OpGuardType after OpGetField in IR (from FBFloat feedback), but none found")
	}

	// Verify the IR text contains "GuardType".
	if !strings.Contains(irBefore, "GuardType") {
		t.Error("IR text should contain 'GuardType'")
	}

	// Step 5: Run TypeSpecialize and verify float-specialized ops cascade.
	fnOpt, err := TypeSpecializePass(fn)
	if err != nil {
		t.Fatalf("TypeSpecializePass error: %v", err)
	}
	irAfter := Print(fnOpt)
	t.Logf("IR after TypeSpecialize:\n%s", irAfter)

	// After type specialization, the float type from GuardType should cascade
	// through the multiply and add, producing MulFloat and/or AddFloat.
	hasMulFloat := strings.Contains(irAfter, "MulFloat")
	hasAddFloat := strings.Contains(irAfter, "AddFloat")
	if !hasMulFloat && !hasAddFloat {
		t.Error("expected MulFloat or AddFloat in optimized IR after TypeSpecialize " +
			"(float type from GuardType should cascade through arithmetic)")
	}
	if hasMulFloat {
		t.Log("confirmed: MulFloat present in optimized IR")
	}
	if hasAddFloat {
		t.Log("confirmed: AddFloat present in optimized IR")
	}

	// Step 6: Verify correctness via IR interpreter on the optimized IR.
	// Run ConstProp + DCE for cleaner IR before interpreting.
	fnOpt, _ = ConstPropPass(fnOpt)
	fnOpt, _ = DCEPass(fnOpt)
	irFinal := Print(fnOpt)
	t.Logf("IR after ConstProp+DCE:\n%s", irFinal)

	args := []runtime.Value{runtime.TableValue(tbl)}
	irResult, irErr := Interpret(fnOpt, args)
	if irErr != nil {
		t.Fatalf("IR interpreter error: %v", irErr)
	}
	t.Logf("IR interpreter result: %v", irResult)

	// Compare: VM and IR interpreter should produce the same result.
	if len(vmResult) == 0 || len(irResult) == 0 {
		t.Fatalf("empty results: VM=%v, IR=%v", vmResult, irResult)
	}
	vmNum := vmResult[0].Number()
	irNum := irResult[0].Number()
	if vmNum != irNum {
		// Allow small epsilon for float comparison.
		diff := vmNum - irNum
		if diff < 0 {
			diff = -diff
		}
		if diff > 1e-6 {
			t.Errorf("result mismatch: VM=%.10f, IR=%.10f (diff=%.2e)", vmNum, irNum, diff)
		}
	}
	t.Logf("VM result=%.6f, IR result=%.6f -- match", vmNum, irNum)
}
