# JIT Rewrite: Architecture Design

## 目标

1. 通过所有现有测试（49+ JIT unit tests + 14 integration tests + 21 benchmarks）
2. 所有 benchmark 结果正确（VM vs JIT 完全一致）
3. 性能超过 main 分支（特别是 float-heavy benchmarks: nbody, matmul, mandelbrot）

## 当前架构的致命缺陷

| 问题 | 根因 | 影响 |
|------|------|------|
| Guard 消除不安全 | SSA value ≡ VM slot，消除 guard 同时消除了 register 分配 | nbody guard-fail → blacklist |
| Store-back 类型错误 | slot 复用：同一 slot 先 table 后 float，store-back 用错 box 函数 | slot 6/12 corruption |
| Write-through + call-exit 冲突 | write-through 把 trace 中间状态写入 memory → call-exit 读到错误值 | GETTABLE 读 float 而不是 table |
| Call-exit store-back 覆盖 table | store-back 把 LOADK 的 float 写入 slot 12 → GETTABLE 读到 float | nbody crash |

**共同根因**：SSA values 和 VM slots 是 1:1 绑定的。解绑它们是唯一正确的修复。

## 新架构

### 核心抽象

```
VM Slot = 一个 8-byte 的内存位置 (regs[base+N])
SSA Value = 一个独立的数据值，有自己的 type 和 register

slot → value 的映射在编译时跟踪，运行时通过 snapshot 恢复。
一个 slot 在不同时间点可以持有不同的 SSA value。
```

### 模块与数据流

```
                                 ┌─────────────────────┐
      Interpreter ──────────────▶│   Trace Recorder     │
      (VM hot loop detection)    │   trace_recorder.go  │
                                 └────────┬────────────┘
                                          │ TraceIR
                                          ▼
                                 ┌─────────────────────┐
                                 │   Slot Classifier    │
                                 │   slot_analysis.go   │
                                 └────────┬────────────┘
                                          │ SlotClass map
                                          ▼
                                 ┌─────────────────────┐
                                 │    SSA Builder       │
                                 │    ssa_build.go      │──── Snapshots
                                 └────────┬────────────┘
                                          │ SSAFunc (值类型 IR)
                                          ▼
                                 ┌─────────────────────┐
                                 │  Optimization Passes │
                                 │  ssa_opt_*.go        │
                                 └────────┬────────────┘
                                          │ Optimized SSAFunc
                                          ▼
                                 ┌─────────────────────┐
                                 │  Register Allocator  │
                                 │  ssa_regalloc.go     │
                                 └────────┬────────────┘
                                          │ SSARef→Register map
                                          ▼
                                 ┌─────────────────────┐
                                 │  ARM64 Code Gen      │
                                 │  ssa_emit.go         │
                                 └────────┬────────────┘
                                          │ ARM64 machine code
                                          ▼
                                 ┌─────────────────────┐
                                 │  Trace Executor      │
                                 │  trace_exec.go       │
                                 └─────────────────────┘
```

### 保留的基础设施

| 文件 | 理由 |
|------|------|
| assembler*.go | ARM64 指令编码，纯粹无状态，经测试 |
| memory*.go | mmap/W^X，平台特定，正确 |
| trampoline.go | Go→native 调用，汇编实现 |
| value_layout.go | NaN-boxing 常量和 EmitBoxInt/EmitUnboxInt 等 helpers |

### Method JIT 处理

保留当前 method JIT（codegen*.go, executor*.go）不改动。它处理整数 benchmark（fib, ackermann）正确且高效。重写只影响 trace JIT pipeline。

## 详细设计

### 1. Slot Classifier (`slot_analysis.go`)

```go
func classifySlots(trace *Trace) map[int]SlotClass

type SlotClass = int
const (
    SlotLiveIn    = iota  // first ref is read → needs guard
    SlotWBR               // first ref is write → no guard needed
    SlotDead              // never referenced
)
```

**算法**：单次正向扫描 trace IR，跟踪每个 slot 的首次引用（read vs write）。

**reads** 包括所有操作数位置 + table base（GETFIELD B, GETTABLE B, SETFIELD A, SETTABLE A）。
**writes** 包括所有目标位置（A slot of ALL opcodes that produce values）。

同一条指令内 reads before writes。

### 2. SSA Builder (`ssa_build.go`)

核心：维护 `slotValues map[int]SSARef` 跟踪每个 slot 当前持有的 SSA value。

```go
func BuildSSA(trace *Trace, slotClass map[int]SlotClass) *SSAFunc {
    b := &builder{slotValues: map[int]SSARef{}}

    // Pre-loop: LOAD + GUARD for live-in slots
    for slot, class := range slotClass {
        if class == SlotLiveIn {
            ref := b.emit(LOAD_SLOT, slot, type)
            b.emit(GUARD_TYPE, ref, expectedType)
            b.slotValues[slot] = ref
        }
    }

    b.emit(SSA_LOOP)
    b.snap() // snapshot at loop entry

    // Loop body
    for _, ir := range trace.IR {
        b.convertIR(ir)
    }

    return b.func
}
```

**convertIR** 对每条 bytecode：
- 从 `slotValues` 读取操作数的 SSA ref
- Emit SSA 指令产生新的 ref
- 更新 `slotValues[A] = newRef`
- 在 guard points 和 call-exit points 调用 `snap()`

**Snapshot**：
```go
func (b *builder) snap() {
    s := Snapshot{PC: currentPC}
    for slot, ref := range b.slotValues {
        if ref != b.preLoopValues[slot] { // 只记录被修改的
            s.Entries = append(s.Entries, SnapEntry{slot, ref, type})
        }
    }
    b.snapshots = append(b.snapshots, s)
}
```

### 3. Call-Exit 处理（最关键的设计决策）

**问题**：call-exit 让 interpreter 执行一条指令。interpreter 从 memory 读操作数。但 trace 的 register 操作可能把不同的值放入了 register（不在 memory）。

**解决方案**：**call-exit 前不做 store-back。**

理由：
- Trace 的 register-only 操作（LOADK, arithmetic, MOVE）不写 memory
- Memory 保留的是 pre-trace 状态（或前一个 call-exit 的结果）
- Call-exit 指令的操作数从 memory 读 → 读到的是 interpreter 预期的值
- Call-exit 的结果被 interpreter 写入 memory → 后续 trace 从 memory reload

**但**：如果一个 slot 被前一个 call-exit 修改（比如 `GETTABLE A=11` 写了 slot 11），然后被 trace 的 register 操作覆盖（比如 `LOADK A=11`），后续 call-exit 读 slot 11 会读到 GETTABLE 的结果（在 memory），不是 LOADK 的结果（在 register）。

**这是正确的！** 因为 interpreter 按顺序执行，在 call-exit 点，interpreter 还没执行 LOADK，所以 slot 11 应该持有 GETTABLE 的结果。

**唯一需要写 memory 的情况**：loop-done 和 side-exit 时。这时 trace 结束，interpreter 从 ExitPC 恢复。需要 snapshot restore 把当前 SSA values 写回对应的 memory slots。

### 4. Side-Exit 处理

**方案**：与 LuaJIT 一致的 snapshot restore。

```
side_exit_stub_N:
    // Save X9 (ExitPC) already set before guard
    STR X9, ctx.ExitPC
    MOV X0, #1  // ExitCode = side-exit
    B epilogue

// In Go (executeTrace):
case 1: // side-exit
    snap := trace.Snapshots[exitSnapIdx]
    for _, entry := range snap.Entries {
        val := readValueFromRegister(entry.Ref, regAlloc)
        boxAndStore(val, entry.Type, regs[base+entry.Slot])
    }
    return exitPC, true, false
```

**关键**：snapshot restore 在 Go 里做（不是 ARM64 里做）。ARM64 code 只需要保存 ExitPC + ExitCode，然后 return。所有 callee-saved registers 在 epilogue 恢复。Go 代码可以直接读 register values（因为它知道 regAlloc 映射）。

等等，ARM64 epilogue 恢复了 callee-saved registers 后，Go 代码怎么读 trace 的 register values？

**答案**：在 epilogue 之前保存 trace registers 到 ExitState：

```asm
side_exit:
    // Save trace registers to ExitState (on stack or in TraceContext)
    STP X20, X21, [ctx, #ExitStateOffset]
    STP X22, X23, [ctx, #ExitStateOffset+16]
    FSTP D4, D5, [ctx, #ExitStateOffset+32]
    ...
    STR X9, [ctx, #ExitPCOffset]
    MOV X0, #1
    B epilogue
```

然后 Go 代码从 TraceContext 的 ExitState 区域读取：
```go
case 1: // side-exit
    snap := trace.Snapshots[ctx.ExitSnapIdx]
    for _, entry := range snap.Entries {
        reg := regAlloc[entry.Ref]
        var val uint64
        if isGPR(reg) {
            val = ctx.ExitGPR[reg - X20]
        } else {
            val = ctx.ExitFPR[reg - D4]
        }
        regs[base + entry.Slot] = runtime.Value(boxValue(val, entry.Type))
    }
```

### 5. Loop-Done 处理

类似 side-exit，但 snapshot 是最后一个（loop body 末尾）：

```
loop_done:
    // Save registers to ExitState
    STP X20, X21, [ctx, #ExitStateOffset]
    ...
    MOV X0, #0
    B epilogue
```

Go 代码恢复最后一个 snapshot 的 entries 到 memory。

### 6. Register Allocator

**基于 SSA value live range 的 linear scan。**

可用寄存器：
- GPR: X20, X21, X22, X23（4 个 callee-saved）
- FPR: D4, D5, D6, D7, D8, D9, D10, D11（8 个）

算法：
1. 计算每个 SSARef 的 live range [firstDef, lastUse]
2. 按 firstDef 排序
3. Linear scan：分配 register，spill 到 stack 当 register 不够

Spill slots：在 native stack 上分配，用 SP+offset 访问。

### 7. 与现有 Method JIT 的交互

Method JIT 编译函数，在 float 操作处 side-exit。side-exit 后 `JITSideExited = true`。
Trace JIT 接管 side-exited 函数的 hot loops。

交互点：
- `ShouldEnableTrace()` — 检查函数是否适合 trace JIT
- `traceExecDepth` — 防止 trace 嵌套过深
- Method JIT 的 `handleCallExit` 独立于 trace JIT 的 call-exit 机制

## 实现步骤

### Step 1: 删除旧 trace JIT 代码

删除 `internal/jit/` 下所有 `ssa_*.go`（SSA pipeline）、`trace*.go`（trace recorder/executor）、`liveness.go`、`usedef.go`、`callexit_ops.go`。

保留：`assembler*.go`、`memory*.go`、`trampoline.go`、`value_layout.go`、`codegen*.go`、`executor*.go`。

### Step 2: 实现 Slot Classifier

新文件 `slot_analysis.go`。单函数 `classifySlots`。

验证：写单元测试，确认 nbody 的 slot 12/13 正确分类。

### Step 3: 实现 SSA IR + Builder

新文件 `ssa_ir.go`（数据结构）、`ssa_build.go`（builder）。

验证：构建 SSA IR，dump 并检查 snapshot 内容。

### Step 4: 实现 Register Allocator

新文件 `ssa_regalloc.go`。

验证：分配结果合理（4 GPR + 8 FPR），无 spill overflow。

### Step 5: 实现 Code Generator

新文件 `ssa_emit.go`、`ssa_emit_arith.go`、`ssa_emit_table.go`。

包括：pre-loop guards、loop body、call-exit（无 store-back）、side-exit stubs。

验证：简单 int loop（fib_iterative）通过。

### Step 6: 实现 Trace Executor + Snapshot Restore

新文件 `trace_exec.go`。

包括：ExitState 保存、snapshot restore、call-exit handling。

验证：sieve 通过（有 GETTABLE call-exit）。

### Step 7: 实现 Trace Recorder

复用大部分现有代码，修复 GETGLOBAL 延迟捕获。

验证：所有 trace tests 通过。

### Step 8: 实现优化 Passes

复用 const_hoist、CSE、FMA fusion，适配新 SSA IR。

验证：mandelbrot 正确 + 加速。

### Step 9: 端到端验证

所有 49+ 单元测试 + 14 集成测试 + 21 benchmark 正确性。

### Step 10: 性能对比

nbody < 0.5s（当前 1.82s），matmul < 0.3s（当前 1.22s）。

## ExitState 扩展 TraceContext

```go
type TraceContext struct {
    Regs           uintptr
    Constants      uintptr
    ExitPC         int64
    ExitCode       int64
    InnerCode      uintptr
    InnerConstants uintptr
    ResumePC       int64
    ExitSnapIdx    int64    // NEW: which snapshot to use for restore
    // ExitState: saved registers for snapshot restore
    ExitGPR [4]int64     // X20, X21, X22, X23
    ExitFPR [8]float64   // D4-D11
}
```

Total: 原来 56 bytes → 新增 (4*8 + 8*8) = 96 bytes → 总 152 bytes。
