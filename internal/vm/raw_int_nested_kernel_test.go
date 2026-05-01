package vm

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

type rawIntNestedTestJIT struct{}

func (rawIntNestedTestJIT) TryCompile(proto *FuncProto) interface{} { return nil }
func (rawIntNestedTestJIT) Execute(compiled interface{}, regs []runtime.Value, base int, proto *FuncProto) ([]runtime.Value, error) {
	return nil, nil
}
func (rawIntNestedTestJIT) SetCallVM(v *VM) {}

const rawIntNestedAckSource = `
func ack(m, n) {
	if m == 0 { return n + 1 }
	if n == 0 { return ack(m - 1, 1) }
	return ack(m - 1, ack(m, n - 1))
}
`

func TestRawIntNestedKernelRecognizesAckermann(t *testing.T) {
	top := compileProto(t, rawIntNestedAckSource)
	ack := top.Protos[0]
	kernel, ok := analyzeRawIntNestedKernel(ack)
	if !ok {
		t.Fatal("ack should qualify for raw-int nested recurrence kernel")
	}
	if kernel.selfName != "ack" || kernel.baseAdd != 1 || kernel.zeroArg != 1 || kernel.mStep != 1 || kernel.nStep != 1 {
		t.Fatalf("unexpected kernel: %#v", kernel)
	}
	got, ok := kernel.fold(runtime.IntValue(3), runtime.IntValue(4))
	if !ok || got != 125 {
		t.Fatalf("ack(3,4) kernel = %d/%v, want 125/true", got, ok)
	}
}

func TestRawIntNestedKernelRecognizesNonAckNameAndZeroArg(t *testing.T) {
	top := compileProto(t, `
func hyper(m, n) {
	if m == 0 { return n + 1 }
	if n == 0 { return hyper(m - 1, 2) }
	return hyper(m - 1, hyper(m, n - 1))
}
`)
	hyper := top.Protos[0]
	kernel, ok := analyzeRawIntNestedKernel(hyper)
	if !ok {
		t.Fatal("non-ack nested recurrence should qualify")
	}
	got, ok := kernel.fold(runtime.IntValue(3), runtime.IntValue(2))
	if !ok || got != 119 {
		t.Fatalf("hyper(3,2) kernel = %d/%v, want 119/true", got, ok)
	}
}

func TestRawIntNestedKernelCallValueUsesPromotedWholeCall(t *testing.T) {
	top := compileProto(t, rawIntNestedAckSource)
	globals := runtime.NewInterpreterGlobals()
	v := New(globals)
	defer v.Close()
	v.SetMethodJIT(rawIntNestedTestJIT{})
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	ackValue := v.GetGlobal("ack")
	ack, ok := closureFromValue(ackValue)
	if !ok {
		t.Fatal("ack global is not a VM closure")
	}
	ack.Proto.Tier2Promoted = true

	got, err := v.CallValue(ackValue, []runtime.Value{runtime.IntValue(3), runtime.IntValue(4)})
	if err != nil {
		t.Fatalf("CallValue(ack): %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 125 {
		t.Fatalf("CallValue(ack) = %v, want int 125", got)
	}
	if ack.Proto.EnteredTier2 == 0 {
		t.Fatal("raw-int nested whole-call kernel did not mark Tier2 entry")
	}
}

func TestRawIntNestedKernelFallsBackWhenSelfGlobalChanges(t *testing.T) {
	top := compileProto(t, rawIntNestedAckSource+`
func replacement(m, n) {
	return 1000
}
`)
	globals := runtime.NewInterpreterGlobals()
	v := New(globals)
	defer v.Close()
	v.SetMethodJIT(rawIntNestedTestJIT{})
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute top: %v", err)
	}
	oldAckValue := v.GetGlobal("ack")
	oldAck, ok := closureFromValue(oldAckValue)
	if !ok {
		t.Fatal("old ack global is not a VM closure")
	}
	oldAck.Proto.Tier2Promoted = true

	v.SetGlobal("ack", v.GetGlobal("replacement"))
	got, err := v.CallValue(oldAckValue, []runtime.Value{runtime.IntValue(1), runtime.IntValue(0)})
	if err != nil {
		t.Fatalf("old ack after rebind: %v", err)
	}
	if len(got) != 1 || !got[0].IsInt() || got[0].Int() != 1000 {
		t.Fatalf("old ack after rebind = %v, want replacement result 1000", got)
	}
}
