//go:build darwin && arm64

package methodjit

import (
	"strings"
	"testing"
)

func TestDiagIRASMSourceMap(t *testing.T) {
	top := compileTop(t, `
func add(a, b) {
	c := a + b
	return c
}
add(1, 2)
`)
	proto := findProtoByName(top, "add")
	if proto == nil {
		t.Fatal("missing add proto")
	}

	tm := NewTieringManager()
	art, err := tm.CompileForDiagnostics(proto)
	if err != nil {
		t.Fatalf("CompileForDiagnostics: %v", err)
	}
	if len(art.SourceMap) == 0 {
		t.Fatal("SourceMap is empty")
	}

	var sawSource, sawCode bool
	for _, entry := range art.SourceMap {
		if entry.BytecodePC >= 0 && entry.SourceLine > 0 && entry.BytecodeOp != "" {
			sawSource = true
		}
		if entry.CodeStart >= 0 && entry.CodeEnd > entry.CodeStart && entry.Pass == "normal" {
			sawCode = true
		}
	}
	if !sawSource {
		t.Fatalf("SourceMap has no source/bytecode-linked entries: %#v", art.SourceMap)
	}
	if !sawCode {
		t.Fatalf("SourceMap has no machine-code ranges: %#v", art.SourceMap)
	}

	annotated := disasmARM64Annotated(art.CompiledCode, art.SourceMap)
	if !strings.Contains(annotated, "; line=") || !strings.Contains(annotated, "ir=v") {
		t.Fatalf("annotated asm missing mapping comments:\n%s", annotated)
	}
}
