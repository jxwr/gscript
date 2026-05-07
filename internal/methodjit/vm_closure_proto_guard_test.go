package methodjit

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestIsVMClosureProtoInterp(t *testing.T) {
	want := &vm.FuncProto{Name: "want"}
	other := &vm.FuncProto{Name: "other"}
	fn := isVMClosureProtoFixture(want)

	match := runtime.VMClosureFastValue(unsafe.Pointer(vm.NewClosure(want)))
	missProto := runtime.VMClosureFastValue(unsafe.Pointer(vm.NewClosure(other)))
	notClosure := runtime.IntValue(7)

	for _, tt := range []struct {
		name string
		arg  runtime.Value
		want bool
	}{
		{name: "matching proto", arg: match, want: true},
		{name: "different proto", arg: missProto, want: false},
		{name: "non closure", arg: notClosure, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Interpret(fn, []runtime.Value{tt.arg})
			if err != nil {
				t.Fatalf("Interpret: %v", err)
			}
			if len(got) != 1 || !got[0].IsBool() || got[0].Bool() != tt.want {
				t.Fatalf("result=%v, want bool %v", got, tt.want)
			}
		})
	}
}

func TestValidateIsVMClosureProtoRequiresProtoRef(t *testing.T) {
	fn := isVMClosureProtoFixture(&vm.FuncProto{Name: "want"})
	fn.Entry.Instrs[1].Aux = 99

	errs := Validate(fn)
	if len(errs) == 0 {
		t.Fatal("Validate succeeded, want proto ref error")
	}
	if !strings.Contains(errs[0].Error(), "proto ref 99 out of range") {
		t.Fatalf("Validate error=%v, want proto ref out of range", errs[0])
	}
}

func TestFunctionAddFuncProtoRefDeduplicates(t *testing.T) {
	proto := &vm.FuncProto{Name: "p"}
	fn := &Function{}
	first := fn.AddFuncProtoRef(proto)
	second := fn.AddFuncProtoRef(proto)
	if first != 0 || second != first || len(fn.FuncProtoRefs) != 1 {
		t.Fatalf("refs: first=%d second=%d len=%d", first, second, len(fn.FuncProtoRefs))
	}
}

func isVMClosureProtoFixture(proto *vm.FuncProto) *Function {
	fn := &Function{Proto: &vm.FuncProto{Name: "fixture", NumParams: 1}, NumRegs: 1}
	entry := &Block{ID: 0}
	fn.Entry = entry
	fn.Blocks = []*Block{entry}
	load := &Instr{ID: fn.newValueID(), Op: OpLoadSlot, Type: TypeAny, Aux: 0, Block: entry}
	ref := fn.AddFuncProtoRef(proto)
	guard := &Instr{ID: fn.newValueID(), Op: OpIsVMClosureProto, Type: TypeBool, Args: []*Value{load.Value()}, Aux: int64(ref), Block: entry}
	ret := &Instr{ID: fn.newValueID(), Op: OpReturn, Type: TypeUnknown, Args: []*Value{guard.Value()}, Block: entry}
	entry.Instrs = []*Instr{load, guard, ret}
	return fn
}
