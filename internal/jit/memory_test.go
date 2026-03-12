//go:build darwin && arm64

package jit

import (
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

func TestAllocExecAndFree(t *testing.T) {
	block, err := AllocExec(4096)
	if err != nil {
		t.Fatalf("AllocExec: %v", err)
	}
	defer block.Free()

	if block.ptr == nil {
		t.Fatal("block.ptr is nil")
	}
	if len(block.mem) < 4096 {
		t.Fatalf("block.mem too small: %d", len(block.mem))
	}
}

func TestWriteAndExecuteCode(t *testing.T) {
	// Generate a trivial ARM64 function: add x0, x0, #1; ret
	asm := NewAssembler()
	asm.ADDimm(X0, X0, 1) // X0 = X0 + 1
	asm.RET()

	code, err := asm.Finalize()
	if err != nil {
		t.Fatal(err)
	}

	block, err := AllocExec(len(code))
	if err != nil {
		t.Fatalf("AllocExec: %v", err)
	}
	defer block.Free()

	if err := block.WriteCode(code); err != nil {
		t.Fatalf("WriteCode: %v", err)
	}

	// Call the function: int64 -> int64
	var fn func(int64) int64
	purego.RegisterFunc(&fn, uintptr(block.ptr))

	result := fn(41)
	if result != 42 {
		t.Fatalf("expected 42, got %d", result)
	}
}

func TestJITMultiply(t *testing.T) {
	// func(a, b int64) int64 { return a * b }
	asm := NewAssembler()
	asm.MUL(X0, X0, X1) // X0 = X0 * X1
	asm.RET()

	code, err := asm.Finalize()
	if err != nil {
		t.Fatal(err)
	}

	block, err := AllocExec(len(code))
	if err != nil {
		t.Fatal(err)
	}
	defer block.Free()
	block.WriteCode(code)

	var fn func(int64, int64) int64
	purego.RegisterFunc(&fn, uintptr(block.ptr))

	result := fn(6, 7)
	if result != 42 {
		t.Fatalf("expected 42, got %d", result)
	}
}

func TestJITLoop(t *testing.T) {
	// func(n int64) int64 { sum := 0; for i := 1; i <= n; i++ { sum += i }; return sum }
	asm := NewAssembler()

	// X0 = n, X1 = sum = 0, X2 = i = 1
	asm.MOVimm16(X1, 0)  // sum = 0
	asm.MOVimm16(X2, 1)  // i = 1

	asm.Label("loop")
	asm.ADDreg(X1, X1, X2) // sum += i
	asm.ADDimm(X2, X2, 1)  // i++
	asm.CMPreg(X2, X0)     // compare i, n
	asm.BCond(CondLE, "loop") // if i <= n, loop

	asm.MOVreg(X0, X1) // return sum
	asm.RET()

	code, err := asm.Finalize()
	if err != nil {
		t.Fatal(err)
	}

	block, err := AllocExec(len(code))
	if err != nil {
		t.Fatal(err)
	}
	defer block.Free()
	block.WriteCode(code)

	var fn func(int64) int64
	purego.RegisterFunc(&fn, uintptr(block.ptr))

	// sum(100) = 5050
	result := fn(100)
	if result != 5050 {
		t.Fatalf("expected 5050, got %d", result)
	}
}

func TestJITMemoryAccess(t *testing.T) {
	// Read an int64 from memory, add 10, store back, return the new value.
	// func(ptr *int64) int64 { *ptr += 10; return *ptr }
	asm := NewAssembler()
	asm.LDR(X1, X0, 0)      // X1 = *ptr
	asm.ADDimm(X1, X1, 10)  // X1 += 10
	asm.STR(X1, X0, 0)      // *ptr = X1
	asm.MOVreg(X0, X1)      // return X1
	asm.RET()

	code, err := asm.Finalize()
	if err != nil {
		t.Fatal(err)
	}

	block, err := AllocExec(len(code))
	if err != nil {
		t.Fatal(err)
	}
	defer block.Free()
	block.WriteCode(code)

	var fn func(unsafe.Pointer) int64
	purego.RegisterFunc(&fn, uintptr(block.ptr))

	val := int64(32)
	result := fn(unsafe.Pointer(&val))
	if result != 42 {
		t.Fatalf("expected 42, got %d", result)
	}
	if val != 42 {
		t.Fatalf("expected stored value 42, got %d", val)
	}
}

func TestJITFloatingPoint(t *testing.T) {
	// func(a, b float64) float64 { return a + b }
	// On ARM64, float args go in D0, D1; result in D0.
	// But purego passes floats differently - let's use memory instead.
	// func(ptr *[2]float64) float64 { return ptr[0] + ptr[1] }

	asm := NewAssembler()
	asm.FLDRd(D0, X0, 0)    // D0 = ptr[0]
	asm.FLDRd(D1, X0, 8)    // D1 = ptr[1]
	asm.FADDd(D0, D0, D1)   // D0 = D0 + D1
	asm.FMOVtoGP(X0, D0)    // X0 = D0 (bits)
	asm.RET()

	code, err := asm.Finalize()
	if err != nil {
		t.Fatal(err)
	}

	block, err := AllocExec(len(code))
	if err != nil {
		t.Fatal(err)
	}
	defer block.Free()
	block.WriteCode(code)

	// Use raw call since purego float handling can be tricky
	var fn func(unsafe.Pointer) uint64
	purego.RegisterFunc(&fn, uintptr(block.ptr))

	args := [2]float64{3.14, 2.86}
	resultBits := fn(unsafe.Pointer(&args[0]))
	result := *(*float64)(unsafe.Pointer(&resultBits))

	// 3.14 + 2.86 = 6.0
	if result != 6.0 {
		t.Fatalf("expected 6.0, got %f", result)
	}
}
