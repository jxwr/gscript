# GScript Performance Benchmarks

Comparative benchmarks for GScript, [gopher-lua](https://github.com/yuin/gopher-lua), and [starlark-go](https://pkg.go.dev/go.starlark.net/starlark).

## Scenarios

| # | Benchmark | What it measures |
|---|-----------|-----------------|
| 1 | **Fibonacci (recursive, n=20)** | Pure computation, deep recursion, call-stack pressure |
| 2 | **Fibonacci (iterative, n=30)** | Tight loop with arithmetic |
| 3 | **Table / dict operations** | Create a 1000-key table and read every key back |
| 4 | **String concatenation** | Append a character 100 times in a loop |
| 5 | **Closure creation** | Create 1000 closures that capture a variable |
| 6 | **Function calls** | Call a trivial `add(a,b)` function 10000 times |
| 7 | **VM startup** | Create a new VM and execute `x := 1` |

GScript benchmarks marked `_Warm` reuse a pre-initialized VM (define functions once, then call them N times) to separate parse/compile overhead from execution time.

## How to run

```bash
# Quick run (1s per benchmark)
go test ./benchmarks/ -bench=. -benchtime=1s -count=1

# Full run (3s per benchmark, more stable numbers)
go test ./benchmarks/ -bench=. -benchtime=3s -count=1

# Run only one category
go test ./benchmarks/ -bench=FibRecursive -benchtime=3s

# Save results
go test ./benchmarks/ -bench=. -benchtime=3s -count=1 | tee benchmarks/results.txt
```

## Latest results

Platform: Apple M4 Max, darwin/arm64, Go 1.25.7

```
BenchmarkGScriptFibRecursive-16             322    11160089 ns/op
BenchmarkGScriptFibRecursive_Warm-16        325    11279667 ns/op
BenchmarkGopherLuaFibRecursive-16          3482     1044179 ns/op
BenchmarkStarlarkFibRecursive-16         670899        5424 ns/op

BenchmarkGScriptFibIterative-16           85510       41348 ns/op
BenchmarkGScriptFibIterative_Warm-16     184182       19180 ns/op
BenchmarkGopherLuaFibIterative-16         59930       65138 ns/op
BenchmarkStarlarkFibIterative-16         380430        9406 ns/op

BenchmarkGScriptTableOps-16               2802     1293954 ns/op
BenchmarkGopherLuaTableOps-16             8006      443914 ns/op
BenchmarkStarlarkTableOps-16            668641        5251 ns/op

BenchmarkGScriptStringConcat-16          60879       58219 ns/op
BenchmarkGopherLuaStringConcat-16        62601       60760 ns/op
BenchmarkStarlarkStringConcat-16       1550168        2321 ns/op

BenchmarkGScriptClosureCreation-16        3993      914032 ns/op
BenchmarkGopherLuaClosureCreation-16     22027      158155 ns/op
BenchmarkStarlarkClosureCreation-16     839658        4276 ns/op

BenchmarkGScriptFunctionCalls-16           494     7199639 ns/op
BenchmarkGScriptFunctionCalls_Warm-16      526     6830237 ns/op
BenchmarkGopherLuaFunctionCalls-16        7119      500696 ns/op
BenchmarkStarlarkFunctionCalls-16       934700        3819 ns/op

BenchmarkGScriptVMStartup-16            185820       19310 ns/op
BenchmarkGopherLuaVMStartup-16           57544       67028 ns/op
BenchmarkStarlarkVMStartup-16          2957395        1198 ns/op
```

## Key takeaways

- **VM startup**: GScript starts ~3.5x faster than gopher-lua (19us vs 67us). Starlark is fastest at ~1.2us.
- **Iterative loops**: GScript is competitive with gopher-lua on tight loops (~41us vs ~65us cold; ~19us warm). Starlark leads at ~9us.
- **Recursive fibonacci**: GScript is ~10x slower than gopher-lua for deep recursion (11ms vs 1ms), indicating room for optimization in the call stack / function dispatch path.
- **String concatenation**: GScript and gopher-lua are nearly identical (~58-61us). Starlark is much faster (~2us).
- **Function calls (10k)**: GScript takes ~7ms vs gopher-lua's ~0.5ms, suggesting function call overhead is a key optimization target.
- **Table/dict ops**: GScript is ~3x slower than gopher-lua for hash table workloads.
- **Closures**: GScript is ~6x slower than gopher-lua for closure-heavy code.

The `_Warm` variants show that GScript's parse/compile step is relatively cheap compared to execution time -- the bottleneck is in the interpreter's execution loop and function dispatch.
