//go:build darwin && arm64

package methodjit

import (
	"testing"
	"github.com/gscript/gscript/internal/runtime"
)

// TestFPRCycle_SinglePixelEscape verifies that the FPR phi move cycle fix
// correctly handles the mandelbrot inner loop. This pixel (cr=-1.5, ci=-1.0)
// should escape after 2 iterations.
func TestFPRCycle_SinglePixelEscape(t *testing.T) {
	src := `func f() {
		zr := 0.0
		zi := 0.0
		cr := -1.5
		ci := -1.0
		escaped := false
		for iter := 0; iter < 50; iter++ {
			tr := zr * zr - zi * zi + cr
			ti := 2.0 * zr * zi + ci
			zr = tr
			zi = ti
			if zr * zr + zi * zi > 4.0 {
				escaped = true
				break
			}
		}
		if !escaped { return 1 }
		return 0
	}`
	proto := compileFunction(t, src)

	// IR interpreter (oracle)
	fn1 := BuildGraph(proto)
	fn1, _ = TypeSpecializePass(fn1)
	fn1, _ = ConstPropPass(fn1)
	fn1, _ = DCEPass(fn1)
	irResult, err := Interpret(fn1, nil)
	if err != nil {
		t.Fatalf("IR interpret error: %v", err)
	}

	// Tier 2 native
	fn2 := BuildGraph(proto)
	fn2, _ = TypeSpecializePass(fn2)
	fn2, _ = ConstPropPass(fn2)
	fn2, _ = DCEPass(fn2)
	alloc := AllocateRegisters(fn2)
	cf, err := Compile(fn2, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	result, err := cf.Execute(nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	vmResult := runVM(t, src, nil)

	t.Logf("Tier2=%v, IR=%v, VM=%v (expect 0, pixel escapes)", result, irResult, vmResult)
	if len(result) == 0 || len(irResult) == 0 {
		t.Fatal("empty result")
	}
	if uint64(result[0]) != uint64(irResult[0]) {
		t.Errorf("Tier2 vs IR MISMATCH: Tier2=%v, IR=%v", result[0], irResult[0])
	}
}

// TestFPRCycle_SinglePixelNoEscape verifies the non-escaping case (cr=0, ci=0).
func TestFPRCycle_SinglePixelNoEscape(t *testing.T) {
	src := `func f() {
		zr := 0.0
		zi := 0.0
		cr := 0.0
		ci := 0.0
		escaped := false
		for iter := 0; iter < 50; iter++ {
			tr := zr * zr - zi * zi + cr
			ti := 2.0 * zr * zi + ci
			zr = tr
			zi = ti
			if zr * zr + zi * zi > 4.0 {
				escaped = true
				break
			}
		}
		if !escaped { return 1 }
		return 0
	}`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	result, err := cf.Execute(nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	t.Logf("Tier2=%v (expect 1, pixel does NOT escape)", result)
	if len(result) == 0 {
		t.Fatal("empty result")
	}
	if result[0] != runtime.IntValue(1) {
		t.Errorf("expected 1 (not escaped), got %v", result[0])
	}
}

// TestFPRCycle_FloatCmpInLoop verifies float comparison works in a loop.
func TestFPRCycle_FloatCmpInLoop(t *testing.T) {
	src := `func f() {
		x := 1.0
		for i := 0; i < 10; i++ {
			x = x + 1.0
			if x > 1.5 { return i }
		}
		return -1
	}`
	proto := compileFunction(t, src)
	fn := BuildGraph(proto)
	fn, _ = TypeSpecializePass(fn)
	fn, _ = ConstPropPass(fn)
	fn, _ = DCEPass(fn)
	irResult, _ := Interpret(fn, nil)

	fn2 := BuildGraph(proto)
	fn2, _ = TypeSpecializePass(fn2)
	fn2, _ = ConstPropPass(fn2)
	fn2, _ = DCEPass(fn2)
	alloc := AllocateRegisters(fn2)
	cf, err := Compile(fn2, alloc)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	defer cf.Code.Free()

	result, err := cf.Execute(nil)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	t.Logf("Tier2=%v, IR=%v (expect 0)", result, irResult)
	if len(result) > 0 && len(irResult) > 0 && uint64(result[0]) != uint64(irResult[0]) {
		t.Errorf("MISMATCH: Tier2=%v, IR=%v", result[0], irResult[0])
	}
}
