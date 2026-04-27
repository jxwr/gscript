//go:build darwin && arm64

package methodjit

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestLoopExitStorePhis_DefersBoxedPhiWriteThrough(t *testing.T) {
	src := `
func fib_iter(n) {
    a := 0
    b := 1
    for i := 0; i < n; i++ {
        t := a + b
        a = b
        b = t
    }
    return a
}
result := fib_iter(70)
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "fib_iter")
	if proto == nil {
		t.Fatal("function fib_iter not found")
	}
	proto.EnsureFeedback()

	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("warm Execute: %v", err)
	}

	tm := NewTieringManager()
	art, err := tm.CompileForDiagnostics(proto)
	if err != nil {
		t.Fatalf("CompileForDiagnostics: %v", err)
	}
	if !strings.Contains(art.IRAfter, "= Add") {
		t.Fatalf("test must exercise boxed loop-carried arithmetic, IR:\n%s", art.IRAfter)
	}

	addBlock := -1
	for _, entry := range art.SourceMap {
		if entry.IROp == "Add" && entry.CodeStart >= 0 {
			addBlock = entry.BlockID
			break
		}
	}
	if addBlock < 0 {
		t.Fatalf("no mapped Add block; source map entries=%d", len(art.SourceMap))
	}

	var jump *IRASMMapEntry
	for i := range art.SourceMap {
		entry := &art.SourceMap[i]
		if entry.BlockID == addBlock && entry.IROp == "Jump" && entry.CodeEnd > entry.CodeStart {
			jump = entry
			break
		}
	}
	if jump == nil {
		t.Fatalf("no mapped backedge Jump for Add block %d", addBlock)
	}
	if stores := countStoresInCodeRange(art.CompiledCode, jump.CodeStart, jump.CodeEnd); stores != 0 {
		t.Fatalf("boxed loop phi backedge Jump emitted %d store(s), want deferred exit-only stores\nIR:\n%s",
			stores, art.IRAfter)
	}
}

func countStoresInCodeRange(code []byte, start, end int) int {
	if start < 0 {
		start = 0
	}
	if end > len(code) {
		end = len(code)
	}
	stores := 0
	for off := start; off+4 <= end; off += 4 {
		insn := binary.LittleEndian.Uint32(code[off : off+4])
		if arm64Class(insn) == "store" {
			stores++
		}
	}
	return stores
}
