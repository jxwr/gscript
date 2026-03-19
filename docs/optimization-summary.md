# GScript 编译器优化技术完整清单

> 自动生成于 2026-03-19，基于 docs/ 博客、CLAUDE.md、git log 及 internal/jit/ 源码分析。

---

## 一、Trace JIT 优化

Trace JIT 针对热循环（for-loop），录制一次迭代的执行轨迹后编译为 ARM64 原生代码。

### 1.1 SSA IR 基础设施

| # | 优化名称 | 原理简述 | 效果 | 来源 |
|---|---------|---------|------|------|
| 1 | **SSA IR 引入** | 在 TraceIR（字节码录制）和 ARM64 代码生成之间插入 SSA 中间表示，每个变量只赋值一次，支持类型推断和优化变换 | 为所有后续优化提供基础；简单整数循环从 ~15 条指令降至 ~3 条 | Blog #2, commit `83fe96e`, `5f4f82f` |
| 2 | **类型推断 (Type Inference)** | SSA Builder 沿 IR 传播已知类型（Int/Float/Table/String），在编译期确定变量类型 | 消除循环体内的运行时类型检查，实现值 unboxing | Blog #2, `internal/jit/ssa.go` |
| 3 | **整数 Unboxing** | 已知为 int64 的值直接保存在 ARM64 通用寄存器中（如 X20-X24），不包装为 32 字节 Value 结构 | `sum = sum + i` 从 ~12 条指令降至 1 条 ADD | Blog #2, `ssa_codegen.go` |
| 4 | **浮点 Unboxing** | 已知为 float64 的值保存在 SIMD 寄存器（D4-D11）中 | mandelbrot 内循环浮点运算直接在 D 寄存器进行，无需装箱/拆箱 | Blog #3, commit `569b740` |
| 5 | **Guard Hoisting（守卫提升）** | 将类型检查从循环体内移到循环入口之前，循环体内无需重复检查 | 每次迭代减少 2-4 条 guard 指令 | Blog #2, `ssa_codegen.go` |
| 6 | **Use/Def Chains** | 为每条 SSA 指令建立使用-定义链，可快速查询"谁使用了这个值" | 支持 DCE、CSE 等分析 pass | `internal/jit/usedef.go` |
| 7 | **Pass Pipeline 架构** | 将原来 1236 行的 `CompileSSA` 单体函数拆分为独立 pass：BuildSSA → ConstHoist → CSE → RegAlloc → Emit | 消除了 3 个 writtenSlots 相关 bug，使每个优化可独立开发和测试 | Blog #4, commit `e2651b9` |

### 1.2 SSA 优化 Pass

| # | 优化名称 | 原理简述 | 效果 | 来源 |
|---|---------|---------|------|------|
| 8 | **Constant Hoisting（常量提升）** | 将循环体内的 `SSA_CONST_INT`/`SSA_CONST_FLOAT` 移到 `SSA_LOOP` 标记之前，常量只加载一次 | mandelbrot 中 `2.0`、`4.0` 不再每次迭代花 5 条指令重新物化 | Blog #5, commit `cae8d98`, `ssa_const_hoist.go` |
| 9 | **CSE（公共子表达式消除）** | 对循环体内的纯运算指令计算 `(Op, Arg1, Arg2)` 哈希，重复计算替换为对首次结果的引用 | mandelbrot 中 `zr*zr` 和 `zi*zi` 各减少一次重复计算 | Blog #5, commit `cae8d98`, `ssa_cse.go` |
| 10 | **DCE（死代码消除）** | 移除 SSA 图中无引用的指令 | 减少无用指令的代码生成 | Blog #2, `ssa.go` |
| 11 | **冗余类型守卫消除** | 若某寄存器的类型已由先前的类型产出操作（ADD/LOADINT/FORLOOP）确定，跳过后续对同一寄存器的类型检查 | 循环体内减少重复 guard | `trace_opt.go` (`removeRedundantGuards`) |
| 12 | **已知整数寄存器标记** | 对只参与整数运算的寄存器标记为 known-int，跳过类型字节回写 | 每次迭代减少 1 字节写操作 | `trace_opt.go` (`markKnownIntRegs`) |
| 13 | **类型特化 LOAD_ARRAY** | 当 SSA 类型系统知道 `table[i]` 结果为 float 时，直接生成 `LDR D` 加载 8 字节，而非加载完整 32 字节 Value | 更小代码、更少内存访问、更好缓存行为 | Blog #5, commit `cae8d98` |

### 1.3 寄存器分配

| # | 优化名称 | 原理简述 | 效果 | 来源 |
|---|---------|---------|------|------|
| 14 | **频率驱动整数寄存器分配** | 统计 VM slot 在 trace IR 中的出现频率，最热的 5 个 slot 分配到 X20-X24 | 首版分配器，适用于频率分布不均匀的场景 | Blog #3, `ssa_regalloc.go` |
| 15 | **SSA-Ref 级浮点线性扫描寄存器分配** | 基于活跃区间（live range）的线性扫描算法，每个 SSA 值独立分配 D 寄存器（D4-D11），非重叠的临时值可共享同一物理寄存器 | mandelbrot 内循环最大同时活跃 5 个浮点值 → 零溢出；替代频率分配器后性能提升 ~8.5% | Blog #6→#7, commit `8e6bc2e`, `ssa_float_regalloc.go` |
| 16 | **循环携带值合并 (Loop-Carried Coalescing)** | 当 MOVE 写入的 slot 在循环头有 pre-loop ref 时，强制分配相同的 D 寄存器，避免跨迭代的寄存器拷贝 | 消除循环回边的 FMOV 指令 | `ssa_float_regalloc.go` |
| 17 | **Liveness Analysis（活跃性分析）** | 从循环出口到入口反向扫描 SSA IR，计算每个 VM slot 是否在循环体内被修改，替代了 bug 多发的手动 `writtenSlots` 机制 | 消除 3 个 writtenSlots 相关 bug，store-back 精确只回写被修改的 slot | commit `e2651b9`, `liveness.go` |

### 1.4 原生指令编译

| # | 优化名称 | 原理简述 | 效果 | 来源 |
|---|---------|---------|------|------|
| 18 | **原生 GETFIELD/SETFIELD** | 录制时捕获 table 的 skeys 索引，编译为直接内存加载（shape guard + 两次 LDR），无哈希/线性扫描 | nbody 从 0.64x（JIT 反而更慢）恢复到 0.95x；消除了 table op 死亡螺旋 | Blog #5, commit `6226dce` |
| 19 | **原生 GETGLOBAL** | 录制时将全局变量值捕获到 trace 常量池，运行时直接加载 | 消除 `math.sqrt` 等全局函数访问的 side-exit | Blog #5, commit `6226dce` |
| 20 | **math.sqrt → FSQRT 内联指令** | 识别 `math.sqrt` GoFunction 调用，替换为 ARM64 `FSQRT D0, D1` 单条指令 | mandelbrot 每像素省去一次完整函数调用 | Blog #5, commit `6226dce` |
| 21 | **bit32 内联指令** | `bit32.bxor` → `EOR`, `bit32.band` → `AND`, `bit32.bor` → `ORR`, `bit32.bnot` → `MVN`, `bit32.lshift` → `LSL`, `bit32.rshift` → `LSR` | 棋类 AI 的 Zobrist hash 运算从 Go 函数调用变为单条 ARM64 指令 | Blog #1, `trace.go` (Intrinsic constants) |
| 22 | **原生 GETTABLE/SETTABLE** | 整数键的数组式 table 访问编译为直接偏移加载/存储 | sieve 等基于整数 table 的 benchmark 可在 trace 内完成 | Blog #5, `ssa_codegen.go` |

### 1.5 Trace 录制与管理

| # | 优化名称 | 原理简述 | 效果 | 来源 |
|---|---------|---------|------|------|
| 23 | **FORPREP 永久黑名单** | 当 trace 因结构性指令（FORPREP/CLOSURE/CONCAT）abort 时，永久黑名单该循环入口，不再重试录制 | **项目最大单项优化**：mandelbrot 从 1.53x → 5.92x（消除了 250 万次无效录制尝试）| Blog #5, commit `ec7a7a2` |
| 24 | **Break abort 临时退避** | 因 `break`（JMP 跳出循环）导致的 abort 不永久黑名单，设置重试计数器（10 次后暂停） | 防止内循环被错误黑名单（bug 差点导致 mandelbrot 的 JIT 完全失效）| Blog #5, commit `d74d857` |
| 25 | **Sub-trace Calling（子 trace 调用）** | 外层循环 trace 遇到内层循环时，生成 `BLR` 调用内层已编译 trace 的代码，支持三层嵌套 | 解释器外层循环时间从 58% 降至 35%；但有 61 指令/调用的 prologue 开销 | Blog #6, commit `9ee431c` |
| 26 | **自递归 CALL 支持** | trace 录制器识别自递归调用，编译为原生 `BL` 跳转 + 深度计数器 | 递归函数可在 trace 内保持 | Blog #1 |

---

## 二、Method JIT 优化

Method JIT 将整个函数编译为 ARM64，适合递归密集型代码（fib, ackermann）。

| # | 优化名称 | 原理简述 | 效果 | 来源 |
|---|---------|---------|------|------|
| 27 | **函数内联 (Function Inlining)** | 编译时追踪 CALL 指令的参数来源和返回值去向；对简单函数（如 `return a + b`），将 callee body 直接内联为单条指令 | callMany: 28us → 5.1us（**5.4x**）；LuaJIT 差距从 9x 缩小到 1.7x | Blog #7, commit `0a029be` |
| 28 | **累加器寄存器锁定 (Accumulator Pinning)** | 循环累加变量（如 `x`）锁定到 ARM64 物理寄存器 X24，跨迭代无需 load/store | 与函数内联配合，`add(x,1)` 整个调用变为 `ADD X24, X24, #1` 一条指令 | Blog #7, commit `0a029be` |
| 29 | **R(0) 锁定到 X19** | 函数第一个参数 R(0) 锁定到 callee-saved 寄存器 X19，函数入口无需从内存加载 | fib(20): 28us → 24us 的关键因素之一；13529 次函数入口各省去一次 load | Blog #7, commit `051aabd` |
| 30 | **R(1) 锁定到 X22** | ackermann 的第二个参数 R(1) 锁定到 X22 | ackermann: 35us → 30us（**15% 提升**）| commit `bd7ddfd` |
| 31 | **嵌套返回跳过溢出 (Skip Spills at Nested Returns)** | 自递归函数返回时，跳过 `spillPinnedRegs`（因为调用方是同一函数，寄存器位置已知） | 消除 fib 每次返回的无用 spill/reload | Blog #7, commit `051aabd` |
| 32 | **LOADINT 常量传播** | 当 `LT R(0), R(3)` 且 R(3) 由 `LOADINT R(3), 2` 定义时，直接生成 `CMP X19, #2`（立即数比较） | fib 的 `n < 2` 基础情况从 3 条指令（store+load+compare）降至 1 条 | Blog #7, commit `051aabd` |
| 33 | **死 store 消除** | 常量传播后 `LOADINT R(3), 2` 的 store 变为无用（无其他消费者），JIT 自动消除 | 与常量传播组合，进一步减少指令 | Blog #7 |
| 34 | **内联 Call/Return** | `OP_CALL` 不再经过 `callValue()` → `call()` → `run()` 三层 Go 函数调用，直接在 `run()` 循环内 push frame + 更新 cached locals | chess benchmark ~30% 提升 | Blog #1 |
| 35 | **自递归识别与直接跳转** | Method JIT 识别 `fib` 调用 `fib`，编译为直接跳转而非完整 interpreter 中介调用 | fib 递归调用避免完整调用框架开销 | Blog #7 |

---

## 三、通用优化（解释器 + 运行时）

这些优化不依赖 JIT，直接提升解释器和运行时性能。

### 3.1 Value 表示与内存布局

| # | 优化名称 | 原理简述 | 效果 | 来源 |
|---|---------|---------|------|------|
| 36 | **紧凑 Value (56B → 32B)** | 将 `ival int64` 和 `fval float64` 合并为单个 `data uint64`（float 通过 `math.Float64bits` 存储），Value 从 56 字节缩减到 32 字节 | 所有寄存器拷贝、table 查找、函数参数传递内存带宽减半 | Blog #1 |
| 37 | **全局变量索引化** | 将 `map[string]Value` 替换为 `[]Value` 数组 + 每个 FuncProto 的 `GlobalCache`（首次访问字符串→索引，后续 O(1)） | 全局变量访问从 ~50ns 降至 ~2ns | Blog #1 |

### 3.2 Table 优化

| # | 优化名称 | 原理简述 | 效果 | 来源 |
|---|---------|---------|------|------|
| 38 | **类型化 Table Map** | 将泛型 `map[Value]Value` 拆分为专用 map：`imap map[int64]Value`（整数键）+ `skeys/svals` 扁平切片（≤12 个字符串键时线性扫描） | chess benchmark 中百万次 `piece.type` 查找：5 元素线性扫描 < Go map 哈希 | Blog #1 |
| 39 | **稀疏数组扩展** | 棋盘键 101-910 使用 array 而非 imap，整数键 <1024 直接数组索引 | sieve(1M×3): 2.502s → 0.182s（**×13.7**，解释器级别最大单项提升）| Blog #2 |
| 40 | **VM 内联字段缓存 (Inline Field Cache)** | 每条 `GETFIELD`/`SETFIELD` 字节码指令附带 `FieldCacheEntry`，记住上次查找的 skeys 索引；同构 table 间缓存命中 | nbody 提升 8.8%（所有 body table 有相同字段布局，首次填充后 O(1)）| Blog #6, commit `a783e36`, `a899eb6` |
| 41 | **懒加载 sfieldIdx** | 大 table（>12 string key）的 O(1) 字符串字段查找 | 大 table 性能提升 | commit `32dc590` |

### 3.3 算术优化

| # | 优化名称 | 原理简述 | 效果 | 来源 |
|---|---------|---------|------|------|
| 42 | **浮点算术快速路径** | 消除 float 运算的闭包开销，在解释器层面直接进行 float64 运算 | 浮点密集 benchmark 提升 | commit `b4509ee` |

### 3.4 并行优化

| # | 优化名称 | 原理简述 | 效果 | 来源 |
|---|---------|---------|------|------|
| 43 | **无锁子 VM (Lock-free Child VMs)** | `OP_GO` 生成的每个 goroutine 获得全局数组的快照副本，零互斥锁竞争 | 并行吞吐从 892K → 2.0M nodes（**×2.25**）| Blog #1 |

---

## 四、失败的优化尝试

诚实记录未能带来提升或被回退的优化。

| # | 尝试 | 原因 | 来源 |
|---|------|------|------|
| 1 | **NaN-Boxing (32B → 16B)** | 整数运算需要 47 位符号扩展，每次类型检查需要 64 位 AND+CMP；在解释器中（值在内存而非寄存器），ALU 开销超过内存带宽节省 | Blog #1 |
| 2 | **Table 对象池 (sync.Pool)** | 原子操作开销 + 旧 Value 引用导致 GC 泄露，造成 2x 性能退化 | Blog #1 |
| 3 | **扩展到 12 个浮点寄存器 (D4-D15)** | 频率分配器在平坦分布下，额外寄存器分配给同样低频的 slot，溢出几乎不变 | Blog #6 |
| 4 | **类型标签写入跳过** | 循环体内跳过 float 类型标签写入，仅在出口回写；改善仅 ~2%（expression forwarder 已消除大部分写入）| Blog #6, commit `02f4db4` |
| 5 | **While 循环 Tracing** | SSA Builder 无法处理 while 循环的任意条件和状态更新（不同于 for 循环的固定 FORPREP/FORLOOP 结构），已回退 | Blog #6, commit `abb63f1` |

---

## 五、Benchmark 里程碑数据

### 最终状态 (2026-03-19)

| Benchmark | 解释器 | JIT | 加速比 | vs LuaJIT |
|-----------|--------|-----|--------|-----------|
| **fib(20) warm** | — | 24us | ×10 (method) | **1.09x 更快 🏆** |
| **ackermann** | 0.017s | 0.017s | ×10 (method) | ~1x |
| **mandelbrot(1000)** | 1.5s | 0.227s | **×6.6** (trace) | 4.0x 慢 |
| **callMany** | — | 5.1us | ×44 (method) | 1.7x 慢 |
| sieve | 0.17s | 0.17s | ×1.0 | — |
| nbody | 2.73s | ~2.5s | ×1.1 | 7.5x 慢 |

### 关键历史节点

| 时间线 | mandelbrot 加速比 | 关键事件 |
|--------|-------------------|---------|
| 基线 | 1.0x | 纯解释器 |
| Blog #1 | 1.84x (chess) | 解释器优化 + Method JIT（但 JIT 在 chess 上反而更慢）|
| Blog #2 | ×2.27 | SSA IR + 类型特化 + 稀疏数组（sieve ×13.7）|
| Blog #3 | ×1.37 | 修复 8 个 bug 后的**真实**数字（×88 是假的）|
| Blog #4 | ×1.53 | 发现 trace JIT 使 5/7 benchmark 变慢 |
| Blog #5 | **×6.09** | FORPREP 黑名单 + 原生 GETFIELD + FSQRT |
| Blog #6 | ×6.3 | 线性扫描浮点寄存器分配 + inline field cache |
| Blog #7 | **×6.6** | 函数内联 + fib 超越 LuaJIT |

### fib(20) 优化轨迹

```
53us  →  类型播种 + 标签消除 + 紧凑帧
35us  →  Method JIT 改进
28us  →  函数内联累加器锁定（意外惠及 fib）
24us  →  R(0) 锁定 X19 + 常量传播 + 死 store 消除
LuaJIT: 26us  →  GScript 胜出 9%
```

---

## 六、与 LuaJIT 差距分析

| 差距领域 | 倍数 | 根因 | 修复方案 | 难度 |
|---------|------|------|---------|------|
| mandelbrot | 4.0x | 子 trace 调用开销（61 指令/调用 × 1M 像素）+ 内循环 26 vs 15 指令/迭代 | 代码内联（Approach C：将内层 trace ARM64 复制到外层） | 高 |
| Table 操作 | 7.5x | Value 32B vs LuaJIT TValue 8B（NaN-boxing）| NaN-Boxing 重构整个运行时 | 极高 |
| 函数调用 | 1.7x | 非简单 callee 无法完全内联 | 更通用的 trace-through-CALL 内联 | 中 |
| ~~fib~~ | ~~0.92x~~ | ~~已超越~~ | ✅ 完成 | — |

---

## 七、架构概览

```
                     ┌─────────────────────────┐
                     │    GScript Source Code   │
                     └────────────┬────────────┘
                                  │ compile
                                  ▼
                     ┌─────────────────────────┐
                     │    Bytecode (FuncProto)  │
                     └──────┬──────────┬───────┘
                            │          │
                   ┌────────▼──┐  ┌────▼────────────┐
                   │ Interpreter│  │ Hot Counter     │
                   │  (VM.run)  │  │ (loop/call)     │
                   └────────────┘  └──┬──────────┬───┘
                                      │          │
                              ┌───────▼──┐  ┌────▼──────────┐
                              │ Method   │  │ Trace         │
                              │ JIT      │  │ Recorder      │
                              │(codegen) │  │(trace.go)     │
                              └────┬─────┘  └──────┬────────┘
                                   │               │ TraceIR
                                   │        ┌──────▼────────┐
                                   │        │ SSA Builder   │
                                   │        │  (ssa.go)     │
                                   │        └──────┬────────┘
                                   │               │ SSAFunc
                                   │        ┌──────▼────────┐
                                   │        │ Optimization  │
                                   │        │  Passes:      │
                                   │        │  ConstHoist   │
                                   │        │  CSE          │
                                   │        │  DCE          │
                                   │        │  TraceOpt     │
                                   │        └──────┬────────┘
                                   │               │
                                   │        ┌──────▼────────┐
                                   │        │ Liveness +    │
                                   │        │ RegAlloc      │
                                   │        │ (linear scan) │
                                   │        └──────┬────────┘
                                   │               │
                              ┌────▼───────────────▼────┐
                              │   ARM64 Code Emitter    │
                              │   (assembler.go)        │
                              └────────────┬────────────┘
                                           │
                              ┌─────────────▼───────────┐
                              │  Executable Code Block  │
                              │  (mmap'd memory)        │
                              └─────────────────────────┘
```

---

*参考来源：docs/01~07 博客系列、CLAUDE.md、internal/jit/ 源码、git log (60+ commits)*
