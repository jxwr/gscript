# GScript Performance Benchmarks

Comparative benchmarks across five runtimes:

| Runtime | Type | Language |
|---------|------|----------|
| **GScript (tree-walker)** | AST interpreter | Go |
| **GScript (bytecode VM)** | Register-based bytecode VM | Go |
| **gopher-lua** | Lua 5.1 VM | Go |
| **starlark-go** | Starlark interpreter | Go |
| **Lua 5.5** | Official Lua VM | C |
| **LuaJIT 2.1** | Tracing JIT compiler | C/ASM |

## Scenarios

| # | Benchmark | What it measures |
|---|-----------|-----------------|
| 1 | **Fibonacci (recursive, n=20)** | Pure computation, deep recursion, call-stack pressure |
| 2 | **Fibonacci (recursive, n=25)** | Same, heavier workload to reduce startup noise |
| 3 | **Fibonacci (iterative, n=30)** | Tight loop with arithmetic |
| 4 | **Table / dict operations** | Create a 1000-key table and read every key back |
| 5 | **String concatenation** | Append a character 100 times in a loop |
| 6 | **Closure creation** | Create 1000 closures that capture a variable |
| 7 | **Function calls** | Call a trivial `add(a,b)` function 10,000 times |
| 8 | **VM startup** | Create a new VM and execute `x := 1` |

> **Note:** Starlark forbids recursion and top-level for-loops by design.
> Recursive benchmarks exclude Starlark; all other Starlark benchmarks wrap code in functions.

## How to run

```bash
# Go-based benchmarks (GScript, gopher-lua, starlark-go)
go test ./benchmarks/ -bench=. -benchtime=3s -count=1

# Native Lua / LuaJIT (requires lua and luajit on PATH)
lua benchmarks/lua/bench_all.lua
luajit benchmarks/lua/bench_all.lua

# Run only one category
go test ./benchmarks/ -bench=FibRecursive -benchtime=3s

# Save results
go test ./benchmarks/ -bench=. -benchtime=3s -count=1 | tee benchmarks/results.txt
```

## Latest results

Platform: Apple M4 Max, darwin/arm64, Go 1.25.7, Lua 5.5.0, LuaJIT 2.1

### Summary table (us/op, lower is better)

| Benchmark | GScript Tree | GScript VM | VM Speedup | gopher-lua | starlark-go | Lua 5.5 (C) | LuaJIT |
|---|---:|---:|---:|---:|---:|---:|---:|
| Fib recursive (n=20) | 11,262 | 2,710 | **4.2x** | 1,035 | n/a | 227 | 27 |
| Fib recursive (n=25) | ~125,000 | 28,581 | **~4.4x** | 10,885 | n/a | 2,496 | 297 |
| Fib iterative (n=30) | 95 | 89 | 1.1x | 48 | 9 | <1 | <1 |
| Table ops (1000 keys) | 1,341 | 529 | **2.5x** | 437 | 254 | 166 | 36 |
| String concat (100x) | 114 | 97 | 1.2x | 48 | 11 | 3 | 1 |
| Closure creation (1000) | 979 | 289 | **3.4x** | 152 | 207 | 86 | 42 |
| Function calls (10000) | 7,084 | 1,324 | **5.4x** | 495 | 732 | 114 | 3 |
| VM startup | 73 | 85 | 0.9x | 40 | 1 | — | — |

### Raw Go benchmark output

```
cpu: Apple M4 Max
BenchmarkGScriptFibRecursive-16          	     322	  11261917 ns/op
BenchmarkGScriptVMFibRecursive-16        	    1341	   2710203 ns/op
BenchmarkGopherLuaFibRecursive-16        	    3501	   1034507 ns/op

BenchmarkGScriptVMFibRecursive_N25-16    	     124	  28581499 ns/op
BenchmarkGopherLuaFibRecursive_N25-16    	     330	  10884599 ns/op

BenchmarkGScriptFibIterative-16          	   37750	     94891 ns/op
BenchmarkGScriptVMFibIterative-16        	   40116	     88651 ns/op
BenchmarkGopherLuaFibIterative-16        	   73249	     48355 ns/op
BenchmarkStarlarkFibIterative-16         	  387262	      9135 ns/op

BenchmarkGScriptTableOps-16             	    2660	   1340942 ns/op
BenchmarkGScriptVMTableOps-16           	    6768	    528523 ns/op
BenchmarkGopherLuaTableOps-16           	    8196	    437417 ns/op
BenchmarkStarlarkTableOps-16            	   14198	    253777 ns/op

BenchmarkGScriptStringConcat-16          	   31597	    114128 ns/op
BenchmarkGScriptVMStringConcat-16        	   38272	     96582 ns/op
BenchmarkGopherLuaStringConcat-16        	   72957	     47818 ns/op
BenchmarkStarlarkStringConcat-16         	  336259	     10712 ns/op

BenchmarkGScriptClosureCreation-16       	    3686	    978709 ns/op
BenchmarkGScriptVMClosureCreation-16     	   12466	    289242 ns/op
BenchmarkGopherLuaClosureCreation-16     	   23499	    152355 ns/op
BenchmarkStarlarkClosureCreation-16      	   17326	    207167 ns/op

BenchmarkGScriptFunctionCalls-16         	     504	   7083599 ns/op
BenchmarkGScriptVMFunctionCalls-16       	    2704	   1323699 ns/op
BenchmarkGopherLuaFunctionCalls-16       	    7126	    495194 ns/op
BenchmarkStarlarkFunctionCalls-16        	    4918	    731851 ns/op

BenchmarkGScriptStartup-16              	   48685	     73201 ns/op
BenchmarkGScriptVMStartup-16            	   42736	     84883 ns/op
BenchmarkGopherLuaStartup-16            	   89572	     40205 ns/op
BenchmarkStarlarkStartup-16             	 3125035	      1152 ns/op
```

### Native Lua / LuaJIT output

```
=== Lua 5.5.0 ===
FibRecursive_N20       1000 iterations       227 us/op
FibRecursive_N25        100 iterations      2496 us/op
FibIterative_N30     100000 iterations        <1 us/op
TableOps_1000          1000 iterations       166 us/op
StringConcat_100      10000 iterations         3 us/op
ClosureCreation_1000   1000 iterations        86 us/op
FunctionCalls_10000     100 iterations       114 us/op

=== LuaJIT 2.1 ===
FibRecursive_N20       1000 iterations        27 us/op
FibRecursive_N25        100 iterations       297 us/op
FibIterative_N30     100000 iterations        <1 us/op
TableOps_1000          1000 iterations        36 us/op
StringConcat_100      10000 iterations         1 us/op
ClosureCreation_1000   1000 iterations        42 us/op
FunctionCalls_10000     100 iterations         3 us/op
```

## Analysis

### Bytecode VM vs Tree-walker

The bytecode VM delivers **3-5x speedup** on function-call-heavy workloads:

- **Function calls (10k):** 5.4x faster — the biggest win, since the tree-walker's per-call overhead (map-based Environment allocation, AST node traversal) is eliminated.
- **Fib recursive (n=20):** 4.2x faster — deep recursion exercises the call stack heavily.
- **Closure creation:** 3.4x faster — closure instantiation is much cheaper with bytecode upvalue descriptors vs runtime free-variable analysis.
- **Table ops:** 2.5x faster — bytecode instructions avoid repeated AST dispatch for each table access.
- **Loops / string concat:** ~1.1x — for tight loops with minimal function call overhead, the improvement is modest since both backends use the same underlying Value and Table types.
- **Startup:** 0.9x — the bytecode VM is slightly slower to start due to the compile step, but the difference is negligible (~12us).

### GScript VM vs gopher-lua

The GScript bytecode VM is roughly **2-3x slower** than gopher-lua across most benchmarks. gopher-lua is a mature, well-optimized Lua 5.1 implementation with years of tuning. The gap could be narrowed with:

- NaN-boxing (packing values into 8 bytes instead of the current tagged struct)
- Computed goto dispatch (requires Go assembly or build tags)
- Instruction specialization (ADDI, GETTABUP, etc.)
- Register allocation improvements

### Go-based interpreters vs native Lua / LuaJIT

| | GScript VM | gopher-lua | Lua 5.5 (C) | LuaJIT |
|---|---:|---:|---:|---:|
| Fib(20) relative | 12x | 4.6x | 1x | 0.12x |
| Func calls relative | 11.6x | 4.3x | 1x | 0.03x |

Native C Lua is **4-5x faster** than Go-based Lua implementations due to lower-level memory management, computed goto dispatch, and no GC pressure from Go's runtime. LuaJIT is another **8-40x faster** than PUC Lua thanks to its tracing JIT compiler — an entirely different class of runtime.

### Starlark

Starlark performs well on iterative workloads (9us for fib iterative) but cannot be compared on recursive benchmarks since **Starlark forbids recursion by design** — it is a configuration language (Bazel BUILD files), not a general-purpose scripting language. Previous benchmark results showing ~5us for Starlark fib(20) were invalid: the function was failing immediately with a "recursion forbidden" error, measuring only error-return time.
