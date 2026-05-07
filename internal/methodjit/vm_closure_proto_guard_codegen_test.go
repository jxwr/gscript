//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestCompileIsVMClosureProtoKeepsProtoRefs(t *testing.T) {
	want := &vm.FuncProto{Name: "want"}
	fn := isVMClosureProtoFixture(want)
	alloc := AllocateRegisters(fn)
	cf, err := Compile(fn, alloc)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(cf.FuncProtoRefs) != 1 || cf.FuncProtoRefs[0] != want {
		t.Fatalf("compiled proto refs=%v, want [%p]", cf.FuncProtoRefs, want)
	}
}
