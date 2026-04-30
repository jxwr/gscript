//go:build darwin && arm64

package methodjit

import (
	"os"
	"strings"
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestTableIntArrayKernel_DiagnosticsRecognizesPrefixReverseLoop(t *testing.T) {
	src := `
func reverse_prefix(a, k) {
    lo := 1
    hi := k
    for lo < hi {
        t := a[lo]
        a[lo] = a[hi]
        a[hi] = t
        lo = lo + 1
        hi = hi - 1
    }
}

func copy_prefix(dst, src, n) {
    for i := 1; i <= n; i++ {
        dst[i] = src[i]
    }
}

func copy_local(n) {
    src := {}
    dst := {}
    for i := 1; i <= n; i++ {
        src[i] = i
        dst[i] = 0
    }
    for i := 1; i <= n; i++ {
        dst[i] = src[i]
    }
    return dst[n]
}

func run() {
    sum := 0
    for r := 1; r <= 80; r++ {
        a := {}
        b := {}
        a[1] = r
        a[2] = r + 1
        a[3] = r + 2
        a[4] = r + 3
        b[1] = 0
        b[2] = 0
        b[3] = 0
        b[4] = 0
        copy_prefix(b, a, 4)
        reverse_prefix(a, 4)
        sum = sum + a[1] + b[4] + copy_local(4)
    }
    return sum
}

result := run()
`
	top := compileProto(t, src)
	globals := runtime.NewInterpreterGlobals()
	v := vm.New(globals)
	defer v.Close()
	v.SetMethodJIT(NewTieringManager())
	if _, err := v.Execute(top); err != nil {
		t.Fatalf("execute warmup: %v", err)
	}

	if err := os.Setenv("GSCRIPT_TIER2_NO_FILTER", "1"); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	defer os.Unsetenv("GSCRIPT_TIER2_NO_FILTER")
	art, err := NewTieringManager().CompileForDiagnostics(findProtoByName(top, "reverse_prefix"))
	if err != nil {
		t.Fatalf("CompileForDiagnostics(reverse_prefix): %v", err)
	}
	if !strings.Contains(art.IRAfter, "TableIntArrayReversePrefix") {
		t.Fatalf("expected structural prefix-reverse kernel in IR:\n%s", art.IRAfter)
	}
	art, err = NewTieringManager().CompileForDiagnostics(findProtoByName(top, "copy_local"))
	if err != nil {
		t.Fatalf("CompileForDiagnostics(copy_local): %v", err)
	}
	if !strings.Contains(art.IRAfter, "TableIntArrayCopyPrefix") {
		t.Fatalf("expected structural prefix-copy kernel in IR:\n%s", art.IRAfter)
	}
}

func TestTableIntArrayKernel_ExecutionFallbacksAndAliasVisibility(t *testing.T) {
	src := `
func reverse_prefix(a, k) {
    lo := 1
    hi := k
    for lo < hi {
        t := a[lo]
        a[lo] = a[hi]
        a[hi] = t
        lo = lo + 1
        hi = hi - 1
    }
}

func int_case(seed) {
    a := {}
    a[1] = seed
    a[2] = seed + 1
    a[3] = seed + 2
    a[4] = seed + 3
    reverse_prefix(a, 4)
    return a[1] - seed
}

func copy_local(n) {
    src := {}
    dst := {}
    for i := 1; i <= n; i++ {
        src[i] = i
        dst[i] = 0
    }
    for i := 1; i <= n; i++ {
        dst[i] = src[i]
    }
    return dst[n]
}

sum := 0
for r := 1; r <= 300; r++ {
    sum = sum + int_case(r)
    sum = sum + copy_local(4)
}

mixed := {}
mixed[1] = 1
mixed[2] = "x"
reverse_prefix(mixed, 2)
sum = sum + mixed[2]

bounded := {}
bounded[1] = 1
bounded[2] = 2
reverse_prefix(bounded, 4)
if bounded[4] == 1 {
    sum = sum + 10
}

aliased := {}
aliased[1] = 1
aliased[2] = 2
aliased[3] = 3
aliased[4] = 4
also := aliased
reverse_prefix(aliased, 4)
sum = sum + also[1] * 100 + also[4]

result := sum
`
	compareTier2Result(t, src, "result")
}
