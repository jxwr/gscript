# Guard Analysis Refactor Plan

## 背景

当前 `ssa_guard_analysis.go` 有 4 个 WBR（Write-Before-Read）分析函数，~80% 代码重复，且 `computeLiveIn` 有 numeric/non-numeric 分叉。每次遇到新 benchmark 的 guard fail 都需要 patch 一个新的 opcode 到某个 WBR 函数中。这是架构级问题。

### 当前代码问题

| 函数 | 行数 | 用途 | 问题 |
|------|------|------|------|
| `isWrittenBeforeFirstRead` | 30 | Int slot 的 WBR，不认 float | 拒绝所有非 numeric op 的 slot |
| `isWrittenBeforeFirstReadExt` | 80 | 扩展版，认 GETFIELD | 不认 GETTABLE、MOVE、arithmetic 作为 write |
| `isFloatSlotWBR` | 80 | Float slot 版，认 arithmetic/MOVE | 与 Ext 版 ~90% 重复 |
| `isWrittenBeforeFirstReadImpl` | 100 | 底层实现，两遍扫描 | 第一遍全局 bail-out 太保守 |
| `computeLiveIn` | 100 | 入口，numeric/non-numeric 分叉 | Non-numeric 走 legacy 路径 |
| `computeLiveInLegacy` | 150 | Legacy 路径，per-opcode 收集 | 漏掉 slot（如 GETTABLE dest） |

**根因**：每个函数对 "什么算 write" 有不同的定义。slot 13 在 nbody 中是 GETTABLE 的 dest（`bj = bodies[j]`），但 `isWrittenBeforeFirstReadExt` 不认 GETTABLE 为 write → slot 13 被标记为 live-in → 生成 Int guard → 实际入口是 nil → guard fail → blacklist。

---

## P0: 统一 Slot Liveness 分析（重写核心）

### 目标
用一个函数 `classifySlots(trace *Trace)` 替代所有 WBR 变体。一次正向扫描 trace IR，对每个 slot 输出四种分类之一。

### 分类定义

| 分类 | 含义 | Guard 策略 |
|------|------|-----------|
| **live-in** | 首次引用是 read（包括 memory-indirect，如 GETFIELD B） | 精确 guard：检查确切类型 |
| **WBR-type-stable** | 首次引用是 write，且该 slot 在整个 trace 中类型单一 | 无 guard：安全跳过 |
| **WBR-type-variable** | 首次引用是 write，但 slot 被多种类型使用（如先 int 后 table） | 宽松 guard：只检查 is-numeric（防止 pointer crash） |
| **dead** | 未被任何指令引用 | 无 guard |

### 实现

```go
type SlotClass int
const (
    SlotLiveIn         SlotClass = iota
    SlotWBRStable                        // WBR, single type
    SlotWBRVariable                      // WBR, multiple types
    SlotDead
)

type SlotInfo struct {
    Class       SlotClass
    GuardType   runtime.ValueType   // for live-in: the expected type
    WriteType   runtime.ValueType   // for WBR: the first write's type
    Types       []runtime.ValueType // all types seen (for WBR-variable)
    FirstPC     int                 // PC of first reference
}

func classifySlots(trace *Trace) map[int]*SlotInfo
```

### 算法

一次正向扫描 `trace.IR`：

```
for each instruction ir:
    for each slot in reads(ir):       // 包括 memory-indirect reads
        if slot not seen:
            mark slot as live-in, record type
        mark slot as seen

    for each slot in writes(ir):
        if slot not seen:
            mark slot as WBR, record write type
        else if slot is WBR:
            if write type != recorded type:
                upgrade to WBR-type-variable
        mark slot as seen
```

`reads(ir)` 和 `writes(ir)` 是统一的函数（复用现有的 `getReadSlots` / `getWriteSlots`，但补全缺失的 opcode）。

**关键**：`reads(ir)` 必须包含 memory-indirect reads：
- GETFIELD B=slot → reads slot as table
- SETFIELD A=slot → reads slot as table
- GETTABLE B=slot → reads slot as table
- SETTABLE A=slot → reads slot as table

`writes(ir)` 必须包含所有写目标：
- 所有 arithmetic (A=dest)
- MOVE (A=dest)
- GETFIELD (A=dest)
- GETTABLE (A=dest)
- GETGLOBAL (A=dest)
- CALL (A=dest)
- LOADK/LOADINT/LOADBOOL/LOADNIL (A=dest)
- FORLOOP (A=idx, A+3=loop var)
- FORPREP (A=idx)

### Guard 生成

在 `build()` 中（SSA builder），用 `SlotInfo.Class` 决定是否生成 `GUARD_TYPE`：

```go
info := slotClassification[slot]
switch info.Class {
case SlotLiveIn:
    emit GUARD_TYPE(slot, info.GuardType)   // 精确 guard
case SlotWBRVariable:
    emit GUARD_NUMERIC(slot)                // 只检查 is-numeric（防止 pointer crash）
case SlotWBRStable, SlotDead:
    // no guard
}
```

### 删除的代码
- `isWrittenBeforeFirstRead` → 删除
- `isWrittenBeforeFirstReadExt` → 删除
- `isWrittenBeforeFirstReadImpl` → 删除
- `isFloatSlotWBR` → 删除
- `computeLiveInLegacy` → 删除
- `computeLiveIn` numeric/non-numeric 分叉 → 改为调用 `classifySlots`
- 假 `ssaBuilder` 对象构造 → 删除

### 影响文件
- `ssa_guard_analysis.go` — 重写核心（~600 行 → ~200 行）
- `ssa_builder.go` — `build()` 中 guard 生成逻辑改用 `classifySlots`
- `ssa_codegen_array.go` — `findWBRFloatSlots` 改用 `classifySlots`
- `ssa_codegen_loop.go` — `emitSSAPreLoopGuards` 和 `computeDeadGuardSlots` 简化

### 风险
- **中等**：guard 策略变化可能引入 correctness regression。每个 benchmark 必须对比 VM vs JIT 结果。
- **缓解**：TDD——先写测试覆盖所有已知的 slot 复用场景（nbody slot 13、SetField obj/count、matmul table/value），然后实现。

---

## P1: 修复 use-def 对 memory-indirect 访问的追踪

### 目标
让 codegen 层的 `computeDeadGuardSlots`（SSA use-def 级别）正确处理 LOAD_FIELD/STORE_FIELD 的 memory-indirect slot 访问，不再限制为 FORLOOP control slots。

### 实现

在 `SSAInst` 中新增字段标记 memory-indirect slot 访问：

```go
type SSAInst struct {
    // ... existing fields ...
    MemReadSlots []int  // slots read from memory (not via SSA refs)
}
```

在 SSA builder 的 `convertTableOp` 中，为 LOAD_FIELD/STORE_FIELD/LOAD_ARRAY/STORE_ARRAY 填充 `MemReadSlots`：

```go
case OP_GETFIELD:
    inst.MemReadSlots = []int{ir.B}  // reads table from slot B via memory
```

在 `computeDeadGuardSlots` 中，扫描 loop-body instructions 的 `MemReadSlots`，排除被 memory 访问的 slot：

```go
memAccessedSlots := make(map[int]bool)
for i := loopIdx + 1; i < len(f.Insts); i++ {
    for _, slot := range f.Insts[i].MemReadSlots {
        memAccessedSlots[slot] = true
    }
}
// Don't eliminate guards for memory-accessed slots
```

### 影响文件
- `ssa_ir.go` — SSAInst 新增 MemReadSlots 字段
- `ssa_builder.go` — convertTableOp 填充 MemReadSlots
- `ssa_codegen_loop.go` — computeDeadGuardSlots 使用 MemReadSlots，移除 FORLOOP-only 限制

### 风险
- **低**：新增字段不影响现有功能。只是让 dead guard elimination 更精确。

---

## P2: spillIfNotAllocated 类型感知

### 目标
`spillIfNotAllocated`（在 `ssa_codegen_resolve.go`）当 slot 没有被分配寄存器时，将 slot 的值从内存 spill 到 side-exit 的 store-back 中。当前无条件调用 `EmitBoxInt`，但 slot 可能持有 float 或 table。

### 实现

使用 `liveInfo.SlotTypes` 选择正确的 box 函数：

```go
func spillIfNotAllocated(asm *Assembler, slot int, regMap *RegMap, sm *ssaSlotMapper, liveInfo *LiveInfo) {
    if _, ok := regMap.Int.slotToReg[slot]; ok {
        return // already allocated
    }
    typ := liveInfo.SlotTypes[slot]
    switch typ {
    case SSATypeFloat:
        // load from memory, box as float, store back
        EmitBoxFloat(asm, ...)
    case SSATypeInt:
        EmitBoxInt(asm, ...)
    default:
        // raw copy (table, string, etc.)
    }
}
```

### 影响文件
- `ssa_codegen_resolve.go` — `spillIfNotAllocated` 改为类型感知
- `liveness.go` — 确认 `SlotTypes` 包含所有 written slots

### 风险
- **低**：仅影响 side-exit store-back 路径。正常执行不受影响。

---

## P3: Guard fail 策略从绝对阈值改为 ratio-based

### 目标
当前 `guardFailBlacklistThreshold = 5`：连续 5 次 guard fail 就 blacklist。对于首次进入时 slot 类型不稳定但后续稳定的场景（如 advance() 前几次调用 slot 13 是 nil，后来稳定为 table），这太激进。

### 实现

```go
type CompiledTrace struct {
    // ... existing fields ...
    guardFailCount   int
    guardPassCount   int
    totalAttempts    int
}

// In executeTrace:
case 2: // guard fail
    ct.guardFailCount++
    ct.totalAttempts++
    // Only blacklist if fail rate > 90% AND enough samples
    if ct.totalAttempts >= 50 && float64(ct.guardFailCount)/float64(ct.totalAttempts) > 0.9 {
        ct.blacklisted = true
    }

case 0, 1, 3: // success / side-exit / call-exit
    ct.guardPassCount++
    ct.totalAttempts++
    ct.guardFailCount = 0  // reset consecutive count (keep ratio tracking)
```

### 影响文件
- `trace_exec.go` — CompiledTrace 新增 counter，executeTrace 改用 ratio

### 风险
- **低**：只影响 blacklisting 策略，不影响 correctness。最差情况是某些 trace 不被 blacklist 导致重复 guard fail（性能略差但不会 crash）。

---

## P4: 代码去重

### 目标
P0 完成后，删除所有 WBR 变体和 legacy 路径。提取共享函数。

### 实现

1. `getReadSlots` / `getWriteSlots` 已经存在且复用。确认补全后，P0 的 `classifySlots` 直接使用它们。
2. 删除 `isWrittenBeforeFirstRead*` 系列（4 个函数）。
3. 删除 `computeLiveInLegacy`。
4. 删除 `isSlotWBR`（在 `ssa_codegen_analysis.go`）和 `findWBRFloatSlots`——P0 的分类结果直接提供这些信息。
5. 将 `computeCalleeOnlySlots` 集成到 `classifySlots` 中（callee-only slots 标记为 dead）。

### 影响文件
- `ssa_guard_analysis.go` — 大幅简化
- `ssa_codegen_analysis.go` — 删除 `findWBRFloatSlots`, `isSlotWBR`
- `ssa_codegen_loop.go` — `emitSSAPreLoopGuards` 简化（不再需要 wbrFloatSlots map）

### 风险
- **无**（纯删除已不使用的代码）。

---

## P5: OP_UNM float 支持

### 目标
`OP_UNM`（unary minus）当前在 SSA builder 中只处理 int。对于 float operand（如 `sign = -sign`），应该生成 `SSA_NEG_FLOAT`。

### 实现

在 `ssa_builder.go` 的 `convertIR` 中，OP_UNM case 添加 float 分支：

```go
case vm.OP_UNM:
    ref := b.getSlotRef(ir.B)
    if b.slotType[ir.B] == SSATypeFloat {
        b.emit(SSAInst{Op: SSA_NEG_FLOAT, Arg1: ref, Slot: int32(ir.A)})
    } else {
        b.emit(SSAInst{Op: SSA_NEG_INT, Arg1: ref, Slot: int32(ir.A)})
    }
```

ARM64 codegen 已有 `FNEG` 指令支持（在 `assembler_float.go`）。`emitSSAFloatArith` 需要添加 `SSA_NEG_FLOAT` case。

### 影响文件
- `ssa_builder.go` — OP_UNM float 分支
- `ssa_codegen_emit_arith.go` — SSA_NEG_FLOAT emission
- `ssa_ir.go` — 确认 SSA_NEG_FLOAT 已存在（可能需要新增）

### 风险
- **低**：新增能力，不改变现有行为。

---

## 执行顺序

```
P0 → P4 → P1 → P3 → P2 → P5
```

1. **P0 先做**（核心问题：统一 liveness 分析），这是所有后续工作的基础
2. **P4 紧跟**（趁热删除旧代码，防止两套并存）
3. **P1**（use-def 层面的 guard elimination 依赖 P0 的正确 liveness 分类）
4. **P3**（guard fail 策略是 P0 的安全网）
5. **P2**（spillIfNotAllocated 是 side-exit 路径，优先级较低）
6. **P5**（独立功能，随时可做）

### 每步验证

每个 P 级完成后：
1. `go test ./internal/jit/ -count=1` — 全部通过
2. `go test ./tests/ -count=1 -run TestJIT` — 集成测试通过
3. 对比 VM vs JIT 结果：fib, sieve, mandelbrot, nbody, matmul, sort, binary_trees, ackermann, fannkuch, sum_primes
4. **nbody 必须不 guard-fail**（P0 的核心目标）
