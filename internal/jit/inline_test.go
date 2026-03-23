//go:build darwin && arm64

package jit

import (
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

// runJITWithGlobals compiles a FuncProto with globals and executes it.
func runJITWithGlobals(t *testing.T, proto *vm.FuncProto, regs []runtime.Value, globals map[string]runtime.Value) (JITContext, []runtime.Value) {
	t.Helper()

	cg := &Codegen{
		asm:     NewAssembler(),
		proto:   proto,
		globals: globals,
	}
	cf, err := cg.compile()
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	defer cf.Code.Free()

	ctx := JITContext{
		Regs: uintptr(unsafe.Pointer(&regs[0])),
	}
	if len(proto.Constants) > 0 {
		ctx.Constants = uintptr(unsafe.Pointer(&proto.Constants[0]))
	}

	var fn func(uintptr) int64
	purego.RegisterFunc(&fn, uintptr(cf.Code.Ptr()))
	exitCode := fn(uintptr(unsafe.Pointer(&ctx)))
	ctx.ExitCode = exitCode

	return ctx, regs
}

// TestJIT_InlineSimpleAdd tests that add(a,b) { return a+b } is correctly inlined.
func TestJIT_InlineSimpleAdd(t *testing.T) {
	// Callee: func add(a, b) { return a + b }
	addProto := &vm.FuncProto{
		Name:      "add",
		NumParams: 2,
		MaxStack:  5,
		Code: []uint32{
			vm.EncodeABC(vm.OP_ADD, 2, 0, 1),    // R(2) = R(0) + R(1)
			vm.EncodeABC(vm.OP_RETURN, 2, 2, 0),  // return R(2)
		},
	}

	addClosure := &vm.Closure{Proto: addProto}
	globals := map[string]runtime.Value{
		"add": runtime.FunctionValue(addClosure),
	}

	// Caller: func caller() {
	//     R(0) = 10  (LOADINT)
	//     R(1) = add (GETGLOBAL) -- skipped by inline
	//     R(2) = R(0) (MOVE, arg1)
	//     R(3) = 32  (LOADINT, arg2)
	//     R(1) = add(R(2), R(3)) (CALL) -- inlined
	//     RETURN R(1)
	// }
	callerProto := &vm.FuncProto{
		Name:      "caller",
		MaxStack:  10,
		Constants: []runtime.Value{runtime.StringValue("add")},
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 0, 10),   // R(0) = 10
			vm.EncodeABx(vm.OP_GETGLOBAL, 1, 0),   // R(1) = globals["add"]
			vm.EncodeABC(vm.OP_MOVE, 2, 0, 0),      // R(2) = R(0)
			vm.EncodeAsBx(vm.OP_LOADINT, 3, 32),    // R(3) = 32
			vm.EncodeABC(vm.OP_CALL, 1, 3, 2),      // R(1) = add(R(2), R(3))
			vm.EncodeABC(vm.OP_RETURN, 1, 2, 0),    // return R(1)
		},
	}

	regs := make([]runtime.Value, 256)
	ctx, regs := runJITWithGlobals(t, callerProto, regs, globals)

	if ctx.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d (ExitPC=%d)", ctx.ExitCode, ctx.ExitPC)
	}
	if ctx.RetCount != 1 {
		t.Fatalf("expected 1 return value, got %d", ctx.RetCount)
	}
	retIdx := int(ctx.RetBase)
	if !regs[retIdx].IsInt() || regs[retIdx].Int() != 42 {
		t.Fatalf("expected IntValue(42), got %v (retBase=%d)", regs[retIdx], ctx.RetBase)
	}
}

// TestJIT_InlineSimpleAddInLoop tests inlining add(x, 1) in a for loop.
// This matches the benchmark: callMany() calls add(x, 1) 10000 times.
func TestJIT_InlineSimpleAddInLoop(t *testing.T) {
	// Callee: func add(a, b) { return a + b }
	addProto := &vm.FuncProto{
		Name:      "add",
		NumParams: 2,
		MaxStack:  5,
		Code: []uint32{
			vm.EncodeABC(vm.OP_ADD, 2, 0, 1),    // R(2) = R(0) + R(1)
			vm.EncodeABC(vm.OP_RETURN, 2, 2, 0),  // return R(2)
		},
	}

	addClosure := &vm.Closure{Proto: addProto}
	globals := map[string]runtime.Value{
		"add": runtime.FunctionValue(addClosure),
	}

	// Caller: func callMany() {
	//     x := 0
	//     for i := 0; i < 100; i++ {
	//         x = add(x, 1)
	//     }
	//     return x
	// }
	//
	// Bytecode (from compiler output, simplified to 100 iterations):
	// [000] LOADINT    R0 0           -- x = 0
	// [001] LOADINT    R1 0           -- i = 0
	// [002] LOADINT    R5 100         -- limit = 100
	// [003] LOADINT    R6 1           -- step = 1
	// [004] SUB        R2 R5 R6       -- R2 = limit - step (FORPREP setup)
	// [005] LOADINT    R3 1           -- step copy
	// [006] FORPREP    R1 5           -- to [012]
	// [007] GETGLOBAL  R5 K0          -- R5 = add (skipped by inline)
	// [008] MOVE       R6 R0          -- arg1 = x
	// [009] LOADINT    R7 1           -- arg2 = 1
	// [010] CALL       R5 B=3 C=2     -- R5 = add(R6, R7) (inlined)
	// [011] MOVE       R0 R5          -- x = result
	// [012] FORLOOP    R1 -6          -- to [007]
	// [013] MOVE       R4 R0          -- result = x
	// [014] RETURN     R4 B=2         -- return result
	callerProto := &vm.FuncProto{
		Name:      "callMany",
		MaxStack:  10,
		Constants: []runtime.Value{runtime.StringValue("add")},
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 0, 0),     // [000] R0 = 0
			vm.EncodeAsBx(vm.OP_LOADINT, 1, 0),     // [001] R1 = 0
			vm.EncodeAsBx(vm.OP_LOADINT, 5, 100),   // [002] R5 = 100
			vm.EncodeAsBx(vm.OP_LOADINT, 6, 1),     // [003] R6 = 1
			vm.EncodeABC(vm.OP_SUB, 2, 5, 6),       // [004] R2 = R5 - R6 = 99
			vm.EncodeAsBx(vm.OP_LOADINT, 3, 1),     // [005] R3 = 1
			vm.EncodeAsBx(vm.OP_FORPREP, 1, 5),     // [006] FORPREP R1, +5 → [012]
			vm.EncodeABx(vm.OP_GETGLOBAL, 5, 0),    // [007] R5 = globals["add"]
			vm.EncodeABC(vm.OP_MOVE, 6, 0, 0),      // [008] R6 = R0
			vm.EncodeAsBx(vm.OP_LOADINT, 7, 1),     // [009] R7 = 1
			vm.EncodeABC(vm.OP_CALL, 5, 3, 2),      // [010] R5 = add(R6, R7)
			vm.EncodeABC(vm.OP_MOVE, 0, 5, 0),      // [011] R0 = R5
			vm.EncodeAsBx(vm.OP_FORLOOP, 1, -6),    // [012] FORLOOP R1, -6 → [007]
			vm.EncodeABC(vm.OP_MOVE, 4, 0, 0),      // [013] R4 = R0
			vm.EncodeABC(vm.OP_RETURN, 4, 2, 0),    // [014] return R4
		},
	}

	regs := make([]runtime.Value, 256)
	ctx, regs := runJITWithGlobals(t, callerProto, regs, globals)

	if ctx.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d (ExitPC=%d)", ctx.ExitCode, ctx.ExitPC)
	}
	if ctx.RetCount != 1 {
		t.Fatalf("expected 1 return value, got %d", ctx.RetCount)
	}
	retIdx := int(ctx.RetBase)
	if !regs[retIdx].IsInt() || regs[retIdx].Int() != 100 {
		t.Fatalf("expected IntValue(100), got %v (retBase=%d)", regs[retIdx], ctx.RetBase)
	}
}

// TestJIT_InlineSubFunction tests inlining sub(a,b) { return a - b }.
func TestJIT_InlineSubFunction(t *testing.T) {
	subProto := &vm.FuncProto{
		Name:      "sub",
		NumParams: 2,
		MaxStack:  5,
		Code: []uint32{
			vm.EncodeABC(vm.OP_SUB, 2, 0, 1),
			vm.EncodeABC(vm.OP_RETURN, 2, 2, 0),
		},
	}

	subClosure := &vm.Closure{Proto: subProto}
	globals := map[string]runtime.Value{
		"sub": runtime.FunctionValue(subClosure),
	}

	callerProto := &vm.FuncProto{
		Name:      "caller",
		MaxStack:  10,
		Constants: []runtime.Value{runtime.StringValue("sub")},
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 0, 100),   // R0 = 100
			vm.EncodeABx(vm.OP_GETGLOBAL, 1, 0),    // R1 = globals["sub"]
			vm.EncodeABC(vm.OP_MOVE, 2, 0, 0),       // R2 = R0
			vm.EncodeAsBx(vm.OP_LOADINT, 3, 58),     // R3 = 58
			vm.EncodeABC(vm.OP_CALL, 1, 3, 2),       // R1 = sub(R2, R3)
			vm.EncodeABC(vm.OP_RETURN, 1, 2, 0),     // return R1
		},
	}

	regs := make([]runtime.Value, 256)
	ctx, regs := runJITWithGlobals(t, callerProto, regs, globals)

	if ctx.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d (ExitPC=%d)", ctx.ExitCode, ctx.ExitPC)
	}
	retIdx := int(ctx.RetBase)
	if !regs[retIdx].IsInt() || regs[retIdx].Int() != 42 {
		t.Fatalf("expected IntValue(42), got %v", regs[retIdx])
	}
}
