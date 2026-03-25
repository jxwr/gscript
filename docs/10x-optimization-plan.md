# 10x Optimization Plan

## 当前状态

21 个 benchmark 全部正确。但只有 3 个被 trace JIT 编译加速：
- mandelbrot (5.5x) — 纯 float 算术 FORLOOP
- fibonacci_iterative (2.9x) — 纯 int 算术 FORLOOP
- math_intensive/leibniz (8.6x) — 纯 float 算术 FORLOOP

其余 18 个运行在 interpreter 速度（~1x）。

## 为什么 18 个 benchmark 没被编译

### 阻塞分类

| 阻塞原因 | 影响的 benchmark | 数量 |
|----------|----------------|------|
| **call-exit hang bug** (SSA_CALL) | spectral_norm, mutual_recursion, closure_bench, method_dispatch, object_creation, binary_trees, sieve(count), sort, fannkuch(perm) | **11** |
| **call-exit hang bug** (SETTABLE) | sieve(count), sort, fannkuch(perm) | **3** (重叠) |
| **while-loop 无 FORLOOP** | sieve(mark), fannkuch(flip) | **2** |
| **2D table access** (non-scalar LOAD_ARRAY) | matmul | **1** |
| **table length** (SSA_TABLE_LEN) | 各种 | (重叠) |

### 根因：call-exit re-entry hang bug

**#1 阻塞器。** `ssaIsIntegerOnly` 第 80-82 行硬拒绝所有含 call-exit 的 trace：
```go
if hasCallExit { return false }
```

这是因为 call-exit re-entry 会导致无限循环。具体机制：

1. Trace 执行到 SSA_CALL → ExitCode=3 → Go handler 执行指令
2. `ctx.ResumePC = nextPC` → trace re-enter
3. Resume dispatch: 用 `CMP + BEQ` 跳到正确的 resume label
4. Resume label: reload ALL registers from memory → 继续执行
5. 到达 FORLOOP → 循环回 loop_top
6. **问题**：如果 FORLOOP 的 store-back 把 index 值写入 memory，但 resume dispatch 在 loop_top 之前...

**实际 hang 场景**：
- sum_primes 外层 `for n := 2; n <= 100000` 是 FORLOOP
- 内层 `for d := 2; d*d <= n; d++` 也是 FORLOOP
- 外层 trace 录了外层循环体（包含内层循环的 FORPREP）
- 内层循环被 skip（full nesting disabled）→ 变成 call-exit
- 外层 trace 有 call-exit（内层循环的 FORPREP 变成 call-exit）
- Call-exit re-entry → resume → 继续外层循环体 → 再次遇到内层 FORPREP → 又 call-exit → 无限

**根因不是 resume dispatch 的 bug，而是嵌套 FORLOOP 被当作 call-exit 处理。** 内层 FORLOOP 不应该是 call-exit — 它应该由 interpreter 完整执行，trace 只需要等它结束。

### 正确的 call-exit 实现

LuaJIT 的 trace 遇到 OP_CALL 时：
1. 保存当前状态到 snapshot
2. 退出 trace (side-exit)
3. Interpreter 执行 CALL（包括被调函数的全部执行）
4. 返回后，FORLOOP 回边重新触发 trace 执行

**GScript 应该做同样的事：call-exit 就是 side-exit。** 不需要 resume dispatch、不需要 re-entry。Interpreter 从 ExitPC 恢复，执行 CALL 指令（包括被调函数），然后 FORLOOP 回边自然重新进入 trace。

当前实现的错误：call-exit 不做 side-exit，而是试图在 Go 层执行指令然后 re-enter trace。这导致：
1. 嵌套循环的 FORPREP 被 Go handler 执行，但 Go handler 不能运行完整的嵌套循环
2. Resume dispatch 跳回 trace 中间，但 register 状态不一致

## 修复方案

### P0: 把 call-exit 改为 side-exit（最高优先级，解锁 11+ benchmarks）

**改动**：

1. **ssa_emit.go**：对 SSA_CALL，emit 和 side-exit 完全相同的代码（store-back + set ExitPC + ExitCode=1 + B epilogue）。不需要 resume label、不需要 resume dispatch。

2. **ssaIsIntegerOnly**：移除 `if hasCallExit { return false }`。所有含 SSA_CALL 的 trace 都可以编译。

3. **删除 resume dispatch 代码**：不再需要。trace 执行是单向的 — 从 loop_top 到 loop_done 或 side_exit。

**效果**：SSA_CALL 变成 side-exit，interpreter 从 ExitPC 恢复，执行 CALL，FORLOOP 回边重新进入 trace。每次 CALL 都退出 trace + 重新进入，overhead 约等于 2 次 guard check。

**解锁**：spectral_norm, mutual_recursion, closure_bench, method_dispatch, object_creation, sieve(count), sort, fannkuch(perm) — **11 个 benchmark**。

### P1: Native STORE_ARRAY（解锁 sieve, sort, fannkuch）

**改动**：

参考已实现的 `emitLoadArray`，实现 `emitStoreArray`：
1. Load table pointer + metatable check + kind check
2. Bounds check (key < len for existing elements，key == len+1 for append)
3. 根据 arrayKind 写入正确的数组：
   - Mixed: 直接 STR 8 bytes
   - Int: EmitUnboxInt → STR 8 bytes
   - Float: FSTRd 8 bytes
   - Bool: extract bool byte → STRB 1 byte
4. 如果 key > len（append）或超出 capacity → side-exit（让 interpreter 处理 grow）

**效果**：sieve 的 `arr[j] = false` 和 sort 的 swap 操作变成 native ARM64。

### P2: While-loop trace 支持（解锁 sieve mark loop, fannkuch flip）

**改动**：

当前 `ssaIsIntegerOnly` 拒绝无 FORLOOP exit 的 trace。While-loop 用 JMP 回边，没有 FORLOOP。

修复：
1. 识别 JMP 回边 trace 的退出条件（loop body 中的 GUARD_TRUTHY 或 comparison guard）
2. 当 guard 失败（条件不满足），side-exit 到 interpreter → 循环结束
3. 当 guard 通过（条件满足），继续执行 → loop_top
4. 需要处理"loop done"的概念不同：FORLOOP 的 loop_done 是 index > limit；while-loop 的 loop_done 是 guard fail

**复杂度**：中等。需要修改 loop exit 检测逻辑。

### P3: 2D table access（解锁 matmul）

**改动**：

`a[i][k]` 的第一个 LOAD_ARRAY 返回 table（non-scalar），当前被拒绝。

修复：
1. 允许 LOAD_ARRAY 产生 table 类型结果
2. LOAD_ARRAY for table → 从 mixed array 加载 NaN-boxed value → check is table → extract ptr
3. 后续 LOAD_ARRAY 用这个 table ptr → 正常的 scalar load

**复杂度**：低。主要是 LOAD_ARRAY 的 type 检查放宽。

### P4: Native TABLE_LEN（简单，解锁多个 benchmark）

**改动**：
```asm
LDR X0, [regRegs, #tableSlot*8]
EmitCheckIsTableFull(X0, ...)
EmitExtractPtr(X0, X0)
LDR X1, [X0, #TableOffArrayLen]  // arrayLen
LDR X2, [X0, #TableOffHashLen]   // hashLen
ADD result, X1, X2                // total length
```

**复杂度**：低。

## 执行顺序

```
P0 (call-exit → side-exit) → 验证 11 benchmark 不 hang
    ↓
P1 (native STORE_ARRAY) → sieve, sort, fannkuch 加速
    ↓
P3 (2D table access) → matmul 加速
    ↓
P4 (native TABLE_LEN) → 各种 benchmark 清理
    ↓
P2 (while-loop) → sieve mark, fannkuch flip
```

## 预期效果

| Benchmark | 当前 | P0 后 | P1 后 | 预期最终 |
|-----------|------|-------|-------|---------|
| mandelbrot | 5.5x | 5.5x | 5.5x | 5.5x |
| fibonacci_iterative | 2.9x | 2.9x | 2.9x | 2.9x |
| math_intensive | 1.1x (leibniz 8.6x) | 1.1x | 1.1x | 1.1x |
| sieve | 1.0x | 1.0x (CALL→exit) | **2-5x** | **5-10x** |
| sort | 1.0x | 1.0x (CALL→exit) | **2-3x** | **3-5x** |
| nbody | 1.0x | 1.0x (field native) | 1.0x | **2-5x** (P0 + field) |
| matmul | 0.8x | 0.8x | 0.8x | **2-5x** (P3) |
| spectral_norm | 0.9x | **1.5-3x** (CALL→exit) | — | **3-5x** |
| fannkuch | 0.8x | 1.0x (CALL→exit) | **2-3x** | **3-5x** |
| closure_bench | 0.9x | **1.5-2x** (CALL→exit) | — | **2-3x** |
| method_dispatch | 1.0x | **1.5-2x** (CALL→exit) | — | **2-3x** |

**关键洞察**：P0（call-exit → side-exit）是最高杠杆的改动。一个简单的改动（删除 resume dispatch，把 SSA_CALL 当 side-exit）解锁 11 个 benchmark。不需要复杂的 re-entry 机制。
