# Optimization Roadmap: 14 项优化的 TDD 实现方案

## 核心原则

**每个优化的实现流程：**
```
1. 写 Layer 5 不变量测试（VM=JIT 的端到端测试，当前 FAIL 或无加速）
2. 写 Layer 4 opcode 测试（验证优化后的 SSA opcode 被编译）
3. 写 Layer 2 micro 测试（验证生成的 ARM64 指令正确）
4. 实现优化
5. 确认步骤 1-3 的测试全 PASS
6. 跑全量 136 测试 + 21 benchmark，确认零 regression
```

**禁止**：直接改代码然后跑全量 benchmark debug。

---

## Phase 1: SSA 优化 Passes（第一梯队，不涉及架构改动）

### 1.1 ConstHoist（常量提升）

**作用**：把 loop body 里的常量加载（SSA_CONST_INT/FLOAT）移到 pre-loop，减少每次迭代的指令数。

**测试先行：**

```go
// Layer 5: 不变量测试 — 已有，确认不 regress
func TestInv1_FloatArith(t *testing.T) {
    assertVMEqualsJIT(t, `result:=0.0; for i:=1;i<=1000;i++ { result=result+3.14 }`, "result")
}

// Layer 4: 新增 — 验证 ConstHoist 把常量移到 pre-loop
func TestOpMatrix_ConstHoist(t *testing.T) {
    tests := []struct{
        name, src, key string; want int64
    }{
        {"INT_const_hoisted", `s:=0; for i:=1;i<=100;i++ { s=s+42 }`, "result", 4200},
        {"FLOAT_const_hoisted", `s:=0.0; for i:=1;i<=100;i++ { s=s+1.5 }`, "result", 150.0},
        {"MULTI_const", `s:=0; for i:=1;i<=100;i++ { s=s+10+20 }`, "result", 3000},
    }
}

// Layer 2: 新增 — 验证 SSA IR 中常量在 LOOP 之前
func TestMicro_ConstHoist_MovedBeforeLoop(t *testing.T) {
    // 构造含 SSA_CONST_INT 在 loop body 的 SSAFunc
    // 调用 ConstHoist()
    // 验证：CONST 指令的 index < LoopIdx
}
```

**实现**：
- 文件：`ssa_opt.go`（新建）
- 函数：`ConstHoist(f *SSAFunc) *SSAFunc`
- 算法：扫描 LoopIdx 之后的 CONST_INT/CONST_FLOAT，移到 LoopIdx 之前，更新所有 Arg1/Arg2 引用
- 验证：Layer 2 测试确认常量位置移动，Layer 5 测试确认结果不变

### 1.2 CSE（公共子表达式消除）

**作用**：相同 Op + 相同 Args 的指令只计算一次。

**测试先行：**

```go
// Layer 5: 不变量测试
func TestInv1_CSE_Benefit(t *testing.T) {
    // dx*dx 出现两次，CSE 应消除第二次
    assertVMEqualsJIT(t, `
        s:=0.0; for i:=1;i<=100;i++ {
            x:=i*0.1; s=s+x*x+x*x
        }`, "result")
}

// Layer 2: 验证 SSA IR 中重复表达式被 NOP 替换
func TestMicro_CSE_DuplicateEliminated(t *testing.T) {
    // 构造含两个相同 ADD_INT(ref_a, ref_b) 的 SSAFunc
    // 调用 CSE()
    // 验证：第二个被替换为 NOP，其使用者引用第一个
}
```

**实现**：
- 文件：`ssa_opt.go`
- 函数：`CSE(f *SSAFunc) *SSAFunc`
- 算法：遍历 loop body，对每条纯指令（arithmetic/const）构造 key = (Op, Arg1, Arg2)，查 map，重复 → NOP + 重写引用
- 约束：只消除 loop body 内的，不跨 side-exit/call-exit 边界

### 1.3 FuseMultiplyAdd（FMADD 融合）

**作用**：`a*b+c` → FMADD 单条 ARM64 指令（4 周期→1 周期）。

**测试先行：**

```go
// Layer 5: nbody 简化版（密集 multiply-add 模式）
func TestInv1_FMA_Physics(t *testing.T) {
    assertVMEqualsJIT(t, `
        vx:=0.0; dx:=1.5; mass:=2.0; mag:=0.01
        for i:=1;i<=1000;i++ { vx = vx - dx * mass * mag }`, "vx")
}

// Layer 4: 验证 FMADD opcode 被使用
func TestOpMatrix_FMA(t *testing.T) {
    // 已有 TestOpMatrix_FusedMulAdd，确认不 regress
}

// Layer 2: 验证 ARM64 FMADD 指令生成
func TestMicro_FMA_EmitsFMADD(t *testing.T) {
    // 构造 MUL_FLOAT + ADD_FLOAT 的 SSAFunc
    // 调用 FuseMultiplyAdd()
    // 验证：MUL 被标记为 absorbed，ADD 变成 FMADD
    // 编译并执行，验证结果正确
}
```

**实现**：
- 文件：`ssa_opt.go`
- 函数：`FuseMultiplyAdd(f *SSAFunc) *SSAFunc`
- 算法：扫描 ADD_FLOAT/SUB_FLOAT，如果 Arg1 或 Arg2 是 MUL_FLOAT 且 MUL 只有这一个 user → 融合为 FMADD/FMSUB
- ARM64：`emitFMADD(dst, mul_a, mul_b, addend)` 在 `ssa_emit.go`
- 需要 `AbsorbedMuls` map 标记被吸收的 MUL

### 1.4 GETGLOBAL native

**作用**：全局变量访问编译为直接内存读取，不走 call-exit。

**测试先行：**

```go
// Layer 5: nbody 简化版
func TestInv_GETGLOBAL_Native(t *testing.T) {
    assertVMEqualsJIT(t, `
        data := {1,2,3,4,5}
        s := 0
        for i := 1; i <= 5; i++ { s = s + data[i] }`, "s")
}

// Layer 4: 验证 GETGLOBAL 不再导致 side-exit
func TestOpMatrix_GetGlobal_Native(t *testing.T) {
    // 循环中读全局变量，JIT 应比 VM 快（不再每次 side-exit）
}

// Layer 2: 验证 SSA_LOAD_GLOBAL 生成 native LDR
func TestMicro_LoadGlobal_Native(t *testing.T) {
    // 构造含 SSA_LOAD_GLOBAL 的 SSAFunc
    // CompileSSA 不应 reject
    // 执行时不应 side-exit
}
```

**实现**：
- SSA builder：`OP_GETGLOBAL` → `SSA_LOAD_GLOBAL`（已有，但编译时走 side-exit）
- Codegen：`emitLoadGlobal` 从 globals 表直接 load（需要 globals 指针在 register）
- 需要在 TraceContext 中添加 globals 指针，prologue 中加载
- `ssaIsIntegerOnly`：移除 SSA_LOAD_GLOBAL 的 call-exit 标记

### 1.5 2D Table Access

**作用**：`a[i][k]` 的第一个 LOAD_ARRAY 返回 table，第二个从该 table 加载 scalar。

**测试先行：**

```go
// Layer 5: matmul 简化版
func TestInv_2DTable(t *testing.T) {
    assertVMEqualsJIT(t, `
        a := {{1,2},{3,4}}
        s := 0
        for i := 0; i < 2; i++ {
            for j := 0; j < 2; j++ {
                s = s + a[i][j]
            }
        }`, "s")
}
```

**实现**：
- `emitLoadArrayTable` 已存在，加载 table pointer 到 memory
- 需要让后续 `emitLoadArray` 能用前一个 LOAD_ARRAY 的 table 结果
- SSA builder 需要把连续的 `GETTABLE(GETTABLE(a, i), k)` 映射为两个 SSA_LOAD_ARRAY

### 1.6 Cold Code Splitting

**作用**：guard fail 跳转路径移到代码末尾，热路径保持紧凑。

**测试先行：**

```go
// Layer 2: 验证 guard fail 分支目标在代码末尾
func TestMicro_ColdSplit_GuardFarJump(t *testing.T) {
    // 编译一个有 guard 的 trace
    // 验证：guard 的 BCond 跳到的 label 在 loop body 之后
    // （可以通过检查 assembler 的 label offset 来验证）
}
```

**实现**：
- `ssa_emit.go`：guard fail 用 `deferCold` 机制——guard 在 hot path emit `BCond label_N`，实际 store-back + exit code 在 cold section emit
- 当前已经这样做了（`side_exit_setup` 在 cold paths），但可以进一步把每个 guard 的 ExitPC 设置也移到 cold path

---

## Phase 2: Trace 录制增强

### 2.1 函数入口 Trace

**作用**：递归函数（fib, ackermann）通过函数入口触发 trace 录制。

**测试先行：**

```go
// Layer 5: fib 必须和 VM 一致
func TestInv_FunctionEntryTrace_Fib(t *testing.T) {
    assertVMEqualsJIT(t, `
        func fib(n) {
            if n < 2 { return n }
            return fib(n-1) + fib(n-2)
        }
        result := fib(20)`, "result")
}

// Layer 5: ackermann
func TestInv_FunctionEntryTrace_Ackermann(t *testing.T) {
    assertVMEqualsJIT(t, `
        func ack(m, n) {
            if m == 0 { return n + 1 }
            if n == 0 { return ack(m-1, 1) }
            return ack(m-1, ack(m, n-1))
        }
        result := ack(3, 4)`, "result")
}

// Layer 3: 函数入口 trace 的 exit state
func TestTraceExec_FunctionEntry_ExitPC(t *testing.T) {
    // 函数入口 trace 在 CALL 处 side-exit
    // ExitPC 应指向 CALL 指令
    // Interpreter 执行 CALL（递归），返回后继续
}
```

**实现**：
- `trace.go`：新增函数调用计数器（`proto.CallCount >= threshold`）
- `OnFunctionEntry(proto, regs, base)` 新方法，在 VM 的 CALL handler 中调用
- 录制从函数第一条指令开始，到 RETURN 结束
- CALL 指令变成 side-exit（和循环内 CALL 一样）
- 需要修改 VM 的 `CallValue` 来 hook 函数入口

### 2.2 Function Inlining

**作用**：trace 录制时，小函数（<=10 条指令）的 CALL 被展开为 inline 指令。

**测试先行：**

```go
// Layer 5: 内联小函数
func TestInv_Inline_SmallFunc(t *testing.T) {
    assertVMEqualsJIT(t, `
        func double(x) { return x * 2 }
        s := 0
        for i := 1; i <= 100; i++ { s = s + double(i) }`, "s")
}

// Layer 4: 验证内联后不再 side-exit
func TestOpMatrix_Inline_NoSideExit(t *testing.T) {
    // double(i) 内联后，循环应比 side-exit 版本快
}
```

**实现**：
- `trace_record.go`：`handleCall` 中检测小函数（body <= N 条指令，无循环，无递归）
- 内联时：emit callee 的指令到 trace IR，用 `Depth > 0` 标记
- SSA builder 处理 `Depth > 0` 的指令（已有部分基础）
- 旧代码的 `inlineCallStack` 机制可参考

### 2.3 WBR Guard Relaxation

**作用**：Write-Before-Read slot 不需要 pre-loop type guard。

**测试先行：**

```go
// Layer 5: WBR slot 不应 guard-fail
func TestInv_WBR_NoGuardFail(t *testing.T) {
    // temp 变量在循环体内先写后读，pre-loop 不需要 guard
    assertVMEqualsJIT(t, `
        s := 0
        for i := 1; i <= 100; i++ {
            tmp := i * 3
            s = s + tmp
        }`, "s")
}
```

**实现**：
- `slot_analysis.go`：`classifySlots` 已有 WBR 分类
- SSA builder：WBR slots 不 emit LOAD_SLOT + GUARD_TYPE
- 需要确保 WBR slots 在 pre-loop 不被 load 到 register
- 测试：在 pre-loop 有更多 slots 被跳过 → 更少的 guard → 更少的 guard-fail 概率

---

## Phase 3: 高级代码生成

### 3.1 Side-exit Continuation

**作用**：频繁 side-exit 的 guard 编译一个 bridge trace，在 native code 中处理 exit path。

**测试先行：**

```go
// Layer 5: mandelbrot escape check 不应频繁 exit/reenter
func TestInv_SideExitContinuation_Mandelbrot(t *testing.T) {
    // 已有 TestInv4_Mandelbrot_Mini
    // 性能测试：JIT 应比 VM 快 5x+
}

// Layer 3: 验证 side-exit 后 bridge trace 编译
func TestTraceExec_SideExitContinuation(t *testing.T) {
    // trace side-exits N 次后，应编译 bridge trace
    // bridge trace 处理 exit path 的计算
    // 再次 exit 时直接走 bridge，不回 interpreter
}
```

**实现**：
- 当一个 guard 的 side-exit 达到阈值（如 50 次），录制从该 exit 开始的 bridge trace
- Bridge trace 编译为新的 CompiledTrace，挂在原 trace 的 guard 上
- Guard fail 时跳到 bridge 而不是 interpreter
- 这是 LuaJIT 和 PyPy 的核心机制

### 3.2 Full Nested Loop Inlining

**作用**：内层循环的指令直接内联到外层 trace，一个 trace 覆盖整个嵌套循环。

**测试先行：**

```go
// Layer 5: 嵌套循环内联
func TestInv_NestedInline(t *testing.T) {
    assertVMEqualsJIT(t, `
        s := 0
        for i := 1; i <= 10; i++ {
            for j := 1; j <= 10; j++ {
                s = s + i * j
            }
        }`, "s")
}

// Layer 2: 验证内联后只有一个 LOOP marker
func TestMicro_NestedInline_SingleLoop(t *testing.T) {
    // 构造嵌套循环 trace
    // 启用 full nesting
    // 验证 SSA IR 中只有一个 SSA_LOOP（内层被展开）
}
```

**实现**：
- `trace_record.go`：`recordNestedForPrep` 启用 full nesting（当前 disabled）
- 内层循环的指令直接追加到 trace IR
- SSA builder 处理 `INNER_LOOP` marker
- 需要处理内层 FORLOOP 的 exit（变成 guard，不是 loop-done）

### 3.3 Sub-trace Calling

**作用**：预编译的内层 trace 通过 BLR 调用，避免内联的代码膨胀。

**测试先行：**

```go
// Layer 5: 嵌套循环用 sub-trace
func TestInv_SubTrace(t *testing.T) {
    // 同 TestInv_NestedInline，但用 sub-trace 而不是 inline
    assertVMEqualsJIT(t, `
        s := 0
        for i := 1; i <= 10; i++ {
            for j := 1; j <= 100; j++ { s = s + 1 }
        }`, "s")
}
```

**实现**：
- 内层循环先独立编译为 CompiledTrace
- 外层 trace 遇到内层 FORPREP 时，emit `BLR inner_trace_code`
- 需要在 TraceContext 中传递 inner trace 的 code pointer
- 旧代码的 `CALL_INNER_TRACE` 机制可参考

### 3.4 Loop Peeling

**作用**：trace 拆成 preamble（一次性 guard + load）+ body（零 guard overhead 循环）。

**测试先行：**

```go
// Layer 5: loop peeling 后结果仍正确
func TestInv_LoopPeeling(t *testing.T) {
    assertVMEqualsJIT(t, `
        s := 0
        for i := 1; i <= 10000; i++ { s = s + i }`, "s")
    // 性能测试：peeled loop 应比 non-peeled 快 20-50%
}

// Layer 2: 验证 preamble 和 body 分离
func TestMicro_LoopPeeling_PreambleBody(t *testing.T) {
    // 编译后的代码应有两段：
    // 1. Preamble: guards + loads + 第一次迭代
    // 2. Body: 无 guard 的循环体 + back-edge
    // 验证：body 中没有 GUARD_TYPE 指令
}
```

**实现**：
- 展开 trace 为两次迭代
- 第一次迭代 = preamble（保留所有 guard）
- 第二次迭代 = body（guard 被 preamble 保证，可消除）
- PHI 节点连接 preamble 和 body 的 loop-carried 值
- 这是最复杂的优化，需要 SSA IR 的 split + PHI insertion

### 3.5 Tail Call Optimization

**作用**：递归尾调用 → 直接 jump 到函数头部，不分配新栈帧。

**测试先行：**

```go
// Layer 5: 尾递归不 stack overflow
func TestInv_TailCall(t *testing.T) {
    assertVMEqualsJIT(t, `
        func sum(n, acc) {
            if n <= 0 { return acc }
            return sum(n-1, acc+n)
        }
        result := sum(100000, 0)`, "result")
}
```

**实现**：
- 需要函数入口 trace（Phase 2.1）作为前置
- 检测 trace 末尾是 `CALL + RETURN` 且 CALL 的结果直接作为 RETURN 值
- 用 `B` 跳回函数头部（preamble）而不是 `BLR` + 新栈帧
- 旧代码的 `emitSelfCall` 机制可参考

---

## 执行时间表

```
Week 1: Phase 1.1-1.4 (ConstHoist + CSE + FMA + GETGLOBAL)
  → 写 12 个新测试
  → 实现 4 个优化
  → 验证 nbody 2-4x, 全面 10-30% 提升

Week 2: Phase 1.5-1.6 + Phase 2.3 (2D table + cold split + WBR)
  → 写 6 个新测试
  → matmul 3-8x, guard overhead 降低

Week 3: Phase 2.1-2.2 (函数入口 trace + function inlining)
  → 写 8 个新测试
  → fib/ackermann 等 7 个 benchmark 解锁

Week 4: Phase 3.1-3.2 (side-exit continuation + nested inlining)
  → 写 6 个新测试
  → 嵌套循环性能大幅提升

Week 5: Phase 3.3-3.5 (sub-trace + loop peeling + tail call)
  → 写 8 个新测试
  → 全面 20-50% 提升
```

## 测试增长预期

| 阶段 | 新增测试 | 累计测试 | 测试覆盖 |
|------|---------|---------|---------|
| 当前 | — | 136 | 基础正确性 |
| Phase 1 | ~18 | 154 | SSA 优化 pass 正确性 |
| Phase 2 | ~14 | 168 | 函数级 trace + guard 优化 |
| Phase 3 | ~14 | 182 | 高级代码生成 |

**每个优化 = 3-4 个新测试（Layer 2 + 4 + 5）+ 全量 regression check。**
