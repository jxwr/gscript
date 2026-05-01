# Structural Variant Benchmarks

These benchmarks are intentionally kept outside the main suite. They are
designed to detect optimizations that overfit the current benchmark names,
literal shapes, or narrow type assumptions.

Run GScript variants with:

```sh
go build -o /tmp/gscript_variants ./cmd/gscript
/tmp/gscript_variants -jit benchmarks/variants/<name>.gs
```

Run matching LuaJIT references with:

```sh
luajit benchmarks/lua_variants/<name>.lua
```

## Variants

- `ack_nested_shifted`: Ackermann-like nested recurrence with the function
  renamed to `nestwave` and the `n == 0` recursive argument changed from `1`
  to `2`. This checks that the raw-int nested recursive kernel is structural
  rather than tied to the original `ack` name or exact zero-case literal.
- `sort_mixed_numeric`: Quicksort over two guarded datasets: negative integers
  and mixed integral floats. This checks radix/fallback guards and comparison
  correctness outside the original positive-integer workload.
- `matmul_row_variant`: Table-of-tables matrix multiplication using `N = 260`
  and a different row construction shape where rows are attached to the matrix
  before being populated. This checks that table/matmul improvements generalize
  beyond the original size and allocation pattern.
- `closure_accumulator_variant`: Closure accumulator workload with an integer
  delta of `3` plus a separate fractional delta fallback case. This checks that
  closure/upvalue accumulator wins are not specialized to increment-by-one or
  integer-only state.
