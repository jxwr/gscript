# GScript Tracing JIT Architecture Audit

## 日期: 2026-03-18
## 背景: 一天开发，从修 bug 到实现 float SSA，最终 mandelbrot 1.37x

---

## A. 架构层面的问题

### A1. FORLOOP 边界：解释器先 increment，trace 再 increment

**问题**: vm.go:1199 解释器 FORLOOP 先 `idx += step` 写入寄存器，然后才调 `OnLoopBackEdge`。trace 内部的 FORLOOP (SSA ADD_INT) 也做 increment。

**影响**: trace 入口时 idx 已经被解释器递增过了。trace body 先跑（用当前 idx），最后 FORLOOP 再递增进入下一次迭代。这个约定是隐式的，没有文档或 assert 保护。任何重构都可能引入 off-by-one。

**LuaJIT 做法**: trace 从 LOOP header 开始，拥有完整的循环语义（包括 increment）。解释器把控制完全交给 trace，不做"先 increment 再交接"。

### A2. 录制边界模糊且有泄漏

**问题**: `OnLoopBackEdge` 同时处理：热度追踪、开始录制、结束录制、分发已编译 trace。`startBase` 在第一条指令时才惰性设置。`EntryPC` 指向 FORLOOP PC 但第一条录制的 IR 在 body start PC。

**影响**: trace 的 entry point 定义不一致，base 和 constants 的捕获不是原子的。

### A3. SSA 的 slot 模型混淆了 VM 寄存器和值

**这是最根本的问题。**

字节码编译器复用 slot（同一个寄存器号在不同时刻存不同类型的值：先是 zr*zr 的结果，再是 4.0 常量）。SSA builder 的 `slotDefs` 只追踪最新定义，丢失了历史。

导致的连锁问题：
1. `writtenSlots` 机制需要手动追踪谁写了哪些 slot → 5 个独立 bug
2. 常量无法提升出循环（因为常量 slot 被别的指令覆写了）
3. 没有 PHI 节点 → 循环携带值靠"ARM64 寄存器跨 back-edge 保持"的惯例

**LuaJIT 做法**: IR 使用独立编号，与 VM slot 完全解耦。SNAP 指令记录 IR ref → VM slot 的映射。

### A4. Guard 系统：ExitCode=2 是 hack

**问题**: pre-loop guard 失败 → ExitCode=2 → "not executed"。这是为了绕开"guard 失败但 idx 已经被解释器 increment"的问题。

**影响**: 如果 pre-loop guard 之前有任何副作用（目前没有，但没有 enforcement），值会丢失。

### A5. writtenSlots / store-back 机制

**问题**: 手动追踪哪些 slot 需要 writeback。今天 5 个 bug 中有 3 个出在这里。

**正确做法**: 从 SSA IR 自动做 liveness analysis，而不是 ad-hoc 逻辑。

---

## B. Bug 模式分析

### 今天的 bug 分类

| Bug | 根因 | 发现方式 |
|-----|------|---------|
| SSA MOVE 不生成代码 | slot alias 不跨 back-edge | dump R(0) before/after trace |
| TEST guard C=0/C=1 反转 | 注释写错导致实现跟着错 | 对比 trace vs interp 结果 |
| dead code 在 writtenSlots | 手动追踪没考虑不可达代码 | 分析 cond_loop 5 vs 10 |
| float slot 分配到 X 寄存器 | buildSSASlotRefs 绕过了 floatSlots 过滤 | dump 类型发现 TypeInt 覆盖 |
| pre-loop guard exitPC 错误 | guard PC 指向 body 中间 | FORLOOP dump 发现 idx 异常 |

### 有效 vs 无效的调试方法

**有效**: dump 寄存器值 before/after trace → 立即看到问题
**无效**: 读代码推理 → 在 5 层间接调用中迷路

**要固化的规范**: 每次改 codegen，先加 before/after dump，跑最小测试，确认正确后再删 dump。

---

## C. 性能瓶颈分析

### Mandelbrot 内层循环：指令计数

| 类别 | 当前 GScript | 理想 (LuaJIT) | 差距 |
|------|-------------|--------------|------|
| Float 计算 (FMUL/FADD/FSUB) | 7 | 5 (CSE 省 2) | 2 |
| 寄存器 move (FMOV) | 2-3 | 1 | 1-2 |
| **常量重新物化 (2.0, 4.0)** | **6-10** | **0** | **6-10** |
| **临时值内存往返** | **4-8** | **0** | **4-8** |
| Guard PC 加载 (LoadImm64 X9) | 2-4 | 0 (snapshot) | 2-4 |
| FORLOOP | 3-4 | 2-3 | 1 |
| **总计** | **~30** | **~12** | **~18** |

**当前 ~30 条 vs 理想 ~12 条 → 2.5x 指令开销 → 1.37x 加速上限合理**

### 最大的两个浪费

1. **常量重新物化** (~8 条/迭代): `LoadImm64(X0, bits) + FMOVtoFP(Dn, X0)` = 5 条指令 × 2 个常量。根因：slot 复用导致常量不能提升出循环。
2. **临时值内存往返** (~6 条/迭代): 非分配 temp slot 的 `FSTRd + MOVimm16 + STRB` + 后续 `FLDRd`。根因：只有 8 个 D 寄存器，10 个 float slot。

---

## D. 从 1.37x 到 10x 的路径

| 优化 | 加速 | 累积 | 工时 | 风险 | 依赖 |
|------|------|------|------|------|------|
| D1: 常量提升 | 1.2x | 1.2x | 1-2天 | 低 | 无 |
| D2: 全量 float 寄存器分配 | 1.3x | 1.56x | 3-5天 | 中 | 无 |
| D3: CSE (公共子表达式消除) | 1.1x | 1.72x | 2-3天 | 低 | 无 |
| D5: 嵌套循环 tracing | 2.5x | 4.3x | 10-15天 | 高 | D2 |
| D6: Snapshot 替换 store-back | 1.15x | 4.9x | 5-7天 | 中 | 无 |
| D4: LICM (循环不变量外提) | 1.05x | 5.1x | 2-3天 | 低 | 无 |
| D7: 全局变量直接访问 | 1.2x | 6.1x | 3-5天 | 中 | 无 |
| 边界优化 (boxing消除等) | 1.7x | ~10x | 5-7天 | 中 | D5+D6 |

**关键路径**: D1 → D2 → D3 → D5。前三个是低挂果实（6-10天，到 ~1.7x），D5 是跳跃性改变（需要 10-15天，到 ~4x）。

**诚实评估**: 不做嵌套循环 tracing (D5)，mandelbrot 上限约 3-4x。因为每个像素都要 Go→JIT→Go 的函数调用开销。

---

## E. 关于 pass 管线化

当前 `CompileSSA` 一个函数里做了：slot 分配、float 分配、pre-loop guard 发射、loop body codegen（包含 forwarding）、store-back。改一处连锁影响其他。

**应该拆成**:

```
BuildSSA(trace)           → SSAFunc (raw IR)
    ↓
OptimizeSSA(f)            → SSAFunc (guards hoisted, DCE done)
    ↓
ConstantHoist(f)          → SSAFunc (loop-invariant constants promoted) [新]
    ↓
CSE(f)                    → SSAFunc (redundant ops eliminated) [新]
    ↓
RegAlloc(f)               → RegMap (int slots → X regs, float slots → D regs) [新]
    ↓
EmitARM64(f, regmap)      → []byte (pure code emission, no analysis)
```

每个 pass: 输入 SSAFunc → 变换 → 输出 SSAFunc。
- 加新优化 = 加新 pass，不动其他代码
- 每个 pass 可以独立测试
- EmitARM64 变成纯粹的"查寄存器映射 + 发指令"，不做任何分析

**这才是接近 LuaJIT 水准的正确架构。**
