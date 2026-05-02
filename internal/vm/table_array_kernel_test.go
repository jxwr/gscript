package vm

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

func TestTableArrayKernelsMatchFallback(t *testing.T) {
	src := `
func int_array_sum(n) {
    arr := {}
    for i := 1; i <= n; i++ {
        arr[i] = i
    }
    sum := 0
    for i := 1; i <= n; i++ {
        sum = sum + arr[i]
    }
    return sum
}

func float_dot_product(n) {
    a := {}
    b := {}
    for i := 1; i <= n; i++ {
        a[i] = 1.0 * i / n
        b[i] = 2.0 * (n - i + 1) / n
    }
    dot := 0.0
    for i := 1; i <= n; i++ {
        dot = dot + a[i] * b[i]
    }
    return dot
}

func array_swap_bench(n, reps) {
    arr := {}
    for i := 1; i <= n; i++ {
        arr[i] = n - i + 1
    }
    for r := 1; r <= reps; r++ {
        for i := 1; i < n; i = i + 2 {
            t := arr[i]
            arr[i] = arr[i + 1]
            arr[i + 1] = t
        }
    }
    return arr[1]
}

func array_2d_access(size) {
    rows := {}
    for i := 1; i <= size; i++ {
        row := {}
        for j := 1; j <= size; j++ {
            row[j] = i * size + j
        }
        rows[i] = row
    }
    sum := 0
    for i := 1; i <= size; i++ {
        row := rows[i]
        for j := 1; j <= size; j++ {
            sum = sum + row[j]
        }
    }
    return sum
}

r1 := int_array_sum(1000)
r2 := float_dot_product(1000)
r3 := array_swap_bench(1000, 11)
r4 := array_2d_access(30)
`
	proto := compileMixedInventoryKernelTestProgram(t, src)
	if len(proto.Protos) != 4 {
		t.Fatalf("nested protos = %d, want 4", len(proto.Protos))
	}
	requireKernelInfo(t, RecognizedWholeCallKernels(proto.Protos[0]), "table_array_int_sum")
	requireKernelInfo(t, RecognizedWholeCallKernels(proto.Protos[1]), "table_array_float_dot")
	requireKernelInfo(t, RecognizedWholeCallKernels(proto.Protos[2]), "table_array_swap")
	requireKernelInfo(t, RecognizedWholeCallKernels(proto.Protos[3]), "table_array_2d")

	globals := runtime.NewInterpreterGlobals()
	vm := New(globals)
	defer vm.Close()
	if _, err := vm.Execute(proto); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := globals["r1"]; !got.IsInt() || got.Int() != 500500 {
		t.Fatalf("r1=%v", got)
	}
	if got := globals["r3"]; !got.IsInt() || got.Int() != 999 {
		t.Fatalf("r3=%v", got)
	}
	if got := globals["r4"]; !got.IsInt() || got.Int() != 432450 {
		t.Fatalf("r4=%v", got)
	}
}
