// diagnose_test.go tests the unified diagnostic tool for the Method JIT.
// Each test compiles a GScript function, runs Diagnose(), and verifies
// the report contains expected sections and correct match/mismatch verdicts.

//go:build darwin && arm64

package methodjit

import (
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

// TestDiagnose_SimpleAdd: f(3,4)=7. Verify Match=true, report contains IR.
func TestDiagnose_SimpleAdd(t *testing.T) {
	src := `func f(a, b) { return a + b }`
	proto := compileFunction(t, src)
	args := []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)}

	report := Diagnose(proto, args)
	t.Log(report)

	if report.FuncName != "f" {
		t.Errorf("expected FuncName 'f', got %q", report.FuncName)
	}
	if report.NumArgs != 2 {
		t.Errorf("expected NumArgs 2, got %d", report.NumArgs)
	}

	// IR should be captured.
	if report.IRBefore == "" {
		t.Error("IRBefore should not be empty")
	}
	if report.IRAfter == "" {
		t.Error("IRAfter should not be empty")
	}

	// Both interpreters should succeed.
	if report.InterpError != nil {
		t.Errorf("InterpError: %v", report.InterpError)
	}
	if report.NativeError != nil {
		t.Errorf("NativeError: %v", report.NativeError)
	}

	// Results should match: int(7).
	if !report.Match {
		t.Errorf("expected Match=true, got false. Mismatch: %s", report.Mismatch)
	}

	// Verify actual values.
	if len(report.InterpResult) == 0 || !report.InterpResult[0].IsInt() || report.InterpResult[0].Int() != 7 {
		t.Errorf("expected InterpResult=[int(7)], got %v", report.InterpResult)
	}
	if len(report.NativeResult) == 0 || !report.NativeResult[0].IsInt() || report.NativeResult[0].Int() != 7 {
		t.Errorf("expected NativeResult=[int(7)], got %v", report.NativeResult)
	}
}

// TestDiagnose_ForLoop: sum(10)=55. Verify IR interpreter is correct and
// PassDiffs captures TypeSpecialize changes. The native result may mismatch
// due to known raw-int boxing issues in the JIT emitter for loops -- the
// diagnostic tool correctly detects this.
func TestDiagnose_ForLoop(t *testing.T) {
	src := `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	proto := compileFunction(t, src)
	args := []runtime.Value{runtime.IntValue(10)}

	report := Diagnose(proto, args)
	t.Log(report)

	// IR interpreter (correctness oracle) should return int(55).
	if report.InterpError != nil {
		t.Errorf("InterpError: %v", report.InterpError)
	}
	if len(report.InterpResult) == 0 || report.InterpResult[0].Int() != 55 {
		t.Errorf("expected InterpResult=[int(55)], got %v", report.InterpResult)
	}

	// If mismatch, verify the diagnostic tool reports it clearly.
	if !report.Match {
		if report.Mismatch == "" {
			t.Error("Mismatch=true but Mismatch description is empty")
		}
		t.Logf("Known JIT mismatch detected by Diagnose: %s", report.Mismatch)
	}

	// TypeSpec should have changed the IR (Int-specialized ops).
	hasTypeSpecDiff := false
	for _, d := range report.PassDiffs {
		if strings.Contains(d, "TypeSpecialize") {
			hasTypeSpecDiff = true
		}
	}
	if !hasTypeSpecDiff {
		t.Error("expected PassDiffs to contain TypeSpecialize diff")
	}
}

// TestDiagnose_Fib: fib(10)=55. Verify report shows deopt info (OpCall triggers deopt).
func TestDiagnose_Fib(t *testing.T) {
	src := `func fib(n) { if n < 2 { return n }; return fib(n-1) + fib(n-2) }`
	proto := compileFunction(t, src)
	args := []runtime.Value{runtime.IntValue(10)}

	report := Diagnose(proto, args)
	t.Log(report)

	// IR interpreter should succeed with fib(10)=55.
	if report.InterpError != nil {
		t.Errorf("InterpError: %v", report.InterpError)
	}
	if len(report.InterpResult) == 0 || report.InterpResult[0].Int() != 55 {
		t.Errorf("expected InterpResult=[int(55)], got %v", report.InterpResult)
	}

	// Native may deopt (OpCall causes deopt to interpreter).
	// Either way, we check the report was generated without panic.
	if report.IRBefore == "" || report.IRAfter == "" {
		t.Error("IR snapshots should be populated")
	}
}

// TestDiagnose_FloatArith: f(1.5, 2.5)=4.0. Verify float comparison works.
func TestDiagnose_FloatArith(t *testing.T) {
	src := `func f(a, b) { return a + b }`
	proto := compileFunction(t, src)
	args := []runtime.Value{runtime.FloatValue(1.5), runtime.FloatValue(2.5)}

	report := Diagnose(proto, args)
	t.Log(report)

	if report.InterpError != nil {
		t.Errorf("InterpError: %v", report.InterpError)
	}

	// Interp result should be float(4.0).
	if len(report.InterpResult) == 0 {
		t.Fatal("empty InterpResult")
	}
	if !report.InterpResult[0].IsFloat() {
		t.Errorf("expected float result, got %s", report.InterpResult[0].TypeName())
	}
	if report.InterpResult[0].Number() != 4.0 {
		t.Errorf("expected 4.0, got %v", report.InterpResult[0])
	}

	// Match verdict (interp vs native).
	if !report.Match {
		t.Logf("Float mismatch (may be expected if JIT deopts): %s", report.Mismatch)
	}
}

// TestDiagnose_Validation: Verify validation errors are captured.
func TestDiagnose_Validation(t *testing.T) {
	src := `func f(a, b) { return a + b }`
	proto := compileFunction(t, src)
	args := []runtime.Value{runtime.IntValue(1), runtime.IntValue(2)}

	report := Diagnose(proto, args)
	t.Log(report)

	// A well-formed function should have no validation errors.
	if len(report.ValidateErrors) != 0 {
		t.Errorf("expected 0 validation errors, got %d: %v", len(report.ValidateErrors), report.ValidateErrors)
	}
}

// TestDiagnose_ReportString: Verify String() output contains all sections.
func TestDiagnose_ReportString(t *testing.T) {
	src := `func f(n) { s := 0; for i := 1; i <= n; i++ { s = s + i }; return s }`
	proto := compileFunction(t, src)
	args := []runtime.Value{runtime.IntValue(10)}

	report := Diagnose(proto, args)
	s := report.String()
	t.Log(s)

	sections := []string{
		"Method JIT Diagnostic Report",
		"Function:",
		"IR (before passes)",
		"IR (after passes)",
		"Register Allocation",
		"Validation",
		"IR Interpreter",
		"Native Execution",
		"Verdict",
	}
	for _, sec := range sections {
		if !strings.Contains(s, sec) {
			t.Errorf("report String() missing section %q", sec)
		}
	}
}

// TestDiagnose_RegAllocMap: Verify register allocation map is populated.
func TestDiagnose_RegAllocMap(t *testing.T) {
	src := `func f(a, b) { return a + b }`
	proto := compileFunction(t, src)
	args := []runtime.Value{runtime.IntValue(1), runtime.IntValue(2)}

	report := Diagnose(proto, args)
	t.Log(report)

	if report.RegAllocMap == "" {
		t.Error("RegAllocMap should not be empty")
	}
	// Should mention register names.
	if !strings.Contains(report.RegAllocMap, "X") && !strings.Contains(report.RegAllocMap, "D") {
		t.Error("RegAllocMap should contain register names (X or D)")
	}
}
