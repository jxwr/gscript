//go:build darwin && arm64

package jit

import (
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// runJIT compiles a FuncProto, executes it with the given register/constant setup,
// and returns the JITContext after execution.
func runJIT(t *testing.T, proto *vm.FuncProto, regs []runtime.Value) (JITContext, []runtime.Value) {
	t.Helper()

	cf, err := Compile(proto)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	defer cf.Code.Free()

	// Prepare context.
	ctx := JITContext{
		Regs:      uintptr(unsafe.Pointer(&regs[0])),
		Constants: 0, // set below
	}
	if len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	}

	// Call JIT code.
	var fn func(uintptr) int64
	purego.RegisterFunc(&fn, uintptr(cf.Code.Ptr()))
	exitCode := fn(uintptr(unsafe.Pointer(&ctx)))
	ctx.ExitCode = exitCode

	return ctx, regs
}

func TestJITLoadInt(t *testing.T) {
	// func() { a := 42 }
	proto := &vm.FuncProto{
		MaxStack: 4,
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 0, 42), // R(0) = 42
			vm.EncodeABC(vm.OP_RETURN, 0, 2, 0),  // return R(0)
		},
	}

	regs := make([]runtime.Value, 256)
	ctx, regs := runJIT(t, proto, regs)

	if ctx.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", ctx.ExitCode)
	}
	if ctx.RetCount != 1 {
		t.Fatalf("expected 1 return value, got %d", ctx.RetCount)
	}
	if !regs[0].IsInt() || regs[0].Int() != 42 {
		t.Fatalf("expected IntValue(42), got %v", regs[0])
	}
}

func TestJITAddIntegers(t *testing.T) {
	// func() { a := 10; b := 32; return a + b }
	proto := &vm.FuncProto{
		MaxStack: 4,
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 0, 10),  // R(0) = 10
			vm.EncodeAsBx(vm.OP_LOADINT, 1, 32),   // R(1) = 32
			vm.EncodeABC(vm.OP_ADD, 2, 0, 1),       // R(2) = R(0) + R(1)
			vm.EncodeABC(vm.OP_RETURN, 2, 2, 0),    // return R(2)
		},
	}

	regs := make([]runtime.Value, 256)
	ctx, regs := runJIT(t, proto, regs)

	if ctx.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d (ExitPC=%d)", ctx.ExitCode, ctx.ExitPC)
	}
	if !regs[2].IsInt() || regs[2].Int() != 42 {
		t.Fatalf("expected 42, got %v", regs[2])
	}
}

func TestJITSubIntegers(t *testing.T) {
	proto := &vm.FuncProto{
		MaxStack: 4,
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 0, 50),
			vm.EncodeAsBx(vm.OP_LOADINT, 1, 8),
			vm.EncodeABC(vm.OP_SUB, 2, 0, 1),
			vm.EncodeABC(vm.OP_RETURN, 2, 2, 0),
		},
	}

	regs := make([]runtime.Value, 256)
	ctx, regs := runJIT(t, proto, regs)

	if ctx.ExitCode != 0 {
		t.Fatalf("exit code %d, ExitPC=%d", ctx.ExitCode, ctx.ExitPC)
	}
	if !regs[2].IsInt() || regs[2].Int() != 42 {
		t.Fatalf("expected 42, got %v", regs[2])
	}
}

func TestJITMulIntegers(t *testing.T) {
	proto := &vm.FuncProto{
		MaxStack: 4,
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 0, 6),
			vm.EncodeAsBx(vm.OP_LOADINT, 1, 7),
			vm.EncodeABC(vm.OP_MUL, 2, 0, 1),
			vm.EncodeABC(vm.OP_RETURN, 2, 2, 0),
		},
	}

	regs := make([]runtime.Value, 256)
	ctx, regs := runJIT(t, proto, regs)

	if ctx.ExitCode != 0 {
		t.Fatalf("exit code %d", ctx.ExitCode)
	}
	if !regs[2].IsInt() || regs[2].Int() != 42 {
		t.Fatalf("expected 42, got %v", regs[2])
	}
}

func TestJITForLoop(t *testing.T) {
	// Equivalent to: sum := 0; for i := 1; i <= 100; i++ { sum += i }; return sum
	// Registers:
	//   R(0) = sum
	//   R(1) = loop idx / init (1)
	//   R(2) = limit (100)
	//   R(3) = step (1)
	//   R(4) = loop var (i)
	proto := &vm.FuncProto{
		MaxStack: 8,
		Code: []uint32{
			// pc=0: R(0) = 0 (sum)
			vm.EncodeAsBx(vm.OP_LOADINT, 0, 0),
			// pc=1: R(1) = 1 (init)
			vm.EncodeAsBx(vm.OP_LOADINT, 1, 1),
			// pc=2: R(2) = 100 (limit)
			vm.EncodeAsBx(vm.OP_LOADINT, 2, 100),
			// pc=3: R(3) = 1 (step)
			vm.EncodeAsBx(vm.OP_LOADINT, 3, 1),
			// pc=4: FORPREP R(1), +2 → jump to pc 7 (FORLOOP)
			vm.EncodeAsBx(vm.OP_FORPREP, 1, 2),
			// pc=5: loop body: sum += i → R(0) = R(0) + R(4)
			vm.EncodeABC(vm.OP_ADD, 0, 0, 4),
			// pc=6: (padding - JMP target from FORLOOP lands at pc=5)
			// Actually, FORLOOP at pc=7 jumps sBx=-2 → pc=7+1+(-2) = pc=6... let me recalculate.
			// FORPREP at pc=4 with sBx=+2 → jumps to pc=4+1+2 = pc=7
			// FORLOOP at pc=7 with sBx=-2 → jumps to pc=7+1+(-2) = pc=6
			// But we want the loop body at pc=5. So:
			// FORPREP sBx = +2 → pc 4+1+2 = 7 (FORLOOP)
			// FORLOOP sBx = -2 → pc 7+1-2 = 6, but body is at pc=5
			// Need: FORLOOP jumps to body start: target = 5, from pc=7: sBx = 5 - (7+1) = -3
			// Need: FORPREP jumps to FORLOOP: target = 7, from pc=4: sBx = 7 - (4+1) = 2 ✓
			// Fix: FORLOOP sBx = -3
			// Wait, also need the body to fall through to FORLOOP.
			// Layout: 0=loadint, 1=loadint, 2=loadint, 3=loadint, 4=FORPREP(sBx=+2→pc7),
			//         5=ADD(body), 6=FORLOOP(sBx=-2→pc5)... no.
			// Let me recalculate. FORPREP jumps to FORLOOP:
			// FORPREP A sBx: PC = pc+1+sBx
			// If FORPREP at pc=4, FORLOOP at pc=6: sBx = 6-(4+1) = 1
			// FORLOOP A sBx: if cont, PC = pc+1+sBx
			// If FORLOOP at pc=6, body at pc=5: sBx = 5-(6+1) = -2
			// Layout: 4=FORPREP(sBx=+1→pc6), 5=body, 6=FORLOOP(sBx=-2→pc5)
			0, // placeholder - will fix below
			0,
			// pc=7: return R(0)
			vm.EncodeABC(vm.OP_RETURN, 0, 2, 0),
		},
	}

	// Fix the layout: FORPREP at pc=4, body at pc=5, FORLOOP at pc=6
	proto.Code[4] = vm.EncodeAsBx(vm.OP_FORPREP, 1, 1)   // pc=4: jump to pc=6
	proto.Code[5] = vm.EncodeABC(vm.OP_ADD, 0, 0, 4)       // pc=5: sum += i
	proto.Code[6] = vm.EncodeAsBx(vm.OP_FORLOOP, 1, -2)    // pc=6: if cont, jump to pc=5
	proto.Code[7] = vm.EncodeABC(vm.OP_RETURN, 0, 2, 0)    // pc=7: return sum

	// Trim code to 8 instructions
	proto.Code = proto.Code[:8]

	regs := make([]runtime.Value, 256)
	ctx, regs := runJIT(t, proto, regs)

	if ctx.ExitCode != 0 {
		t.Fatalf("exit code %d, ExitPC=%d", ctx.ExitCode, ctx.ExitPC)
	}
	if !regs[0].IsInt() || regs[0].Int() != 5050 {
		t.Fatalf("expected sum=5050, got %v", regs[0])
	}
}

func TestJITFibIterative(t *testing.T) {
	// Iterative fibonacci: fib(n) where n is pre-loaded in R(0)
	// a, b := 0, 1
	// for i := 0; i < n; i++ { a, b = b, a+b }
	// return a
	//
	// Register layout:
	// R(0) = n (input, pre-loaded)
	// R(1) = a
	// R(2) = b
	// R(3) = temp (a+b)
	// R(4) = loop init (0)
	// R(5) = loop limit (n-1)
	// R(6) = loop step (1)
	// R(7) = loop var (i)

	n := int64(30)
	expected := int64(832040) // fib(30)

	proto := &vm.FuncProto{
		MaxStack: 16,
		Code: []uint32{
			// pc=0: a = 0
			vm.EncodeAsBx(vm.OP_LOADINT, 1, 0),
			// pc=1: b = 1
			vm.EncodeAsBx(vm.OP_LOADINT, 2, 1),
			// pc=2: loop init = 0
			vm.EncodeAsBx(vm.OP_LOADINT, 4, 0),
			// pc=3: loop limit = n-1 (we use R(0) directly... but need n-1 for <=)
			// Actually, for i := 0; i < n; i++: init=0, limit=n-1, step=1
			// We need: R(5) = R(0) - 1
			// Use SUB with constant... but we don't have SUB with immediate in bytecode.
			// Let's use LOADINT for a temp and SUB.
			vm.EncodeAsBx(vm.OP_LOADINT, 3, 1),           // R(3) = 1
			// pc=4: R(5) = R(0) - R(3) = n - 1
			vm.EncodeABC(vm.OP_SUB, 5, 0, 3),
			// pc=5: R(6) = 1 (step)
			vm.EncodeAsBx(vm.OP_LOADINT, 6, 1),
			// pc=6: FORPREP R(4), sBx → jump to FORLOOP at pc=11
			vm.EncodeAsBx(vm.OP_FORPREP, 4, 4),  // pc + 1 + 4 = 11
			// pc=7: body: temp = a + b
			vm.EncodeABC(vm.OP_ADD, 3, 1, 2),
			// pc=8: a = b
			vm.EncodeABC(vm.OP_MOVE, 1, 2, 0),
			// pc=9: b = temp
			vm.EncodeABC(vm.OP_MOVE, 2, 3, 0),
			// pc=10: (fall through to FORLOOP)
			// This is dead space - we need the layout to be contiguous
			// Actually: FORPREP at 6 jumps to 11, body at 7-9, then FORLOOP at 10
			// FORPREP sBx = 10 - (6+1) = 3
			// FORLOOP at 10, sBx jumps to 7: sBx = 7 - (10+1) = -4
			0, // placeholder for FORLOOP
			// pc=11: return a
			vm.EncodeABC(vm.OP_RETURN, 1, 2, 0),
		},
	}

	// Fix layout
	proto.Code[6] = vm.EncodeAsBx(vm.OP_FORPREP, 4, 3)   // jump to pc=10 (FORLOOP)
	proto.Code[10] = vm.EncodeAsBx(vm.OP_FORLOOP, 4, -4)  // jump back to pc=7 (body)

	regs := make([]runtime.Value, 256)
	regs[0] = runtime.IntValue(n) // pre-load n

	ctx, regs := runJIT(t, proto, regs)

	if ctx.ExitCode != 0 {
		t.Fatalf("exit code %d, ExitPC=%d", ctx.ExitCode, ctx.ExitPC)
	}
	if !regs[1].IsInt() || regs[1].Int() != expected {
		t.Fatalf("expected fib(%d)=%d, got %v", n, expected, regs[1])
	}
}

func TestJITLoadK(t *testing.T) {
	// Load a constant and return it.
	proto := &vm.FuncProto{
		MaxStack:  4,
		Constants: []runtime.Value{runtime.IntValue(9999)},
		Code: []uint32{
			vm.EncodeABx(vm.OP_LOADK, 0, 0),       // R(0) = K(0) = 9999
			vm.EncodeABC(vm.OP_RETURN, 0, 2, 0),
		},
	}

	regs := make([]runtime.Value, 256)
	ctx, regs := runJIT(t, proto, regs)

	if ctx.ExitCode != 0 {
		t.Fatalf("exit code %d", ctx.ExitCode)
	}
	if !regs[0].IsInt() || regs[0].Int() != 9999 {
		t.Fatalf("expected 9999, got %v", regs[0])
	}
}

func TestJITAddWithConstant(t *testing.T) {
	// The compiler always loads constants into registers via LOADK, so test that pattern.
	// R(0) = R(1) + R(2) where R(2) is loaded from K(0) = 100
	proto := &vm.FuncProto{
		MaxStack:  4,
		Constants: []runtime.Value{runtime.IntValue(100)},
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 1, 42),
			vm.EncodeABx(vm.OP_LOADK, 2, 0),         // R(2) = K(0) = 100
			vm.EncodeABC(vm.OP_ADD, 0, 1, 2),         // R(0) = R(1) + R(2)
			vm.EncodeABC(vm.OP_RETURN, 0, 2, 0),
		},
	}

	regs := make([]runtime.Value, 256)
	ctx, regs := runJIT(t, proto, regs)

	if ctx.ExitCode != 0 {
		t.Fatalf("exit code %d, ExitPC=%d", ctx.ExitCode, ctx.ExitPC)
	}
	if !regs[0].IsInt() || regs[0].Int() != 142 {
		t.Fatalf("expected 142, got %v", regs[0])
	}
}

func TestJITSideExitOnNonIntArith(t *testing.T) {
	// ADD with a string value should side-exit.
	proto := &vm.FuncProto{
		MaxStack: 4,
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 0, 10),
			vm.EncodeAsBx(vm.OP_LOADINT, 1, 20),
			vm.EncodeABC(vm.OP_ADD, 2, 0, 1), // this works (both int)
			vm.EncodeABC(vm.OP_RETURN, 2, 2, 0),
		},
	}

	regs := make([]runtime.Value, 256)
	// Override R(1) with a string to trigger side exit
	regs[1] = runtime.StringValue("hello")

	ctx, _ := runJIT(t, proto, regs)

	// The LOADINT at pc=1 will overwrite R(1) with IntValue(20),
	// so the ADD at pc=2 should succeed. Let's test with pre-loaded values instead.

	// Second test: skip the LOADINTs, just do ADD
	proto2 := &vm.FuncProto{
		MaxStack: 4,
		Code: []uint32{
			vm.EncodeABC(vm.OP_ADD, 2, 0, 1),
			vm.EncodeABC(vm.OP_RETURN, 2, 2, 0),
		},
	}

	regs2 := make([]runtime.Value, 256)
	regs2[0] = runtime.IntValue(10)
	regs2[1] = runtime.StringValue("hello")

	ctx, _ = runJIT(t, proto2, regs2)

	if ctx.ExitCode != 1 {
		t.Fatalf("expected side exit (code=1), got %d", ctx.ExitCode)
	}
	if ctx.ExitPC != 0 {
		t.Fatalf("expected ExitPC=0 (ADD instruction), got %d", ctx.ExitPC)
	}
}

func TestJITComparison(t *testing.T) {
	// if 10 < 20 then R(0) = 1 else R(0) = 0
	// OP_LT A=0 B=0 C=1 → if (R(0) < R(1)) != false then skip (i.e., if NOT less, skip)
	// Wait, the semantics: if (RK(B) < RK(C)) != bool(A) then PC++ (skip)
	// A=0: skip if condition is TRUE (condition matches false → skip)
	// Actually: if result != bool(A): skip.
	// A=0: if (B < C) != false → if (B < C) is true → skip.
	// So A=0 means "skip next if B < C is true".

	// Layout:
	// pc=0: LOADINT R(0), 10
	// pc=1: LOADINT R(1), 20
	// pc=2: LT A=1 B=0 C=1  → if (R(0) < R(1)) != true → skip. Since 10<20 is true, != true is false → don't skip
	// pc=3: JMP +1 → skip to pc=5 (the "then" path continues, jumps over "else")
	// pc=4: LOADINT R(2), 0 (else)
	// pc=5: LOADINT R(2), 1 (then... wait, this is wrong)
	// Actually let me use a simpler encoding.

	// Just test: R(0) = 5, R(1) = 10
	// LT A=1 B=0 C=1: if (R(0) < R(1)) != true then skip
	// Since 5 < 10 is true, != true is false → don't skip → execute next JMP
	// JMP +1 → skip the "else" LOADINT
	// LOADINT R(2), 0 (else, skipped)
	// LOADINT R(2), 1 (then, executed)
	// RETURN R(2)

	proto := &vm.FuncProto{
		MaxStack: 4,
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 0, 5),    // R(0) = 5
			vm.EncodeAsBx(vm.OP_LOADINT, 1, 10),   // R(1) = 10
			vm.EncodeABC(vm.OP_LT, 1, 0, 1),        // if (5 < 10) != true → skip. 5<10=true, !=true=false → don't skip
			vm.EncodesBx(vm.OP_JMP, 1),              // skip the else (jump +1 to pc=5)
			vm.EncodeAsBx(vm.OP_LOADINT, 2, 0),     // else: R(2) = 0
			vm.EncodeAsBx(vm.OP_LOADINT, 2, 1),     // then: R(2) = 1
			vm.EncodeABC(vm.OP_RETURN, 2, 2, 0),
		},
	}

	regs := make([]runtime.Value, 256)
	ctx, regs := runJIT(t, proto, regs)

	if ctx.ExitCode != 0 {
		t.Fatalf("exit code %d, ExitPC=%d", ctx.ExitCode, ctx.ExitPC)
	}
	if !regs[2].IsInt() || regs[2].Int() != 1 {
		t.Fatalf("expected R(2)=1 (then branch), got %v", regs[2])
	}
}

func TestJITLoadBool(t *testing.T) {
	proto := &vm.FuncProto{
		MaxStack: 4,
		Code: []uint32{
			vm.EncodeABC(vm.OP_LOADBOOL, 0, 1, 0), // R(0) = true
			vm.EncodeABC(vm.OP_LOADBOOL, 1, 0, 0), // R(1) = false
			vm.EncodeABC(vm.OP_RETURN, 0, 3, 0),    // return R(0), R(1)
		},
	}

	regs := make([]runtime.Value, 256)
	ctx, regs := runJIT(t, proto, regs)

	if ctx.ExitCode != 0 {
		t.Fatalf("exit code %d", ctx.ExitCode)
	}
	if !regs[0].IsBool() || !regs[0].Bool() {
		t.Fatalf("expected true, got %v", regs[0])
	}
	if !regs[1].IsBool() || regs[1].Bool() {
		t.Fatalf("expected false, got %v", regs[1])
	}
}

func TestJITLoadNil(t *testing.T) {
	proto := &vm.FuncProto{
		MaxStack: 4,
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 0, 42),
			vm.EncodeABC(vm.OP_LOADNIL, 0, 0, 0),   // R(0) = nil
			vm.EncodeABC(vm.OP_RETURN, 0, 2, 0),
		},
	}

	regs := make([]runtime.Value, 256)
	ctx, regs := runJIT(t, proto, regs)

	if ctx.ExitCode != 0 {
		t.Fatalf("exit code %d", ctx.ExitCode)
	}
	if !regs[0].IsNil() {
		t.Fatalf("expected nil, got %v", regs[0])
	}
}

func TestJITReturnNothing(t *testing.T) {
	proto := &vm.FuncProto{
		MaxStack: 4,
		Code: []uint32{
			vm.EncodeABC(vm.OP_RETURN, 0, 1, 0), // return nothing (B=1)
		},
	}

	regs := make([]runtime.Value, 256)
	ctx, _ := runJIT(t, proto, regs)

	if ctx.ExitCode != 0 {
		t.Fatalf("exit code %d", ctx.ExitCode)
	}
	if ctx.RetCount != 0 {
		t.Fatalf("expected 0 return values, got %d", ctx.RetCount)
	}
}

func TestJITNot(t *testing.T) {
	proto := &vm.FuncProto{
		MaxStack: 4,
		Code: []uint32{
			vm.EncodeABC(vm.OP_LOADBOOL, 0, 1, 0), // R(0) = true
			vm.EncodeABC(vm.OP_NOT, 1, 0, 0),       // R(1) = !R(0) = false
			vm.EncodeABC(vm.OP_RETURN, 1, 2, 0),
		},
	}

	regs := make([]runtime.Value, 256)
	ctx, regs := runJIT(t, proto, regs)

	if ctx.ExitCode != 0 {
		t.Fatalf("exit code %d", ctx.ExitCode)
	}
	if !regs[1].IsBool() || regs[1].Bool() {
		t.Fatalf("expected false, got %v", regs[1])
	}
}

func TestJITSetfieldNativeFastPath(t *testing.T) {
	// Test that SETFIELD takes the native fast path for plain tables.
	// Bytecode: R(0) = table, R(1) = value; SETFIELD R(0) K(0) R(1); RETURN R(0)
	// Table has field "x" in skeys[0], and we overwrite it via SETFIELD.

	tbl := runtime.NewTable()
	tbl.RawSetString("x", runtime.IntValue(10))

	proto := &vm.FuncProto{
		MaxStack:  4,
		Constants: []runtime.Value{runtime.StringValue("x")},
		Code: []uint32{
			// SETFIELD R(0)[Constants[0]] = R(1)   → table.x = R(1)
			vm.EncodeABC(vm.OP_SETFIELD, 0, 0, 1),
			vm.EncodeABC(vm.OP_RETURN, 0, 1, 0), // return (no return values)
		},
	}

	regs := make([]runtime.Value, 256)
	regs[0] = runtime.TableValue(tbl) // R(0) = table {x: 10}
	regs[1] = runtime.IntValue(99)    // R(1) = 99

	ctx, _ := runJIT(t, proto, regs)

	// The native fast path should have written 99 to table.x
	val := tbl.RawGetString("x")
	if !val.IsInt() || val.Int() != 99 {
		t.Fatalf("expected table.x = 99, got %v (exit code=%d, exitPC=%d)", val, ctx.ExitCode, ctx.ExitPC)
	}
}

func TestJITSetfieldNativeFastPathFloat(t *testing.T) {
	// Test SETFIELD with float values (the nbody pattern).
	tbl := runtime.NewTable()
	tbl.RawSetString("x", runtime.FloatValue(1.0))
	tbl.RawSetString("y", runtime.FloatValue(2.0))

	proto := &vm.FuncProto{
		MaxStack:  4,
		Constants: []runtime.Value{runtime.StringValue("x"), runtime.StringValue("y")},
		Code: []uint32{
			// SETFIELD R(0)[Constants[0]] = R(1)   → table.x = R(1)
			vm.EncodeABC(vm.OP_SETFIELD, 0, 0, 1),
			// SETFIELD R(0)[Constants[1]] = R(2)   → table.y = R(2)
			vm.EncodeABC(vm.OP_SETFIELD, 0, 1, 2),
			vm.EncodeABC(vm.OP_RETURN, 0, 1, 0),
		},
	}

	regs := make([]runtime.Value, 256)
	regs[0] = runtime.TableValue(tbl)
	regs[1] = runtime.FloatValue(3.14)
	regs[2] = runtime.FloatValue(2.71)

	ctx, _ := runJIT(t, proto, regs)

	valX := tbl.RawGetString("x")
	valY := tbl.RawGetString("y")
	if !valX.IsFloat() || valX.Float() != 3.14 {
		t.Fatalf("expected table.x = 3.14, got %v (exit=%d)", valX, ctx.ExitCode)
	}
	if !valY.IsFloat() || valY.Float() != 2.71 {
		t.Fatalf("expected table.y = 2.71, got %v (exit=%d)", valY, ctx.ExitCode)
	}
}

func TestJITSetfieldFallbackMetatable(t *testing.T) {
	// Test SETFIELD fallback when table has a metatable.
	// The native fast path should fall back to call-exit.
	tbl := runtime.NewTable()
	tbl.RawSetString("x", runtime.IntValue(10))
	mt := runtime.NewTable()
	tbl.SetMetatable(mt)

	proto := &vm.FuncProto{
		MaxStack:  4,
		Constants: []runtime.Value{runtime.StringValue("x")},
		Code: []uint32{
			vm.EncodeABC(vm.OP_SETFIELD, 0, 0, 1),
			vm.EncodeABC(vm.OP_RETURN, 0, 1, 0),
		},
	}

	regs := make([]runtime.Value, 256)
	regs[0] = runtime.TableValue(tbl)
	regs[1] = runtime.IntValue(99)

	ctx, _ := runJIT(t, proto, regs)

	// Should fall back to call-exit (ExitCode=2)
	if ctx.ExitCode != 2 {
		t.Fatalf("expected call-exit (ExitCode=2), got %d", ctx.ExitCode)
	}
}

func TestJITUNM(t *testing.T) {
	proto := &vm.FuncProto{
		MaxStack: 4,
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 0, 42),
			vm.EncodeABC(vm.OP_UNM, 1, 0, 0),   // R(1) = -R(0) = -42
			vm.EncodeABC(vm.OP_RETURN, 1, 2, 0),
		},
	}

	regs := make([]runtime.Value, 256)
	ctx, regs := runJIT(t, proto, regs)

	if ctx.ExitCode != 0 {
		t.Fatalf("exit code %d", ctx.ExitCode)
	}
	if !regs[1].IsInt() || regs[1].Int() != -42 {
		t.Fatalf("expected -42, got %v", regs[1])
	}
}
