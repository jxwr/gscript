package methodjit

import (
	"strings"
	"testing"
)

func TestBoolTableFillLoop_RewritesContiguousBoolInit(t *testing.T) {
	src := `
func init_flags(n) {
    flags := {}
    for i := 2; i <= n; i++ {
        flags[i] = true
    }
    if flags[0] != nil { return 0 }
    if flags[1] != nil { return 0 }
    if !flags[2] { return 0 }
    return 1
}
result := init_flags(10)
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "init_flags")
	if proto == nil {
		t.Fatal("init_flags proto not found")
	}
	art, err := NewTieringManager().CompileForDiagnostics(proto)
	if err != nil {
		t.Fatalf("CompileForDiagnostics(init_flags): %v", err)
	}
	if !strings.Contains(art.IRAfter, "TableBoolArrayFill") {
		t.Fatalf("expected bool table fill rewrite:\n%s", art.IRAfter)
	}
	compareTier2Result(t, src, "result")
}

func TestBoolTableFillLoop_DoesNotRewriteNonContiguousLoop(t *testing.T) {
	src := `
func init_flags(n) {
    flags := {}
    i := 2
    for i <= n {
        flags[i] = true
        i = i + 2
    }
    return flags[3]
}
result := init_flags(10)
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "init_flags")
	if proto == nil {
		t.Fatal("init_flags proto not found")
	}
	art, err := NewTieringManager().CompileForDiagnostics(proto)
	if err != nil {
		t.Fatalf("CompileForDiagnostics(init_flags): %v", err)
	}
	if strings.Contains(art.IRAfter, "TableBoolArrayFill") {
		t.Fatalf("non-contiguous loop should not be rewritten:\n%s", art.IRAfter)
	}
	compareTier2Result(t, src, "result")
}
