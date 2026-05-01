package benchmarks

import (
	"math"
	"testing"

	gs "github.com/gscript/gscript/gscript"
)

var precisionBenchSink []interface{}

type precisionBenchCase struct {
	name     string
	source   string
	fn       string
	args     []interface{}
	warmArgs []interface{}
	want     interface{}
}

type precisionBenchMode struct {
	name string
	opts []gs.Option
	warm int
}

func BenchmarkPrecision(b *testing.B) {
	cases := []precisionBenchCase{
		{
			name: "fib/n30",
			fn:   "precision_fib",
			args: []interface{}{30},
			want: int64(832040),
			source: `
func precision_fib(n) {
    a := 0
    b := 1
    for i := 0; i < n; i++ {
        t := a + b
        a = b
        b = t
    }
    return a
}
`,
		},
		{
			name:     "fib_recursive/n20",
			fn:       "precision_fib_recursive",
			args:     []interface{}{20},
			warmArgs: []interface{}{10},
			want:     int64(6765),
			source: `
func precision_fib_recursive(n) {
    if n < 2 { return n }
    return precision_fib_recursive(n - 1) + precision_fib_recursive(n - 2)
}
`,
		},
		{
			name:     "ackermann/m3_n4",
			fn:       "precision_ackermann",
			args:     []interface{}{3, 4},
			warmArgs: []interface{}{2, 2},
			want:     int64(125),
			source: `
func precision_ackermann(m, n) {
    if m == 0 { return n + 1 }
    if n == 0 { return precision_ackermann(m - 1, 1) }
    return precision_ackermann(m - 1, precision_ackermann(m, n - 1))
}
`,
		},
		{
			name: "mutual_recursion/n25",
			fn:   "precision_hofstadter_f",
			args: []interface{}{25},
			want: int64(16),
			source: `
func precision_hofstadter_f(n) {
    if n == 0 { return 1 }
    return n - precision_hofstadter_m(precision_hofstadter_f(n - 1))
}

func precision_hofstadter_m(n) {
    if n == 0 { return 0 }
    return n - precision_hofstadter_f(precision_hofstadter_m(n - 1))
}
`,
		},
		{
			name: "method_dispatch/n100",
			fn:   "precision_method_dispatch",
			args: []interface{}{100},
			want: 10221.658172601095,
			source: `
func precision_new_point(x, y) {
    return {x: x, y: y}
}

func precision_point_distance(p1, p2) {
    dx := p1.x - p2.x
    dy := p1.y - p2.y
    return math.sqrt(dx * dx + dy * dy)
}

func precision_point_translate(p, dx, dy) {
    return precision_new_point(p.x + dx, p.y + dy)
}

func precision_point_scale(p, factor) {
    return precision_new_point(p.x * factor, p.y * factor)
}

func precision_method_dispatch(n) {
    total := 0.0
    p := precision_new_point(0.0, 0.0)
    for i := 1; i <= n; i++ {
        q := precision_new_point(1.0 * i, 2.0 * i)
        total = total + precision_point_distance(p, q)
        p = precision_point_translate(p, 0.1, 0.2)
        p = precision_point_scale(p, 0.999)
    }
    return total
}
`,
		},
		{
			name: "binary_trees/depth6_iters4",
			fn:   "precision_binary_trees",
			args: []interface{}{6, 4},
			want: int64(508),
			source: `
func precision_make_tree(depth) {
    if depth == 0 {
        return {left: nil, right: nil}
    }
    return {left: precision_make_tree(depth - 1), right: precision_make_tree(depth - 1)}
}

func precision_check_tree(node) {
    if node.left == nil { return 1 }
    return 1 + precision_check_tree(node.left) + precision_check_tree(node.right)
}

func precision_binary_trees(depth, iterations) {
    check := 0
    for i := 1; i <= iterations; i++ {
        check = check + precision_check_tree(precision_make_tree(depth))
    }
    return check
}
`,
		},
	}

	modes := []precisionBenchMode{
		{name: "VM", opts: []gs.Option{gs.WithVM()}},
		{name: "JIT", opts: []gs.Option{gs.WithJIT()}, warm: 20},
	}

	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			for _, mode := range modes {
				mode := mode
				b.Run(mode.name, func(b *testing.B) {
					runPrecisionBench(b, tc, mode)
				})
			}
		})
	}
}

func runPrecisionBench(b *testing.B, tc precisionBenchCase, mode precisionBenchMode) {
	b.Helper()
	b.ReportAllocs()

	vm := gs.New(mode.opts...)
	if err := vm.Exec(tc.source); err != nil {
		b.Fatalf("%s setup failed: %v", tc.name, err)
	}

	warmArgs := tc.warmArgs
	if warmArgs == nil {
		warmArgs = tc.args
	}
	for i := 0; i < mode.warm; i++ {
		if _, err := vm.Call(tc.fn, warmArgs...); err != nil {
			b.Fatalf("%s %s warmup failed: %v", tc.name, mode.name, err)
		}
	}
	result, err := vm.Call(tc.fn, tc.args...)
	if err != nil {
		b.Fatalf("%s %s validation call failed: %v", tc.name, mode.name, err)
	}
	verifyPrecisionBenchResult(b, tc, mode, result)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := vm.Call(tc.fn, tc.args...)
		if err != nil {
			b.Fatalf("%s %s call failed: %v", tc.name, mode.name, err)
		}
		precisionBenchSink = result
	}
}

func verifyPrecisionBenchResult(b *testing.B, tc precisionBenchCase, mode precisionBenchMode, got []interface{}) {
	b.Helper()
	if len(got) != 1 {
		b.Fatalf("%s %s returned %d values, want 1", tc.name, mode.name, len(got))
	}

	switch want := tc.want.(type) {
	case int64:
		gotInt, ok := precisionInt64(got[0])
		if !ok || gotInt != want {
			b.Fatalf("%s %s returned %v (%T), want %d", tc.name, mode.name, got[0], got[0], want)
		}
	case float64:
		gotFloat, ok := got[0].(float64)
		if !ok || math.Abs(gotFloat-want) > 1e-9 {
			b.Fatalf("%s %s returned %v (%T), want %.12f", tc.name, mode.name, got[0], got[0], want)
		}
	default:
		b.Fatalf("%s %s has unsupported expected value %T", tc.name, mode.name, tc.want)
	}
}

func precisionInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		if math.Trunc(n) == n {
			return int64(n), true
		}
	}
	return 0, false
}
