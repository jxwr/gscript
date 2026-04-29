package vm

import (
	"errors"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

type resultBufferJIT struct {
	value      runtime.Value
	err        error
	sawBuffer  bool
	usedLegacy bool
}

func (j *resultBufferJIT) TryCompile(proto *FuncProto) interface{} {
	return proto
}

func (j *resultBufferJIT) Execute(compiled interface{}, regs []runtime.Value, base int, proto *FuncProto) ([]runtime.Value, error) {
	j.usedLegacy = true
	if j.err != nil {
		return nil, j.err
	}
	return []runtime.Value{j.value}, nil
}

func (j *resultBufferJIT) ExecuteWithResultBuffer(compiled interface{}, regs []runtime.Value, base int, proto *FuncProto, retBuf []runtime.Value) ([]runtime.Value, error) {
	j.sawBuffer = cap(retBuf) > 0
	if j.err != nil {
		return nil, j.err
	}
	return runtime.ReuseValueSlice1(retBuf, j.value), nil
}

func (j *resultBufferJIT) SetCallVM(v *VM) {}

func TestMethodJITReceivesReusableReturnBuffer(t *testing.T) {
	v := New(runtime.NewInterpreterGlobals())
	jit := &resultBufferJIT{value: runtime.IntValue(123)}
	v.SetMethodJIT(jit)

	results, err := v.Execute(&FuncProto{MaxStack: 1})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if jit.usedLegacy {
		t.Fatal("VM used legacy MethodJIT Execute path")
	}
	if !jit.sawBuffer {
		t.Fatal("MethodJIT did not receive reusable return buffer")
	}
	if len(results) != 1 || !results[0].IsInt() || results[0].Int() != 123 {
		t.Fatalf("results=%v, want int 123", results)
	}
}

func TestMethodJITBufferedReturnFallbackPreservesInterpreterMultiReturn(t *testing.T) {
	proto := compileProto(t, `func f() { return 1, 2 }`)
	v := New(runtime.NewInterpreterGlobals())
	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("Execute top: %v", err)
	}
	fn := v.GetGlobal("f")
	if fn.IsNil() {
		t.Fatal("missing function f")
	}

	jit := &resultBufferJIT{err: errors.New("force interpreter fallback")}
	v.SetMethodJIT(jit)
	results, err := v.CallValue(fn, nil)
	if err != nil {
		t.Fatalf("CallValue fallback: %v", err)
	}
	if !jit.sawBuffer {
		t.Fatal("MethodJIT did not receive reusable return buffer")
	}
	if len(results) != 2 ||
		!results[0].IsInt() || results[0].Int() != 1 ||
		!results[1].IsInt() || results[1].Int() != 2 {
		t.Fatalf("results=%v, want [1 2]", results)
	}
}
