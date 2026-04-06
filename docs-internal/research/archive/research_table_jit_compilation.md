# Table/Array Operations JIT Compilation: LuaJIT & V8 Deep Dive

Research report for GScript JIT table operation optimization.

---

## 1. LuaJIT Table IR Instructions

### 1.1 SSA IR Instruction Set for Tables

LuaJIT's trace compiler records bytecode into an SSA IR with these key table instructions:

| IR Op | Description | Operands |
|-------|-------------|----------|
| **FLOAD** | Load object field (e.g., `tab.array`, `tab.asize`, `tab.hmask`) | obj, field-id |
| **AREF** | Array element reference: computes `&array[key]` | FLOAD(tab.array), key |
| **ALOAD** | Load value from array reference | AREF |
| **ASTORE** | Store value to array reference | AREF, value |
| **HREF** | Hash part lookup (searches hash chain) | table, key |
| **HREFK** | Hash part lookup with constant key (specialized to expected slot) | FLOAD(tab.node), KSLOT(key, slot-hint) |
| **HLOAD** | Load value from hash reference | HREF/HREFK |
| **HSTORE** | Store value to hash reference | HREF/HREFK, value |
| **NEWREF** | Insert new key into hash part (assumes key doesn't exist) | table, key |
| **TBAR** | Table write barrier (for incremental GC) | table |
| **OBAR** | Object write barrier (for upvalues) | obj, value |
| **TNEW** | Allocate new table | asize, hbits |
| **TDUP** | Duplicate table template | template |

### 1.2 Typical Array Access IR Sequence

For `t[i]` where `i` is a known integer:

```
FLOAD  tab.asize    -- load array size field
ABC    key, asize   -- array bounds check (may be eliminated)
FLOAD  tab.array    -- load array base pointer
AREF   array, key   -- compute element address: array + key * sizeof(TValue)
ALOAD  aref         -- load the TValue at that address
```

Key insight: AREF and HREFK reference an FLOAD of the array/hash part as their left operand. HREF and NEWREF directly reference the table pointer because they need to search or extend the table.

### 1.3 Array Bounds Check (ABC)

LuaJIT implements Array Bounds Check Elimination (ABC) as a dedicated optimization pass (`-Oabc`, enabled by default).

**How it works:**

1. During recording, `rec_idx_abc()` in `lj_record.c` emits an `ABC` IR instruction that guards `key < tab.asize`.
2. The ABC optimization pass (`lj_opt_fold.c`) analyzes loop induction variables using scalar evolution analysis.
3. For a loop like `for i = 1, n do t[i] = ... end`, the optimizer proves that:
   - `i` starts at 1 (>= 0, so lower bound OK)
   - `i` is bounded by `n`, and `n <= tab.asize` (checked once at loop entry)
4. The per-element bounds check inside the loop body is hoisted to a single guard at the loop header.

**Result:** In tight loops, array access compiles to a raw pointer dereference with zero per-element bounds checking -- only a single guard at the loop entry.

The optimization gives roughly **~10% speedup** on array-heavy benchmarks according to LuaJIT's benchmarks.

### 1.4 Table Write Barrier (TBAR)

LuaJIT uses a tri-color incremental mark-and-sweep GC. The write barrier preserves the invariant: **a black (fully traversed) object must never hold a reference to a white (unvisited) object**.

**Table barrier strategy (backward barrier):**

Tables use a "backward barrier" -- when a store into a black table is detected, the table is pushed back to gray (marked for re-traversal). This is different from forward barriers used for other objects.

**LuaJIT 2.x optimization:**
- The barrier only checks the table's color (is it black?), **ignoring the color of the stored value**.
- This means the barrier check is just 2-3 machine instructions:
  ```asm
  ldrb  w0, [table, #color_offset]   ; load table's GC color byte
  tst   w0, #BLACK_BIT                ; test if table is black
  b.ne  barrier_slow_path             ; rarely taken
  ```
- The JIT compiler can **eliminate most write barriers entirely** through analysis:
  - If the table was recently allocated (known white), no barrier needed.
  - If no GC can run between allocation and store, no barrier needed.
  - Allocation sinking can eliminate both the allocation AND the barrier.

### 1.5 NEWREF (Hash Table Insertion)

NEWREF is emitted when the trace recorder sees a store to a key that doesn't exist in the table yet. It:

1. Assumes the key does NOT exist (recorded observation).
2. Calls `lj_tab_newkey()` which:
   - Finds the main position for the hash key.
   - If occupied, uses Brent's variation of open addressing to chain.
   - May trigger table resize if load factor is exceeded.
3. Returns a pointer to the new TValue slot.
4. NEWREF already handles the key barrier (since it creates a new reference from table to key).

NEWREF requires 3 GPR arguments for `lj_tab_newkey()`, making it an expensive call-out from JIT code. This is acceptable because new key insertion is inherently less frequent than reads/updates.

---

## 2. LuaJIT Table Internal Structure

### 2.1 GCtab Memory Layout

```c
typedef struct GCtab {
  GCHeader;          // GC header with mark bits and type tag
  uint8_t nomm;      // Negative cache for fast metamethods (bitfield)
  int8_t colo;       // Array colocation hint (-128..+127)
  MRef array;        // Pointer to array part (TValue[])
  GCRef gclist;      // GC list link
  GCRef metatable;   // Metatable pointer (or NULL)
  MRef node;         // Pointer to hash part (Node[])
  uint32_t asize;    // Size of array part (keys 0..asize-1, 1-indexed: 1..asize)
  uint32_t hmask;    // Hash part mask = hash_size - 1 (always power of 2)
} GCtab;
```

**Total memory:**
```
sizeof(GCtab) + sizeof(TValue) * asize + sizeof(Node) * (hmask + 1)
```

### 2.2 Array Part vs Hash Part

**Array part:**
- Contiguous `TValue[]` array indexed by integer keys.
- Lua tables are 1-indexed, but internally the array is 0-indexed with `array[0]` unused (or used for key 0).
- `asize` field stores the size. Valid array keys: `1 <= key < asize`.
- Resizing heuristic: the actual size is the largest `n` such that **more than half the slots between 1 and n are in use**. This avoids wasting memory on sparse arrays.

**Hash part:**
- Array of `Node` structures, size always a power of 2 (represented by `hmask = size - 1`).
- Each `Node` has: `{TValue val, TValue key, MRef next}`.
- Collision resolution: hybrid of open addressing and separate chaining with **Brent's variation** -- when inserting, if the colliding element is NOT in its main position, it gets moved to make room. This maintains the invariant: if an element is not in its main position, the element occupying its main position IS in its own main position.

**Key is in array range if:**
```c
key >= 1 && key < tab->asize && tab->array[key] is not nil
```
For integer keys, the runtime always checks the array part first, then falls back to the hash part.

### 2.3 TValue: NaN-Boxing (8 bytes)

LuaJIT uses NaN-boxing to encode all Lua values in exactly **8 bytes** (one `uint64_t`):

```
Numbers (double):    standard IEEE 754 double, using all 64 bits
                     Valid doubles have at most 1 sign bit + 11 exponent bits set

Non-numbers:         The top 13 bits are all 1s (NaN pattern)
                     Bits [50:47] = itype (4 bits, type tag)
                     Bits [46:0]  = payload (47 bits, enough for 48-bit pointers)

Layout:
  [63]     [62:52]      [51:48]    [47:0]
  sign     exponent+NaN  itype      payload (pointer/int32/etc)
```

**itype values (when top 13 bits = 0xFFF8+):**
- `0xFFF8` = nil
- `0xFFF9` = false
- `0xFFFA` = true
- `0xFFFB` = lightuserdata
- `0xFFFC` = string (pointer in lower 47 bits)
- `0xFFFD` = upvalue
- `0xFFFE` = thread
- `0xFFFF` = proto/function/table/userdata/cdata (pointer)

**Why NaN-boxing is critical for performance:**
- Every TValue is exactly 8 bytes = 1 machine register.
- Array elements are 8 bytes apart: `&array[i] = base + i * 8 = base + (i << 3)`.
- L1 cache holds 2x more values vs 16-byte tagged unions, 4x more vs GScript's current 24-byte Values.
- No multi-word loads/stores -- single LDR/STR per element.
- Function arguments passed in registers without boxing overhead.

---

## 3. V8 Array Access Compilation

### 3.1 Elements Kinds

V8 classifies arrays by their "elements kind" -- 21 different kinds, organized in a lattice:

```
PACKED_SMI_ELEMENTS           (small integers, no holes)
    |
PACKED_DOUBLE_ELEMENTS        (doubles, no holes)
    |
PACKED_ELEMENTS               (any JS value, no holes)
    |
HOLEY_SMI_ELEMENTS            (small integers, may have holes)
    |
HOLEY_DOUBLE_ELEMENTS         (doubles, may have holes)
    |
HOLEY_ELEMENTS                (any JS value, may have holes)
```

**Key properties:**
- Transitions are **irreversible** and go only downward (more general).
- Once an array gets a double, it's forever DOUBLE_ELEMENTS even if the double is later removed.
- Once a hole is created, it's forever HOLEY even if filled later.
- More specific kinds enable more aggressive optimizations (e.g., PACKED_SMI avoids float-to-int checks and hole checks).

### 3.2 Inline Caches (ICs)

V8 uses inline caches to specialize array access at each call site:

1. **Monomorphic IC:** The array access site has only seen one elements kind (e.g., PACKED_SMI). Generated code:
   ```
   check map == expected_map
   load elements pointer
   bounds check: index < length
   load element (raw SMI, no type check needed)
   ```

2. **Polymorphic IC:** The site has seen 2-4 different maps. Generated code has a chain of map checks.

3. **Megamorphic IC:** Too many maps seen. Falls back to generic lookup.

### 3.3 Bounds Check Elimination in TurboFan

TurboFan (V8's top-tier optimizer) uses **numerical range analysis** to eliminate bounds checks:

1. Each `CheckBounds` node carries range information: `[min_value, max_value]`.
2. During the "simplified lowering" phase, `VisitCheckBounds` compares:
   - Is `index.min >= 0`? (lower bound safe)
   - Is `index.max < array.length`? (upper bound safe)
3. If both hold, the `CheckBounds` node is replaced with a no-op (`DeferReplacement`).

**Loop-specific optimization:**
- For `for (let i = 0; i < arr.length; i++)`, TurboFan proves `0 <= i < arr.length` from the loop structure.
- The per-iteration bounds check is eliminated entirely.

**Security note:** V8 historically had bounds check elimination bugs that were exploitable for RCE. They eventually reduced the aggressiveness of this optimization for security reasons. For a scripting language without security concerns, more aggressive elimination is safe.

### 3.4 Hidden Classes (Maps) and Shape Transitions

V8 tracks object "shapes" (hidden classes / maps):
- Each object has a pointer to its Map (shape descriptor).
- The Map describes: property names, their offsets in the object, and the elements kind.
- Adding a property creates a **map transition** to a new Map with the added property.
- `TransitionArray` stores edges from one Map to sibling Maps.

For property access in JIT code:
```
check object.map == expected_map    ; 1 comparison
load object[known_offset]           ; direct memory access at known offset
```
This turns `obj.x` from a hash lookup into a **single load at a compile-time-known offset**.

### 3.5 Write Barriers in V8

V8 uses a generational garbage collector (young generation + old generation):

- **Card table:** Heap divided into fixed-size "cards" (~256-512 bytes). A bitmap tracks which cards contain cross-generation pointers.
- **Write barrier:** After every pointer store, check if old->young reference was created:
  ```asm
  str   value, [object, #offset]     ; the actual store
  ; Write barrier:
  lsr   x0, object, #CARD_SHIFT      ; compute card index
  strb  wzr, [card_table, x0]        ; mark card as dirty (1 byte store)
  ```
- **JIT optimization:** If the compiler can prove an object is in the young generation (recently allocated), write barriers are omitted entirely. Stack-allocated objects (via escape analysis) also skip barriers.

---

## 4. Key Performance Tricks

### 4.1 Avoiding Per-Element Bounds Checks

**Approach 1: Loop-invariant bounds check hoisting (LuaJIT ABC)**
- At the loop header, emit a single guard: `array.len >= loop_limit`.
- Inside the loop body, no bounds checks needed.
- Requires: knowing the loop's induction variable range.

**Approach 2: Range analysis (V8 TurboFan)**
- Track the numeric range `[min, max]` of every SSA value.
- If `index.min >= 0 && index.max < array.len`, eliminate the check.
- More general than loop-based approach -- works for non-loop array access too.

**Approach 3: Speculative elimination with side-exit**
- Record the observed array length at trace time.
- Guard once: `array.len >= observed_len` (side-exit if resized).
- All array accesses with index < observed_len skip bounds checks.
- This is the simplest to implement in a tracing JIT.

**Recommendation for GScript:**
Start with Approach 3 (cheapest to implement). Then add Approach 1 for `for i = 1, #t` patterns where the loop variable is bounded by table length.

### 4.2 Minimizing GC Write Barrier Overhead

**Current state:** GScript uses Go's GC, so write barriers are handled by Go's runtime. But when JIT code writes directly to `[]Value` memory via raw pointers, Go's write barrier is bypassed.

**Strategies:**

1. **Exploit the Go GC model:** Go uses a concurrent tri-color GC with write barriers on pointer fields. Since GScript Values contain `unsafe.Pointer`, stores via raw pointer arithmetic bypass Go's write barrier. This is currently safe because:
   - The `[]Value` slice is reachable from the Table, which is reachable from the VM.
   - As long as the stored value is also reachable from somewhere Go can see (the VM registers), the GC won't collect it.
   - BUT: if JIT code creates the only reference to an object and stores it only via raw pointer, Go's GC might miss it.

2. **Use `runtime.KeepAlive()` or store barriers:** For stores of pointer-containing values (table, function, string), ensure Go's write barrier is triggered. This can be done by calling a small Go helper function for pointer-type stores, while integer and float stores (no pointers) skip the barrier entirely.

3. **Batch barriers:** If multiple stores happen in sequence (e.g., SETLIST), trigger the barrier once after all stores rather than per-store.

4. **Allocation sinking (LuaJIT technique):** For short-lived tables (allocated and used within a trace), sink the allocation to side exits. The table never materializes on the fast path -- all reads/writes are forwarded from SSA values. LuaJIT reports **400x speedup** for point-class examples with this optimization.

### 4.3 Table Pre-allocation Optimization

**LuaJIT:**
- `table.new(narray, nhash)` pre-allocates both array and hash parts.
- Hash size is always `2^hbits` where `hbits` = number of bits needed.
- The bytecode compiler analyzes table constructors `{1,2,3,x=4,y=5}` and emits `TNEW` with correct `asize` and `hbits` hints.
- TDUP (table template duplication) is used for constant tables -- the template is created once, and each use copies it, preserving the pre-allocated shape.

**GScript (current):**
- `NewTableSized(arrayHint, hashHint)` exists and is used by `OP_NEWTABLE`.
- The compiler already extracts array/hash hints from table constructors.
- Further opportunity: the JIT could specialize `NEWTABLE` to pre-allocate based on observed runtime sizes.

---

## 5. Concrete Implementation Recommendations for GScript

### 5.1 Priority 1: Array Bounds Check Hoisting (Medium effort, high impact)

Currently, every `SSA_LOAD_ARRAY` and `SSA_STORE_ARRAY` emits a full bounds check (4 instructions: CMPimm, BCond, LDR len, CMPreg, BCond). For loops like sieve and nbody, this is 4-8 instructions per iteration wasted.

**Implementation plan:**
1. Add an `SSA_GUARD_ARRAY_BOUNDS` instruction that checks `key < array.len` once.
2. In the SSA builder, when recording a FORLOOP whose body contains GETTABLE/SETTABLE with the loop variable as key:
   - Emit `SSA_GUARD_ARRAY_BOUNDS(table, loop_limit)` at the loop header.
   - Mark subsequent LOAD_ARRAY/STORE_ARRAY in the loop body as "bounds-checked" (skip the per-element check).
3. In codegen, bounds-checked LOAD_ARRAY becomes:
   ```asm
   ; At loop header (once):
   ldr   x3, [table, #array_offset+8]    ; load array.len
   cmp   x_loop_limit, x3
   b.ge  side_exit                        ; exit if limit >= len

   ; In loop body (per iteration):
   ldr   x3, [table, #array_offset]       ; load array.ptr
   ; compute element: x3 + key * ValueSize
   ; load/store -- NO bounds check
   ```

**Expected impact:** ~10% on array-heavy benchmarks (sieve, matmul).

### 5.2 Priority 2: Reduce ValueSize from 24B to 16B or 8B (High effort, very high impact)

The single biggest performance gap vs LuaJIT is Value size: GScript uses 24 bytes per Value vs LuaJIT's 8 bytes (NaN-boxing). This affects:
- Array element stride: `key * 24` requires MUL vs `key << 3` (single LSL)
- Cache efficiency: 3x fewer Values per cache line
- Memory bandwidth: 3x more data moved for table copies

**Path to 8-byte NaN-boxing:**

Phase A: Shrink to 16 bytes (tagged pointer union):
```go
type Value struct {
    tag  uint64  // type tag + small payload (int/float/bool)
    ptr  unsafe.Pointer  // pointer for ref types (string/table/func)
}
```
- Doubles: store float64 bits in `tag`, `ptr` = nil.
- Integers: store int64 in `tag` (with type tag in high bits), `ptr` = nil.
- Reference types: `tag` = type tag, `ptr` = object pointer.
- Element stride becomes `key << 4` (single LSL).

Phase B: Full NaN-boxing (8 bytes):
```go
type Value uint64
```
- Doubles: raw IEEE 754 bits.
- Others: NaN-tagged with type in bits [50:47], pointer/payload in bits [46:0].
- Element stride becomes `key << 3` (single LSL).
- BUT: requires 47-bit pointers (fine on current x86-64 and ARM64).
- Requires significant refactoring of entire codebase.

**Recommendation:** Do Phase A first (16B). It's a significant win with lower risk. Phase B (8B NaN-boxing) is "Season 2" material.

### 5.3 Priority 3: Type-Specialized Array Operations (Medium effort, high impact)

Currently, `SSA_LOAD_ARRAY` with `SSATypeInt` already loads only the data field (8 bytes instead of 24). Extend this to `SSA_STORE_ARRAY`:

**Type-specialized STORE_ARRAY for integers:**
```asm
; Current: copies all 24 bytes (3 LDR + 3 STR)
; Optimized: writes only type byte + data field (1 STRB + 1 STR)
mov   w0, #TypeInt
strb  w0, [element_addr, #OffsetTyp]    ; write type byte
str   x_value, [element_addr, #OffsetData]  ; write int64 data
; Skip OffsetPtr (not needed for int/float)
```

This halves the number of memory operations for int/float array stores.

### 5.4 Priority 4: HREFK-style Field Access with Shape Guards (Medium effort)

Currently, LOAD_FIELD uses a field index captured at recording time and guards that `skeys.len > fieldIdx`. This doesn't guard that the key at `skeys[fieldIdx]` is still the expected key (shape could change).

**LuaJIT-style approach:**
1. Record a "shape ID" (hash of skeys list) at trace recording time.
2. Guard the shape ID at the loop header (once per trace execution).
3. If shape matches, all field accesses in the trace are guaranteed correct -- no per-access guards needed.

**V8-style approach (hidden class):**
1. Add a `shapeVersion uint32` field to Table, incremented on any structural change (add/remove key).
2. Record the shapeVersion at trace time.
3. Guard `table.shapeVersion == expected` once at trace entry.
4. All subsequent field accesses use compile-time-constant offsets without any guards.

**Recommendation:** The V8-style `shapeVersion` is simpler and cheaper. A single uint32 comparison replaces multiple per-field guards.

### 5.5 Priority 5: Allocation Sinking for Short-Lived Tables (High effort, very high impact long-term)

LuaJIT's allocation sinking optimization eliminates tables that don't escape a trace. For patterns like:
```lua
local p = {x = a + b, y = c + d}  -- TNEW + stores
return p.x * p.y                    -- loads + arithmetic
```

The optimizer:
1. Marks the TNEW as "sinkable" (no escape to a call or side-exit that needs it).
2. Forwards stores through loads (STORE_FIELD x → LOAD_FIELD x = the stored value).
3. The table is never allocated on the fast path.
4. Only at side exits is the table materialized from snapshotted SSA values.

**Result:** 400x faster for point-class patterns (LuaJIT benchmark data).

This is architecturally complex but is the single highest-impact optimization for table-heavy code like nbody (which creates temporary vector objects).

---

## 6. Summary: Optimization Priority Matrix

| Priority | Optimization | Effort | Impact | Benchmarks Affected |
|----------|-------------|--------|--------|-------------------|
| P1 | Array bounds check hoisting | Medium | ~10% | sieve, matmul, sort |
| P2 | Value 24B -> 16B | High | ~30-50% | ALL table ops |
| P3 | Type-specialized STORE_ARRAY | Low | ~15% on stores | sieve, matmul |
| P4 | Shape version guard (replace per-field guards) | Medium | ~20% on field access | nbody, method_dispatch |
| P5 | Allocation sinking | Very High | ~100-400% for patterns | nbody, point-class patterns |
| P6 | Value 16B -> 8B (NaN-boxing) | Extreme | ~2-3x | Everything |

**Recommended order:** P3 (quick win) -> P1 (moderate win) -> P4 (field access win) -> P2 (big restructure) -> P5 (long-term) -> P6 (Season 2).

---

## Sources

- [LuaJIT 2.0 SSA IR](http://wiki.luajit.org/SSA-IR-2.0)
- [LuaJIT SSA IR (Tarantool wiki)](https://github.com/tarantool/tarantool/wiki/LuaJIT-SSA-IR)
- [LuaJIT Allocation Sinking Optimization](http://wiki.luajit.org/Allocation-Sinking-Optimization)
- [LuaJIT New Garbage Collector](http://wiki.luajit.org/New-Garbage-Collector)
- [LuaJIT Optimizations](https://github.com/tarantool/tarantool/wiki/LuaJIT-Optimizations)
- [The Anatomy of LuaJIT Tables (Percona)](https://percona.community/blog/2020/04/29/the-anatomy-of-luajit-tables-and-whats-special-about-them/)
- [LuaJIT Source: lj_tab.c](https://github.com/LuaJIT/LuaJIT/blob/v2.1/src/lj_tab.c)
- [LuaJIT Source: lj_obj.h](https://github.com/LuaJIT/LuaJIT/blob/v2.1/src/lj_obj.h)
- [LuaJIT Source: lj_record.c](https://github.com/LuaJIT/LuaJIT/blob/v2.1/src/lj_record.c)
- [LuaJIT Source: lj_opt_fold.c](https://github.com/LuaJIT/LuaJIT/blob/v2.1/src/lj_opt_fold.c)
- [LuaJIT Source Code Analysis: Data Type (Medium)](https://medium.com/@eclipseflowernju/luajit-source-code-analysis-part-2-data-type-59b501d59e7f)
- [Elements Kinds in V8](https://v8.dev/blog/elements-kinds)
- [JavaScript Engine Fundamentals: Shapes and Inline Caches (Mathias Bynens)](https://mathiasbynens.be/notes/shapes-ics)
- [Maps (Hidden Classes) in V8](https://v8.dev/docs/hidden-classes)
- [Maglev - V8's Fastest Optimizing JIT](https://v8.dev/blog/maglev)
- [Exploiting TurboFan Through Bounds Check Elimination](https://gts3.org/2019/turbofan-BCE-exploit.html)
- [V8 Garbage Collection Tour (jayconrod.com)](https://jayconrod.com/posts/55/a-tour-of-v8-garbage-collection)
- [Trash Talk: The Orinoco Garbage Collector (V8 Blog)](https://v8.dev/blog/trash-talk)
- [LuaJIT Internals: Fighting the JIT Compiler](https://pwner.gg/blog/2022-09-13-lua-jit-part2)
- [What I Learned from LuaJIT (mrale.ph)](https://mrale.ph/talks/vmss16/)
- [How JIT Compilers are Implemented and Fast (kipply)](https://carolchen.me/blog/jits-impls/)
- [LuaJIT Hacking: Getting next() out of the NYI list (Cloudflare)](https://blog.cloudflare.com/luajit-hacking-getting-next-out-of-the-nyi-list/)
- [LuaJIT/LuaJIT DeepWiki](https://deepwiki.com/LuaJIT/LuaJIT)
