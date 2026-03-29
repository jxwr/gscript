# GScript JIT Master Plan: From 6/21 to 21/21

## 现状

21 个 benchmark 中，只有 6 个的 trace 被编译为原生代码。15 个 benchmark 的 JIT 性能 ≈ 1.0x（等于解释器）。

**已编译（6个）**：table_field_access(11.3x), mandelbrot(5.5x), fibonacci_iterative(3.2x), table_array_access(3.0x), sieve(1.9x), fannkuch(1.2x)

**未编译（15个）**：fib, ackermann, nbody, sort, binary_trees, matmul, spectral_norm, mutual_recursion, method_dispatch, closure_bench, string_bench, object_creation, math_intensive, coroutine_bench, sum_primes

## 设计原则

1. **覆盖优先** — 让 trace 编译为原生代码是第一要义。0x→2x 远比 5x→6x 有价值。
2. **可插拔框架** — 每个优化独立文件、独立测试、独立开关。
3. **持续调研** — 不断扩大优化技术库，架构不够时迭代架构。

---

## Phase 0: 可插拔编译框架

**目标**：将硬编码的 pass chain 重构为注册制 pipeline。

### 当前架构（硬编码）

```go
// trace.go:314-319
ssaFunc := BuildSSA(r.current)
ssaFunc = OptimizeSSA(ssaFunc)
ssaFunc = ConstHoist(ssaFunc)
ssaFunc = CSE(ssaFunc)
ssaFunc = FuseMultiplyAdd(ssaFunc)
```

### 目标架构（注册制）

```go
// ssa_pipeline.go — 新文件

// Pass 是一个编译优化 pass 的接口。
type Pass struct {
    Name    string
    Fn      func(f *SSAFunc) *SSAFunc
    Enabled bool
}

// Pipeline 管理编译 pass 链。
type Pipeline struct {
    passes []Pass
}

// DefaultPipeline 返回默认的编译 pipeline。
func DefaultPipeline() *Pipeline {
    return &Pipeline{
        passes: []Pass{
            {Name: "while-loop-detect", Fn: OptimizeSSA, Enabled: true},
            {Name: "const-hoist",       Fn: ConstHoist,  Enabled: true},
            {Name: "cse",               Fn: CSE,         Enabled: true},
            {Name: "fma",               Fn: FuseMultiplyAdd, Enabled: true},
            // 未来 pass 在这里注册
        },
    }
}

// Run 按顺序执行所有 enabled 的 pass。
func (p *Pipeline) Run(f *SSAFunc) *SSAFunc {
    for _, pass := range p.passes {
        if pass.Enabled {
            f = pass.Fn(f)
        }
    }
    return f
}
```

```go
// trace.go — 修改后
ssaFunc := BuildSSA(r.current)
ssaFunc = r.pipeline.Run(ssaFunc)
```

### 每个 Pass 的标准结构

```
ssa_opt_<name>.go       — 实现：func MyPass(f *SSAFunc) *SSAFunc
ssa_opt_<name>_test.go  — 单元测试：Layer 2 micro + Layer 4 opcode
```

**验收标准**：
- [ ] Pipeline 注册制运行
- [ ] 每个 pass 可以通过 `pipeline.Disable("name")` 独立关闭
- [ ] 全量 136 测试 + 21 benchmark 通过（零 regression）

---

## Phase 1: 覆盖率攻坚 — 从 6/21 到 15/21

**目标**：消除所有不需要架构改动就能修复的编译阻塞。

### 1.1 Native GETGLOBAL（解锁 nbody, matmul, spectral_norm）

**阻塞原因**：`SSA_LOAD_GLOBAL` 当前作为 call-exit 处理，每次迭代退出到解释器。

**根因分析**：之前的 native 实现从常量池加载，导致 nbody hang。实际分析发现 nbody 的 `bodies` 全局变量 assigned once, fields mutated。table 引用不变。hang 的真正原因需要定位（可能是 store-back 策略切换：从 `emitStoreBackTypeSafe` 变成 `emitStoreBack`，或 `hasCallExit` flag 变化导致 reload 逻辑出错）。

**实现方案**：
1. 恢复 `emitLoadGlobal`，但保持 `hasCallExit = true` 不变（即使 GETGLOBAL 是 native，也让 store-back 走 type-safe 路径）
2. 在 `ssaIsIntegerOnly` 中，不再把 `SSA_LOAD_GLOBAL` 计为 call-exit
3. 添加 TDD 测试：单 GETGLOBAL 的 trace、table 类型 GETGLOBAL、循环中多次 GETGLOBAL

**预期**：nbody 1.0x → 2-4x, matmul 解锁

### 1.2 Native TABLE_LEN（解锁 sort 的部分阻塞）

**阻塞原因**：`SSA_TABLE_LEN` 作为 call-exit 处理。

**实现方案**：
1. `emitTableLen`：从 table 结构体直接读 `length` 字段
2. ARM64：`LDR X0, [table_ptr, #length_offset]`（需要确定 runtime.Table 的 length offset）
3. 需要 guard：table 的 kind 必须是 Array（非 Mixed kind 时 length 语义不同）

**预期**：消除 sort 等 benchmark 的一个 call-exit 来源

### 1.3 更多 Intrinsics（解锁 math_intensive）

**阻塞原因**：`math.abs`, `math.floor`, `math.max`, `math.min` 等未被识别为 intrinsic。

**实现方案**：
1. `trace_record.go:recognizeIntrinsic` 添加更多函数
2. `ssa_emit.go:emitIntrinsic` 添加对应 ARM64 指令：
   - `math.abs` → `FABS Dd, Dn`
   - `math.floor` → `FRINTM Dd, Dn`
   - `math.ceil` → `FRINTP Dd, Dn`
   - `math.max(a,b)` → `FMAX Dd, Dn, Dm`
   - `math.min(a,b)` → `FMIN Dd, Dn, Dm`
3. `assembler.go` 添加对应指令编码

**预期**：math_intensive 1.0x → 2-3x

### 1.4 OP_POW 支持

**阻塞原因**：`OP_POW` 未在 SSA op 列表中，trace 直接 reject。

**实现方案**：
1. 常量指数特化：`x^2` → `MUL(x,x)`, `x^3` → `MUL(MUL(x,x),x)`, `x^0.5` → `FSQRT`
2. 通用情况：call-exit 到 `math.pow`（不阻塞编译，只是不 native）
3. SSA builder：`OP_POW` → `SSA_POW` 或直接展开

**预期**：spectral_norm 部分加速

### 1.5 OP_NOT / OP_CONCAT / OP_SELF 支持

**阻塞原因**：这些 opcode 录到 trace 后，在 SSA builder 中走 `default` 分支。

**实现方案**：
- `OP_NOT`：`SSA_NOT` → ARM64 `EOR` 或 `CSINV`
- `OP_CONCAT`：保持 call-exit（字符串操作复杂）
- `OP_SELF`：展开为 `LOAD_FIELD(R(B), RK(C))` + `MOVE(R(A+1), R(B))`

**预期**：method_dispatch, string_bench 部分 trace 可编译

### 1.6 Upvalue 支持（解锁 closure_bench）

**阻塞原因**：`OP_GETUPVAL`/`OP_SETUPVAL` 未录制。

**实现方案**：
1. trace_record：录制 `OP_GETUPVAL` → `SSA_LOAD_UPVAL`
2. SSA builder：`SSA_LOAD_UPVAL` 从 upvalue 数组加载
3. Codegen：通过 TraceContext 传入 upvalue 数组指针，直接 LDR

**预期**：closure_bench 0.9x → 1.5-2x

### Phase 1 验收标准

| Benchmark | 当前 | 目标 | 解锁方式 |
|---|---|---|---|
| nbody | 1.0x | 2-4x | 1.1 GETGLOBAL |
| matmul | 0.7x | 1.5-2x | 1.1 GETGLOBAL + 2D table |
| spectral_norm | 0.9x | 1.5-2x | 1.1 GETGLOBAL + 1.4 POW |
| math_intensive | 1.0x | 2-3x | 1.3 Intrinsics |
| sort | 1.0x | 1.2-1.5x | 1.2 TABLE_LEN（仍有 SSA_CALL 阻塞） |
| method_dispatch | 0.9x | 1.2-1.5x | 1.5 SELF |
| closure_bench | 0.9x | 1.5-2x | 1.6 Upvalue |
| string_bench | 0.8x | 0.9-1.0x | 1.5 CONCAT call-exit（不再阻塞编译） |

**不可解的 benchmark**（需要 Phase 3 架构改动）：
- fib, ackermann, binary_trees, mutual_recursion — 递归，需要函数入口 trace
- coroutine_bench — goroutine/channel，不可 JIT

---

## Phase 2: 内存操作优化链

**前置条件**：Phase 1 完成，大部分 trace 已可编译。
**目标**：优化已编译 trace 的运行效率，特别是 table field 密集型 benchmark。

### 2.1 Alias Analysis（基础设施）

**文件**：`ssa_opt_alias.go`

**作用**：判断两个内存操作是否可能访问同一地址。在 trace JIT 中，这只需要跟踪 `(table_ref, field_index)` 对。

**算法**：
- 两个 LOAD_FIELD/STORE_FIELD 操作：如果 table SSA ref 不同 → 不 alias
- 同一 table 不同 field index → 不 alias
- 同一 table 同一 field → must alias
- LOAD_ARRAY/STORE_ARRAY：key SSA ref 不同 → 可能 alias（保守）

**输出**：`AliasMap` 可被后续 pass 查询。

### 2.2 Load Elimination / Store-to-Load Forwarding

**文件**：`ssa_opt_load_elim.go`

**作用**：消除冗余内存读取。如果 `STORE_FIELD(table, field, val)` 后跟 `LOAD_FIELD(table, field)`，直接用 `val`。

**ROI**：nbody 每次迭代读 `body.x`, `body.vx` 等多次，可消除 30-50% 的 LOAD_FIELD。

**算法**（线性 trace 中很简单）：
```
known = map[(table_ref, field_idx)] → value_ref
for each inst:
    if LOAD_FIELD(t, f):
        if (t, f) in known → replace with known[(t,f)]
    if STORE_FIELD(t, f, v):
        known[(t, f)] = v
    if CALL/side-exit:
        clear known (conservative)
```

**预期**：nbody 2-4x → 4-6x

### 2.3 Full LICM（Loop-Invariant Code Motion）

**文件**：`ssa_opt_licm.go`

**作用**：超越 ConstHoist——把 loop-invariant 的 LOAD_FIELD 也移到 pre-loop。

**条件**：
- LOAD_FIELD 的 table ref 在 pre-loop 定义
- 该 (table, field) 在 loop body 中没有 STORE_FIELD（通过 alias analysis 判断）

**例子**：nbody 中 `body.mass` 在内层循环中不被修改，但每次迭代都 load。LICM 将其提升到 pre-loop。

**预期**：nbody +10-20%, matmul +10-15%

### 2.4 Dead Store Elimination

**文件**：`ssa_opt_dse.go`

**作用**：如果 STORE_FIELD 的值在下次 load 之前被另一个 STORE_FIELD 覆盖，消除第一个 store。

**预期**：5-10% on field-heavy traces

### 2.5 ABC Elimination（Array Bounds Check Hoisting）

**文件**：`ssa_opt_abc.go`

**作用**：FORLOOP 提供 `[init, limit, step]` 范围。如果 array index 是 loop variable 且 `limit <= #array`，将 bounds check 提升到 pre-loop 做一次。

**预期**：sieve +10-15%, table_array_access +5-10%

### Phase 2 验收标准

| Benchmark | Phase 1 后 | Phase 2 后 | 关键优化 |
|---|---|---|---|
| nbody | 2-4x | 4-6x | Load Elim + LICM |
| matmul | 1.5-2x | 2-4x | LICM + ABC |
| spectral_norm | 1.5-2x | 2-3x | Load Elim |
| table_field_access | 11.3x | 13-15x | Load Elim |
| sieve | 1.9x | 2.5-3x | ABC |
| table_array_access | 3.0x | 3.5-4x | ABC |

---

## Phase 3: Trace 架构扩展

**目标**：解锁递归 benchmark + 分支密集型 benchmark。这是最大的架构投资。

### 3.1 函数入口 Trace（解锁 fib, ackermann, binary_trees, mutual_recursion）

**作用**：除了热循环外，热函数也触发 trace 录制。从函数入口录到 RETURN。

**架构改动**：
1. `trace.go`：`OnFunctionEntry(proto, regs, base)` 新 hook
2. VM（`vm.go`）：在 `OP_CALL` handler 中调用 `OnFunctionEntry`
3. 函数入口 trace 的 CALL 指令 → side-exit（和循环内 CALL 相同）
4. 录制在 RETURN 时结束
5. 需要新的 trace key：`funcKey{proto}` 而非 `loopKey{proto, pc}`

**复杂度**：中高。主要是 VM hook + 新的录制终止条件。

**预期**：fib 1.0x → 2-3x, ackermann 0.8x → 1.5-2x

### 3.2 Side-Exit Bridge（解锁 mandelbrot 进一步加速）

**作用**：频繁 side-exit 的 guard 编译 bridge trace，从 guard fail 直接跳到另一段原生代码，不回解释器。

**架构改动**：
1. 每个 guard 添加 exit counter（不是每个 trace）
2. Guard exit > threshold → 录制 bridge trace（从 exit 点开始）
3. Bridge 编译后，patch 原 guard 的 branch target → bridge code
4. 需要 W^X transition 来 patch 已编译代码

**复杂度**：高。代码 patching + bridge 录制。这是 LuaJIT 的核心竞争力。

**预期**：mandelbrot 5.5x → 8-12x

### 3.3 函数内联（解锁 sort, math_intensive 中的函数调用）

**作用**：trace 录制时，小函数的 CALL 被展开为 inline 指令序列。

**架构改动**：
1. `trace_record.go:handleCall`：检测小函数（≤15 条指令，无循环，无递归）
2. 内联：emit callee 指令到 trace IR，用 `Depth > 0` 标记
3. SSA builder 处理 `Depth > 0`（已有部分基础）
4. 需要 callee 的 constants 合并到 trace constants

**预期**：sort 1.0x → 2-3x（比较函数被内联）

### Phase 3 验收标准

| Benchmark | Phase 2 后 | Phase 3 后 | 关键优化 |
|---|---|---|---|
| fib | 1.0x | 2-3x | 函数入口 trace |
| ackermann | 0.8x | 1.5-2x | 函数入口 trace |
| binary_trees | 1.0x | 1.5-2x | 函数入口 trace |
| mutual_recursion | 0.9x | 1.5-2x | 函数入口 trace |
| mandelbrot | 5.5x | 8-12x | Side-exit bridge |
| sort | 1.0x | 2-3x | 函数内联 |

---

## Phase 4: 深度优化

**前置条件**：Phase 3 完成，所有可 JIT 的 benchmark 已编译。
**目标**：逐步逼近和超越 LuaJIT。

### 4.1 Snapshot-Based Deoptimization

**作用**：替换 store-back 机制。guard fail 时从 snapshot 精确重建解释器状态，而非每次循环都 store-back。

**好处**：
- 热路径零 store-back 开销
- 使 allocation sinking 成为可能
- 使更激进的 LICM / dead store elimination 成为可能

**架构改动**：
- `SSAFunc.Snapshots` 已有数据结构，需要实现 reconstruction 逻辑
- `emitStoreBack` 被替换为 `emitSnapshotRestore`（在 exit handler 中执行）
- 需要在 ExitState 中保存所有活跃 register 值

**复杂度**：高。是最大的单点架构改动。

### 4.2 Allocation Sinking / Scalar Replacement

**作用**：循环内创建的 table 如果不逃逸循环，直接用 register 存储其字段。allocation 只在 side-exit 时才执行（从 snapshot 重建）。

**前置**：4.1 Snapshot-Based Deopt

**预期**：binary_trees 1.5-2x → 3-5x, object_creation 0.9x → 2-3x

### 4.3 Strength Reduction

**文件**：`ssa_opt_strength.go`

**作用**：
- `x % 2^n` → `x & (2^n - 1)`（sieve 的 `if i % p == 0`，p 不一定是 2 的幂，但常量除数可以用 reciprocal）
- `x * 2` → `x + x` 或 `LSL #1`
- `x * 2^n` → `LSL #n`
- 乘法 strength reduction：循环中 `i * stride` → 累加 `iv += stride`

**复杂度**：低。纯模式匹配。

### 4.4 GVN（扩展 CSE）

**文件**：`ssa_opt_gvn.go`

**作用**：交换律感知 CSE。`a + b` 和 `b + a` 识别为同一值。

**实现**：CSE hash key 中，交换律操作的 (Arg1, Arg2) 按 min/max 排序。

**复杂度**：低。扩展现有 CSE。

### 4.5 FOLD Engine（代数简化）

**文件**：`ssa_opt_fold.go`

**作用**：在 SSA builder 层面进行代数简化。模式表驱动：
- `x + 0` → `x`
- `x * 1` → `x`
- `x * 0` → `0`
- `x - x` → `0`
- `(x + c1) + c2` → `x + (c1+c2)`
- `-(-x)` → `x`
- 常量折叠：`CONST(3) + CONST(4)` → `CONST(7)`

**复杂度**：中。从 20 条规则开始，逐步扩展到 100+。

### 4.6 Loop Peeling

**文件**：`ssa_opt_peel.go`

**作用**：trace 展开为 preamble（第一次迭代，保留所有 guard）+ body（后续迭代，guard 被 preamble 保证）。

**前置**：4.1 Snapshot-Based Deopt（preamble fail 需要精确 deopt）

**预期**：所有 trace 5-15% 提升（消除循环内 guard）

### 4.7 Register Rematerialization

**文件**：`ssa_regalloc_remat.go`

**作用**：register spill 时，对可重新计算的值（常量、简单表达式），重新计算而非从内存 reload。

**预期**：mandelbrot +3-8%（float register 压力大）

### 4.8 Loop Unrolling

**文件**：`ssa_opt_unroll.go`

**作用**：trace body 展开 2-4 次，消除 branch overhead + 允许跨迭代 CSE。

**预期**：tight loop 5-15%

---

## 完整优化技术清单（按优先级）

| # | 技术 | 文件 | 类型 | Phase | 前置 | 影响 benchmark |
|---|---|---|---|---|---|---|
| 1 | Pipeline 框架 | `ssa_pipeline.go` | 架构 | 0 | 无 | 所有 |
| 2 | Native GETGLOBAL | `ssa_emit.go` | 覆盖 | 1.1 | 0 | nbody,matmul,spectral |
| 3 | Native TABLE_LEN | `ssa_emit.go` | 覆盖 | 1.2 | 0 | sort |
| 4 | 更多 Intrinsics | `ssa_emit.go` | 覆盖 | 1.3 | 0 | math_intensive |
| 5 | OP_POW | `ssa_build.go` | 覆盖 | 1.4 | 0 | spectral_norm |
| 6 | OP_NOT/CONCAT/SELF | `ssa_build.go` | 覆盖 | 1.5 | 0 | method,string |
| 7 | Upvalue 支持 | `ssa_build.go/emit` | 覆盖 | 1.6 | 0 | closure_bench |
| 8 | Alias Analysis | `ssa_opt_alias.go` | 基础 | 2.1 | 0 | 所有 table 操作 |
| 9 | Load Elimination | `ssa_opt_load_elim.go` | 优化 | 2.2 | 8 | nbody,spectral |
| 10 | Full LICM | `ssa_opt_licm.go` | 优化 | 2.3 | 8 | nbody,matmul |
| 11 | Dead Store Elim | `ssa_opt_dse.go` | 优化 | 2.4 | 8 | field-heavy |
| 12 | ABC Elimination | `ssa_opt_abc.go` | 优化 | 2.5 | 0 | sieve,sort,array |
| 13 | 函数入口 Trace | `trace.go/record` | 架构 | 3.1 | 0 | fib,ack,binary_trees |
| 14 | Side-Exit Bridge | `trace_bridge.go` | 架构 | 3.2 | 0 | mandelbrot,sieve |
| 15 | 函数内联 | `trace_record.go` | 架构 | 3.3 | 0 | sort,math_intensive |
| 16 | Snapshot Deopt | `ssa_snapshot.go` | 架构 | 4.1 | 0 | 所有（基础设施） |
| 17 | Alloc Sinking | `ssa_opt_sink.go` | 优化 | 4.2 | 16 | binary_trees,object |
| 18 | Strength Reduction | `ssa_opt_strength.go` | 优化 | 4.3 | 0 | sieve,matmul |
| 19 | GVN | `ssa_opt_gvn.go` | 优化 | 4.4 | 0 | 所有 |
| 20 | FOLD Engine | `ssa_opt_fold.go` | 优化 | 4.5 | 0 | 所有 |
| 21 | Loop Peeling | `ssa_opt_peel.go` | 优化 | 4.6 | 16 | 所有 |
| 22 | Reg Rematerialization | `ssa_regalloc_remat.go` | 优化 | 4.7 | 0 | mandelbrot |
| 23 | Loop Unrolling | `ssa_opt_unroll.go` | 优化 | 4.8 | 0 | tight loops |

---

## 里程碑预期

| 里程碑 | 编译覆盖 | 关键指标 |
|---|---|---|
| Phase 0 完成 | 6/21 | 框架就绪，零 regression |
| Phase 1 完成 | 15/21 | nbody 2-4x, math_intensive 2-3x |
| Phase 2 完成 | 15/21 | nbody 4-6x, 已编译 trace 全面 +20-50% |
| Phase 3 完成 | 19/21 | fib 2-3x, mandelbrot 8-12x, sort 2-3x |
| Phase 4 完成 | 20/21 | 逼近/超越 LuaJIT |

不可 JIT：coroutine_bench（goroutine/channel 本质上不可编译）

---

## TDD 工作流

每个优化项遵循：

```
1. Layer 2 Micro Test — 验证 SSA IR 变换正确
   ssa_opt_<name>_test.go: 构造 SSAFunc → 调用 pass → 验证输出

2. Layer 4 Opcode Test — 验证编译后的 trace 行为正确
   opcode_matrix_test.go: 新增 TestOpMatrix_<Name>

3. Layer 5 Invariant Test — 验证 VM == JIT 端到端
   jit_invariant_test.go: 新增 TestInv_<Name>

4. 实现 Pass
   ssa_opt_<name>.go

5. 注册到 Pipeline
   ssa_pipeline.go: 添加一行

6. 全量 Regression Check
   136 tests + 21 benchmarks，零 regression
```

**禁止**：直接改代码跑 benchmark debug。先写测试，测试先红，实现后变绿。

---

## 实施顺序

```
Phase 0: Pipeline 框架               ← 第一步，一次性投资
  ↓
Phase 1.1: Native GETGLOBAL          ← 最高 ROI，解锁 3 个 benchmark
Phase 1.2: Native TABLE_LEN          ← 简单，1-2 小时
Phase 1.3: 更多 Intrinsics           ← 简单，纯添加
Phase 1.4: OP_POW                    ← 简单
Phase 1.5: NOT/CONCAT/SELF           ← 中等
Phase 1.6: Upvalue                   ← 中等
  ↓
Phase 2.1: Alias Analysis            ← Phase 2 的基础
Phase 2.2: Load Elimination          ← 最高 ROI 优化 pass
Phase 2.3: Full LICM                 ← 依赖 2.1
Phase 2.4: Dead Store Elimination    ← 依赖 2.1
Phase 2.5: ABC Elimination           ← 独立
  ↓
Phase 3.1: 函数入口 Trace            ← 最大架构改动，解锁 4 个 benchmark
Phase 3.2: Side-Exit Bridge          ← 最大性能提升单项
Phase 3.3: 函数内联                  ← 依赖录制改动
  ↓
Phase 4.1-4.8: 深度优化              ← 逐个实施，每个独立测试
```

每完成一个子项：跑全量测试 + benchmark，更新 README.md，写 blog post（如果有 breakthrough）。
