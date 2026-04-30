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

func TestBoolTableFillLoop_DoesNotRewriteUnprovenDynamicStep(t *testing.T) {
	src := `
func init_flags(n, step) {
    flags := {}
    i := 2
    for i <= n {
        flags[i] = true
        i = i + step
    }
    return flags[3]
}
result := init_flags(10, 2)
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

func TestBoolTableFillLoop_RewritesStridedBoolMarkingLoop(t *testing.T) {
	src := `
func sieve_once(n) {
    flags := {}
    for i := 2; i <= n; i++ {
        flags[i] = true
    }
    i := 2
    for i * i <= n {
        if flags[i] {
            j := i * i
            for j <= n {
                flags[j] = false
                j = j + i
            }
        }
        i = i + 1
    }
    count := 0
    for i := 2; i <= n; i++ {
        if flags[i] { count = count + 1 }
    }
    return count
}
result := sieve_once(100)
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "sieve_once")
	if proto == nil {
		t.Fatal("sieve_once proto not found")
	}
	art, err := NewTieringManager().CompileForDiagnostics(proto)
	if err != nil {
		t.Fatalf("CompileForDiagnostics(sieve_once): %v", err)
	}
	if !strings.Contains(art.IRAfter, "TableBoolArrayFill") || !strings.Contains(art.IRAfter, " step ") {
		t.Fatalf("expected strided bool table fill rewrite:\n%s", art.IRAfter)
	}
	compareTier2Result(t, src, "result")
}

func TestBoolTableFillLoop_StridedFallbackMixedArray(t *testing.T) {
	src := `
func mark_mixed(n) {
    flags := {}
    for i := 0; i <= n; i++ {
        flags[i] = true
    }
    flags[0] = "mixed"
    step := 2
    j := 4
    for j <= n {
        flags[j] = false
        j = j + step
    }
    if flags[4] { return 0 }
    if flags[6] { return 0 }
    if flags[5] { return 1 }
    return 0
}
result := mark_mixed(8)
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "mark_mixed")
	if proto == nil {
		t.Fatal("mark_mixed proto not found")
	}
	art, err := NewTieringManager().CompileForDiagnostics(proto)
	if err != nil {
		t.Fatalf("CompileForDiagnostics(mark_mixed): %v", err)
	}
	if !strings.Contains(art.IRAfter, " step ") {
		t.Fatalf("expected strided bool table fill rewrite for fallback coverage:\n%s", art.IRAfter)
	}
	compareTier2Result(t, src, "result")
}

func TestBoolTableFillLoop_StridedFallbackBadBounds(t *testing.T) {
	src := `
func mark_bad_bounds() {
    flags := {}
    for i := 0; i <= 6; i++ {
        flags[i] = true
    }
    step := 2
    j := -2
    for j <= 4 {
        flags[j] = false
        j = j + step
    }
    if flags[-2] != false { return 0 }
    if flags[0] != false { return 0 }
    if flags[2] != false { return 0 }
    if flags[4] != false { return 0 }
    if flags[5] != true { return 0 }
    return 1
}
result := mark_bad_bounds()
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "mark_bad_bounds")
	if proto == nil {
		t.Fatal("mark_bad_bounds proto not found")
	}
	art, err := NewTieringManager().CompileForDiagnostics(proto)
	if err != nil {
		t.Fatalf("CompileForDiagnostics(mark_bad_bounds): %v", err)
	}
	if !strings.Contains(art.IRAfter, " step ") {
		t.Fatalf("expected strided bool table fill rewrite for bad-bounds fallback:\n%s", art.IRAfter)
	}
	compareTier2Result(t, src, "result")
}

func TestBoolTableFillLoop_DoesNotRewriteMutationHazard(t *testing.T) {
	src := `
func mark_with_hazard(n) {
    flags := {}
    for i := 0; i <= n; i++ {
        flags[i] = true
    }
    step := 2
    j := 4
    for j <= n {
        flags[j] = false
        flags[j + 1] = true
        j = j + step
    }
    return flags[n]
}
result := mark_with_hazard(8)
`
	top := compileTop(t, src)
	proto := findProtoByName(top, "mark_with_hazard")
	if proto == nil {
		t.Fatal("mark_with_hazard proto not found")
	}
	art, err := NewTieringManager().CompileForDiagnostics(proto)
	if err != nil {
		t.Fatalf("CompileForDiagnostics(mark_with_hazard): %v", err)
	}
	if strings.Contains(art.IRAfter, " step ") {
		t.Fatalf("mutation hazard loop should not be rewritten:\n%s", art.IRAfter)
	}
	compareTier2Result(t, src, "result")
}
