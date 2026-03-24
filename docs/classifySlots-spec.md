# classifySlots 正确规格

## 核心洞察

Trace IR 层的 "write" ≠ ARM64 codegen 层的 "memory write"。

- MOVE A, arithmetic A: 只写 ARM64 register，不写 regs[A] 内存
- LOADK A, GETTABLE A, GETFIELD A, CALL A: 既写 register 又写 memory

而 LOAD_FIELD/LOAD_ARRAY codegen 从 **memory** 读 table pointer（`regs[slot]`），不是从 SSA ref。

所以 WBR 分析必须区分 memory-write 和 register-write。

## 两级分析

每个 slot 有两个 first-reference：
- **Register first-ref**: 被 MOVE/arithmetic 读写的 slot
- **Memory first-ref**: 被 GETFIELD B / SETTABLE A 读取的 slot（table base）

Guard 策略：
- 如果 slot 的 **memory first-ref** 是 read → MUST guard（as table）
- 如果 slot 的 **register first-ref** 是 read, 且没有 memory read → guard（as the register type）
- 如果 slot 的 both first-refs 是 write → no guard（safe WBR）
- 如果 slot 的 register first-ref 是 write, memory first-ref 是 read → guard（memory read needs correct value）

## 简化规则

**Table-base slots（GETFIELD B, SETTABLE A）永远 guard as Table。** 无论 register 层是否 WBR。

**非 table-base slots 做正常 WBR 分析。** register-only write counts as write。

## 实现

```go
func classifySlots(trace *Trace) map[int]*SlotInfo {
    // 1. 标记 table-base slots
    tableBaseSlots := findTableBaseSlots(trace)

    // 2. 正向扫描
    for each ir in trace.IR:
        for each read slot:
            if not seen → live-in
        for each write slot:
            if not seen → WBR

    // 3. Override: table-base slots 永远是 live-in + Table guard
    for slot in tableBaseSlots:
        if slot in result:
            result[slot].Class = SlotLiveIn
            result[slot].GuardType = TypeTable
        else:
            result[slot] = &SlotInfo{Class: SlotLiveIn, GuardType: TypeTable}

    // 4. Float slots 永远是 live-in (D register 需要 pre-loop load)
    for slot in floatSlots:
        if result[slot].Class != SlotLiveIn:
            result[slot].Class = SlotLiveIn
            result[slot].GuardType = TypeFloat

    return result
}
```

这确保了：
- Table-base slots 总是有 Table guard（即使 MOVE 先写了）→ 不会 crash
- Float slots 总是 live-in（D register 初始化）→ 不会 corrupt
- 纯 int/scalar WBR slots 不生成 guard → 消除 guard fail
