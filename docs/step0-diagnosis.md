# Step 0 Diagnosis: nbody crash root cause

## 观察

加 MOVE 到 `isWrittenBeforeFirstReadExt` write list 后：
- 单元测试全过
- nbody crash: `advance pc=17 op=12(GETFIELD) A=13 B=6 type=number`
- slot 6 应该是 `bi` (table)，实际持有 float (0x65060591962db6d2)
- `jitEntry=true sideExited=true` — Method JIT 编译了 advance() 且已 side-exit

## 排除

1. ❌ **Trace JIT store-back**: slot 6 不在任何 trace 的 WrittenSlots 或 Int/Float register map
2. ❌ **Method JIT pinned register spill**: `findAccumulators` 的 safety check 排除了 GETFIELD/GETTABLE dest
3. ❌ **Guard 消除**: slot 6 的 `isWrittenBeforeFirstReadExt` 返回 false (NOT WBR)，guard 正确保留

## 根因

**slot 复用 + trace 执行暴露中间状态。**

不加 MOVE 改动时：
- slot 13 guard fail → trace 不执行 → 全部 interpreter → 正确但慢
- slot 6 从未被 JIT 损坏（因为 trace 没执行）

加 MOVE 改动后：
- slot 13 WBR → 无 guard → trace 开始执行
- trace 执行中 GETFIELD 把 float 写入 slot 6（bytecode compiler 复用 slot）
- trace side-exit 或 loop-exit 时 slot 6 持有 float（中间状态）
- interpreter 恢复执行 pc=17 (GETFIELD B=6) → 读到 float → crash

## 核心问题

**advance() 的字节码把 `bi` (table) 和 `bi.x` (float) 放在同一个 slot 6。**

bytecode:
```
GETTABLE slot=6, bodies, i     -- slot 6 = bi (table)
GETFIELD slot=13, slot=6, "x"  -- slot 13 = bi.x (float), reads slot 6 as table
GETFIELD slot=14, slot=6, "y"  -- reads slot 6 as table (still valid)
...
SUB slot=12, slot=13, slot=14  -- float arithmetic
...
GETFIELD slot=6, slot=11, "x"  -- slot 6 = bj.x (float!) overwrites bi table!
```

在 trace 录制时，这些指令按顺序执行，interpreter 在每条指令内先读后写，所以一切正确。

但在 trace 编译后，如果 trace side-exit 发生在 GETFIELD slot=6 已经被 float 覆盖之后，interpreter 恢复时 slot 6 是 float 不是 table → crash。

## 修复方向

这是 **LuaJIT 的 snapshot 机制** 解决的问题。每个 guard/side-exit 点有一个 snapshot，记录每个 slot 在那个点应该持有的值。side-exit 时从 snapshot 恢复，而不是从当前 register 状态恢复。

GScript 目前没有 snapshot。store-back 写回的是 loop body 执行到一半的中间状态，可能和 interpreter 预期不一致。

**短期修复**: 让字节码 GETFIELD 的 emitter 在写入 dest slot 之前保存 table base 到 memory（如果 dest == table base slot）。

**长期修复**: 实现 snapshot 机制（P2+ 级别）。
