# Snapshot Mechanism Design

## 问题

当前 GScript trace JIT 在 side-exit 时用 `emitSlotStoreBack` 把所有 **写过的** slot 的当前 register 值写回 memory。问题：register 值可能是循环体执行到一半的**中间状态**，不是 interpreter 在 ExitPC 处期望的值。

具体例子（nbody advance()）：
```
bytecode slot 6:
  GETTABLE → slot 6 = bi (table)        ← interpreter 在 pc=17 期望 table
  GETFIELD → slot 6 = bi.x (float)      ← JIT 把 float 写入 slot 6
  ...
  GETFIELD B=6 → 读 slot 6 作为 table   ← side-exit → interpreter 读到 float → crash
```

## LuaJIT 的方案

LuaJIT 在每个 guard 点维护一个 **Snapshot**：slot→IR ref 的稀疏映射。Guard fail 时：
1. 所有 register 保存到 ExitState（栈上 ~1.3KB）
2. `lj_snap_restore` 遍历 snapshot entries
3. 每个 entry 从 ExitState（register 或 spill slot）读取值，写回 Lua stack
4. 只恢复 snapshot 中的 slots（其他 slot 在 Lua stack 上未被 trace 修改）

关键优化：
- **稀疏表示**：只存被修改的 slots（SLOAD 同位置读取的省略）
- **NORESTORE**：给 side-trace 继承用但不需要恢复的 entry
- **Snapshot 合并**：相邻无 guard 的 snapshots 合并为一个
- **Use-def 清理**：dead slots 不进 snapshot
- 每 trace 约 50 个 snapshot，每个约 10 entries，总开销 ~3KB

## GScript 的方案

### 设计原则

1. **最小改动**：复用现有 SSA pipeline，不重写 register allocator
2. **渐进式**：先做 per-side-exit snapshot，不做 per-guard-point
3. **利用现有优势**：GScript 用 forward-pass allocator，register 不会重命名（不需要 RENAME 机制）

### 核心思路

当前 side-exit 有一个共享的 store-back（写回所有 writtenSlots）。改为：**每个 guard/side-exit 有自己的 store-back，只写回在该点 live 的 slots，且使用正确的类型。**

### 数据结构

```go
// Snapshot 记录一个 guard 点的 slot 状态
type Snapshot struct {
    PC      int             // 对应的 bytecode PC（side-exit 后 interpreter 从这里恢复）
    Entries []SnapEntry     // 需要恢复的 slot 列表
}

// SnapEntry 记录一个 slot 在 guard 点的值来源
type SnapEntry struct {
    Slot    int             // VM slot 编号
    Type    SSAType         // Int / Float / Unknown
    Ref     SSARef          // 值来源的 SSA ref（用于确定 register/spill）
    IsConst bool            // 是常量？
    ConstVal int64          // 常量值（如果 IsConst）
}
```

在 SSAFunc 中存储：
```go
type SSAFunc struct {
    Insts          []SSAInst
    Trace          *Trace
    Snapshots      []Snapshot  // 新增：每个 guard 点的 snapshot
    // ...
}
```

### Pipeline 集成

```
Recording → BuildSSA → Optimize → RegAlloc → EmitSSA
                ↓                               ↓
         buildSnapshots()              emitPerGuardStoreBack()
```

#### Phase 1: 构建 Snapshots（在 BuildSSA 之后，EmitSSA 之前）

在 `CompileSSA` 中，遍历 SSA instructions，为每个 guard-like instruction 构建 snapshot：

```go
func buildSnapshots(f *SSAFunc) []Snapshot {
    // slotDefs[slot] = 最近写入该 slot 的 SSARef
    slotDefs := map[int]SSARef{}
    // slotTypes[slot] = 最近写入该 slot 的类型
    slotTypes := map[int]SSAType{}
    var snapshots []Snapshot

    for i, inst := range f.Insts {
        // 跟踪 slot 定义
        if isSlotWrite(inst) {
            slotDefs[int(inst.Slot)] = SSARef(i)
            slotTypes[int(inst.Slot)] = inst.Type
        }

        // 在 guard 点创建 snapshot
        if isGuardPoint(inst) {
            snap := Snapshot{PC: inst.PC}
            for slot, ref := range slotDefs {
                snap.Entries = append(snap.Entries, SnapEntry{
                    Slot: slot,
                    Type: slotTypes[slot],
                    Ref:  ref,
                })
            }
            snapshots = append(snapshots, snap)
            f.Insts[i].AuxSnap = len(snapshots) - 1 // 链接 inst → snapshot
        }
    }
    return snapshots
}
```

Guard 点包括：
- `SSA_GUARD_TRUTHY`（loop body 内的条件 guard）
- In-loop table type checks（LOAD_FIELD/STORE_FIELD 的 inline guard）
- `SSA_SIDE_EXIT`

**不包括** pre-loop `SSA_GUARD_TYPE`（这些 guard fail 时不需要 store-back，因为没有 register 被修改）。

#### Phase 2: 生成 per-guard store-back（在 EmitSSA 中）

替换共享的 `side_exit` label。每个 guard 有自己的 cold stub：

```
// 热代码：
  guard_check → 跳转到 guard_N_exit

// 冷代码（per-guard）：
guard_N_exit:
  // 只 store-back 在这个 guard 点 live 的 slots，用正确的类型
  for entry in snapshot[N].Entries:
    if entry.Type == SSATypeInt:
      EmitBoxInt(reg_for(entry.Ref)) → STR to regs[entry.Slot]
    elif entry.Type == SSATypeFloat:
      FSTRd(dreg_for(entry.Ref)) → to regs[entry.Slot]
    else:
      // table/string：从 memory 读取（memory 中的值是正确的，
      // 因为 call-exit 在执行时已经更新了 memory）
      // 不需要 store-back
  STR X9, ctx.ExitPC
  MOV X0, #1  // ExitCode = side-exit
  B epilogue
```

关键区别：
- **当前**：所有 guard 共享一个 `side_exit` label → 一个 store-back → 所有 writtenSlots 写回
- **新方案**：每个 guard 有自己的 exit stub → 只写回 snapshot 中的 slots → 使用正确类型

### 为什么这能修复 nbody

nbody 的 slot 6 流程：
1. GETTABLE → slot 6 = bi (table)，由 call-exit 写入 memory ✓
2. GETFIELD → slot 6 = bi.x (float)，codegen 把 float 写入 D register + memory
3. 后续 guard 的 snapshot 包含 `{slot:6, type:Float, ref:GETFIELD_ref}`

如果 guard 在 step 3 之后 fail：
- per-guard store-back 把 slot 6 写为 float（正确）
- interpreter 从 ExitPC 恢复
- ExitPC 在 step 3 之后，interpreter 期望 slot 6 是 float ✓

如果 guard 在 step 1 和 step 2 之间 fail（GETTABLE call-exit 之后，GETFIELD 之前）：
- snapshot 中 slot 6 还是 table（GETTABLE 写入的）
- per-guard store-back 不写 slot 6（table 已在 memory 中）
- interpreter 恢复时 slot 6 = table ✓

**不再有 slot 6 被错误类型覆盖的问题。**

### 代码量估算

| 组件 | 行数 | 文件 |
|------|------|------|
| Snapshot 数据结构 | ~30 | ssa_ir.go |
| buildSnapshots | ~80 | ssa_snapshot.go (新文件) |
| emitPerGuardStoreBack | ~100 | ssa_codegen_loop.go (修改 emitSSAColdPaths) |
| 删除共享 side_exit store-back | -30 | ssa_codegen_loop.go |
| 测试 | ~100 | ssa_snapshot_test.go |
| **总计** | **~280 新增** | |

### 执行步骤

1. **定义数据结构**：`Snapshot`, `SnapEntry` in `ssa_ir.go`
2. **实现 `buildSnapshots`**：新文件 `ssa_snapshot.go`
3. **测试 buildSnapshots**：验证 snapshot 内容正确
4. **修改 codegen**：per-guard exit stub 替代共享 side_exit
5. **验证 nbody**：energy 匹配 + 无 crash
6. **跑全量 benchmark**：VM vs JIT 对比

### 风险和缓解

| 风险 | 缓解 |
|------|------|
| Per-guard exit stub 增大代码体积 | 每个 stub ~5 条指令（20 bytes）。50 个 guard = 1KB。可接受。 |
| Snapshot 中 SSARef 对应的 register 可能已被 reallocated | GScript 用 forward-pass allocator，register 分配稳定。不需要 RENAME。 |
| Call-exit 后 register 状态变化 | Call-exit 有 resume label 重新 reload。Snapshot 在 call-exit 后重置。 |
| Table/string 值不在 register | 这些值只在 memory 中。Snapshot 标记 type=Unknown，store-back 跳过（memory 已有正确值）。 |

### 与 P0 的关系

P0（guard analysis）让 trace 能执行（消除不必要的 guard）。Snapshot 让 trace side-exit 安全（恢复正确状态）。两者是互补的。

**执行顺序**：先 snapshot（让 side-exit 安全），再 P0（让更多 trace 执行）。
