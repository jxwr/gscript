# P0 Execution Plan: Unified Slot Liveness Analysis

## 业界调研

### LuaJIT 2 (Mike Pall)
- **SLOAD + TYPECHECK**: 每个从 stack slot load 的 SSA 指令自带 type guard flag。只有被 READ 的 slot 才生成 SLOAD。WBR slot 根本不 SLOAD → 天然没有 guard。
- **PHI + type comparison**: loop back-edge 比较 entry/exit type map。允许 int↔float 转换。不兼容类型 → abort trace。
- **Snapshot 机制**: guard fail 时从最近 snapshot 恢复 interpreter state。不需要 per-guard state。
- **核心设计**: guard 是 per-SSA-ref 的（跟着 SLOAD 走），不是 per-slot 的。

### PyPy RPython
- **Loop peeling**: 展开一次迭代，分成 preamble（一次性）+ loop body（重复）。invariant guard 只在 preamble，loop body guard 减半。
- **Bridge trace**: guard fail 200 次后编译 bridge trace（side-exit trace），不是 blacklist。
- **Type instability**: 通过 bridge → new specialized loop 解决，不是放弃。

### TraceMonkey (Mozilla)
- **Entry type map**: 所有 slot 全量验证类型。不匹配 → 找 peer tree 或新建 trace。
- **被废弃原因**: JavaScript 类型太不稳定，constant mode switching overhead，trace explosion。
- **教训**: 全量 type map 验证太重。LuaJIT 的 per-ref guard 更高效。

### GScript 应该学什么
**LuaJIT 的 SLOAD 策略**: 只为实际 read-before-write 的 slot 生成 guard。WBR slot 不需要 guard 也不需要 pre-loop load。这就是 P0 的目标。

---

## 回顾：5 轮失败的原因

| 轮次 | 做法 | 结果 | 真实失败原因 |
|------|------|------|------------|
| 1 | computeDeadGuardSlots 只做 FORLOOP control slots | 测试过，nbody 没改善 | 太保守，没解决 slot 13 |
| 2 | 消除所有 non-live-in guards (Int/Float only) | SetField/ArrayIntAccess/Mandelbrot 失败 | 混淆了 SSA ref user 和 memory-indirect reader（LOAD_FIELD 从 memory 读 table pointer，不通过 SSA ref） |
| 3 | 统一 classifySlots 替代所有 WBR 变体 | mandelbrot + ArrayIntAccess 失败 | 两个问题：(a) numeric trace 的 float slot 处理不一致 (b) table-base slot 的 memory read 问题 |
| 4 | isWrittenBeforeFirstReadExt 加 MOVE | 单元测试过，nbody crash | **误以为 SSA_MOVE 不写 memory。实际它通过 spillIfNotAllocated 写 memory。crash 原因未确认——可能来自其他 trace 或 Method JIT 交互。** |
| 5 | 统一 classifySlots + 各种 override (table-base/float) | 反复 crash | 在 3 和 4 的基础上叠加更多 patch，复杂度失控 |

### 根因分析

**每次失败的共同模式**: 改了 guard generation 但没同步改 store-back。

Guard 消除意味着：
1. ✅ 不生成 pre-loop GUARD_TYPE
2. ❌ 但 pre-loop LOAD_SLOT 仍然执行 → register 被加载
3. ❌ store-back 仍然写回 → register 值覆盖 memory 中的正确值

正确的做法（LuaJIT 的方式）：WBR slot 连 SLOAD 都不生成 → register 不被分配 → 没有 store-back 问题。

### 轮次 4 的重新分析

关键证据（ssa_codegen_emit.go:146-183）：
```go
// emitSSAMove: Int → spillIfNotAllocated (写 memory)
//              Float → FSTRd (写 memory)
//              Unknown → full copy (写 memory)
```

**SSA_MOVE 写 memory。** 所以 MOVE 是合法的 memory write，可以作为 WBR write。

轮次 4 加 MOVE 后单元测试全过但 nbody crash——crash 原因需要重新诊断。最可能的原因：
- 不是 trace JIT 的 guard analysis 问题
- 是 Method JIT 或 nested trace execution 的交互问题
- 或者是其他 trace（不是 `for j`）的 guard 被错误消除

---

## 执行计划

### Step 0: 确认轮次 4 的 crash 来源 [预计 15 分钟]

**目标**: 确切知道 nbody `--jit` 模式 crash 是来自哪个组件。

**操作**:
1. 在 `isWrittenBeforeFirstReadExt` 中加 MOVE 为 write
2. Build 并运行: `GOTRACEBACK=crash ./gscript --jit benchmarks/suite/nbody.gs`
3. 如果是 Go panic: 看 stack trace 确认是哪个函数
4. 如果是 Lua runtime error: 在 VM 的 GETFIELD/GETTABLE handler 加 log 看是哪个 slot

**验证标准**: 写出 "crash 发生在 [文件:行号]，因为 slot [N] 在 [上下文] 中持有 [类型] 而不是 [预期类型]"

**关键**: 这一步**只诊断不修复**。不管结果如何，继续下一步。

### Step 1: 在 SSA builder 层实施 LuaJIT 式 guard 生成 [预计 30 分钟]

**目标**: 只为 read-before-write slots 生成 LOAD_SLOT + GUARD_TYPE。WBR slots 完全不 emit。

**具体改动**:

#### 1a: 修改 `isWrittenBeforeFirstReadExt` 的 write list
文件: `ssa_guard_analysis.go`

添加所有产生 memory write 的 opcodes（有代码证据支持每个都写 memory）：
```go
case vm.OP_MOVE:      // SSA_MOVE 通过 spillIfNotAllocated 写 memory (codegen_emit.go:168)
    isWrite = (ir.A == slot)
case vm.OP_ADD, vm.OP_SUB, vm.OP_MUL, vm.OP_MOD, vm.OP_DIV, vm.OP_UNM:
    // SSA arithmetic 通过 spillIfNotAllocated 写 memory (同理)
    isWrite = (ir.A == slot)
case vm.OP_LEN:       // 写到 A
    isWrite = (ir.A == slot)
```

**验证**: `go test ./internal/jit/` 全过

#### 1b: 确保 `computeLiveInLegacy` 对 table-base slots 正确

关键约束：即使 MOVE/arithmetic 是 WBR write，table-base slots 的 guard 仍然需要。因为：
- GETFIELD B / GETTABLE B 作为 table base 被 `computeLiveInLegacy` 的 `OP_GETFIELD` case 处理
- 该 case 调用 `isWrittenBeforeFirstReadExt(ir.B)`
- `ir.B` 是 table base slot，它的 first ref 是 READ（作为 table base），不是 write
- 所以 `isWrittenBeforeFirstReadExt` return false → 保留 guard

**唯一风险**: 如果一个 slot 先被 MOVE/arithmetic 写（first ref = write），然后被 GETFIELD B 读作 table base（second ref = read）。这种情况下 `isWrittenBeforeFirstReadExt` return true → 不 guard。但这个 slot 在 first-ref write 后持有 non-table 值，GETFIELD 读到 non-table → crash。

**需要检查**: nbody 中是否有这种模式。从 IR dump 看：
```
IR 1: MOVE A=13, B=10    → writes slot 13 (int j)
IR 2: GETTABLE A=11, B=12, C=13  → reads slot 13 as KEY (not table base!)
```
Slot 13 是 GETTABLE 的 C（key），不是 B（table base）。`computeLiveInLegacy` 对 GETTABLE 的处理：B 和 C 分别检查。C（key slot）走 `isWrittenBeforeFirstReadExt`。

GETTABLE C=13 is a **read** of slot 13 in `isWrittenBeforeFirstReadExt` (line 472-475):
```go
case vm.OP_GETTABLE:
    if ir.B == slot || (ir.C < 256 && ir.C == slot) {
        isRead = true
    }
```
So GETTABLE C=13 → isRead = true. But MOVE A=13 comes **before** GETTABLE C=13 in the IR. So the scan order is:
1. IR 1: MOVE A=13 → isWrite check: YES → return true (WBR)

Wait, the read check happens FIRST in the function:
```go
for _, ir := range b.trace.IR {
    // Check reads first
    isRead := false
    switch ir.Op {
    case vm.OP_GETTABLE:
        if ir.B == slot || (ir.C < 256 && ir.C == slot) {
            isRead = true
        }
    }
    if isRead { return false }

    // Then check writes
    isWrite := false
    ...
    if isWrite { return true }
}
```

For IR 1 (MOVE), the READ check for GETTABLE doesn't apply (ir.Op is MOVE, not GETTABLE). The WRITE check: MOVE A=13 → isWrite = true → **return true**.

MOVE comes before GETTABLE in the trace → function returns true at MOVE → slot 13 is WBR → no guard.

This is correct behavior! MOVE writes j to slot 13, then GETTABLE reads slot 13 as key. The value is correct.

But the **previous crash** happened. So either:
1. The crash is from a different slot/trace
2. Store-back corruption

**This is why Step 0 is critical.**

#### 1c: 修改 `computeDeadGuardSlots` 同步

当 `computeLiveIn` 不为某个 slot 生成 guard 时，`computeDeadGuardSlots` 也应该标记该 slot 为 dead（跳过 pre-loop load 和 store-back）。

当前 `computeDeadGuardSlots` 用 SSA use-def 分析，独立于 `computeLiveIn`。这导致两者不一致。

**修改**: `computeDeadGuardSlots` 改为直接使用 `SSAFunc.SlotClassification`（如果可用）或比较 SSA 中的 GUARD_TYPE 指令集和 `liveIn` map。

最简单的方式：在 SSA builder 的 `build()` 中，为 non-live-in slots 不生成 LOAD_SLOT（也不分配 register）。这样 codegen 层的 `computeDeadGuardSlots` 就不需要了——没有 GUARD_TYPE 就没有东西需要消除。

**这就是 LuaJIT 的方式：WBR slot 不 SLOAD → 不分配 register → 不 store-back。**

### Step 2: 验证 [预计 20 分钟]

1. `go test ./internal/jit/ -count=3` — 全过，无 flaky
2. `go test ./tests/ -run TestJIT -count=1` — 集成测试全过
3. 对比 VM vs JIT：
```bash
for f in fib sieve mandelbrot ackermann matmul nbody sort sum_primes binary_trees fannkuch closure_bench string_bench; do
  vm=$(/tmp/gscript_bench --vm benchmarks/suite/$f.gs 2>&1 | tail -1)
  jit=$(/tmp/gscript_bench --jit benchmarks/suite/$f.gs 2>&1 | tail -1)
  echo "$f: VM=$vm | JIT=$jit"
done
```
4. nbody energy 值必须匹配
5. nbody 时间必须 < 1.0s（如果 trace 真正执行了，应该有显著加速）

### Step 3: 处理 store-back corruption（如果 Step 2 有 crash）[预计 30 分钟]

基于 Step 0 的诊断结果，修复具体的 store-back corruption。

可能的修复：
- `emitSlotStoreBack` 中，对 WBR slots（non-live-in）跳过 store-back
- 这需要把 `liveIn` map 传递到 codegen 层（通过 `SSAFunc.SlotClassification` 或直接存入 SSAFunc）

### Step 4: 清理 [预计 15 分钟]

删除已不使用的旧代码：
- `computeLiveInLegacy`
- `isWrittenBeforeFirstRead` / `isWrittenBeforeFirstReadImpl` / `isFloatSlotWBR`
- 假 ssaBuilder 对象构造
- `computeLiveInNumeric` 和 numeric/non-numeric 分叉

统一为一个 `computeLiveIn` 实现。

---

## 关键约束

1. **每一步之后全量测试** — 不允许跳过测试
2. **不改方向** — Step 0 的诊断结果可能改变 Step 1 的具体做法，但大方向不变
3. **WBR = 不生成 LOAD_SLOT + GUARD_TYPE** — 这是 LuaJIT 验证过的设计
4. **table-base slots 通过 read check 自然保留 guard** — 不需要特殊处理
5. **float slots 通过 read check 自然保留 guard** — float 作为 arithmetic operand 的 first ref 是 read
