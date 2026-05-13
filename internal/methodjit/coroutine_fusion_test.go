//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestTier1FusesImmediateLeafCoroutineCreateResume(t *testing.T) {
	proto := compileTop(t, `
func run(n) {
	total := 0
	for i := 1; i <= n; i++ {
		co := coroutine.create(func() {
			return i * 2
		})
		ok, val := coroutine.resume(co)
		if !ok {
			return -1
		}
		total = total + val
	}
	return total
}

result := run(2000)
`)
	v := vm.New(runtime.NewInterpreterGlobals())
	defer v.Close()
	v.EnableCoroutineStats()
	v.SetMethodJIT(NewTieringManager())
	if _, err := v.Execute(proto); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	result := v.GetGlobal("result")
	if !result.IsInt() || result.Int() != 4002000 {
		t.Fatalf("result=%s, want 4002000", result.String())
	}
	stats := v.CoroutineStats()
	if stats.Created >= 2000 {
		t.Fatalf("immediate leaf create/resume was not fused: created=%d", stats.Created)
	}
}
