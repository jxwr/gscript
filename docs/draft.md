---
layout: default
title: "The Dead Pointer"
permalink: /45-the-dead-pointer
---

# The Dead Pointer

Every `Table` in GScript carries a `shape *Shape` field. It's a pointer to a V8-style hidden class — the thing that makes field inline caches work. When you do `t.x = 1; t.y = 2; t.z = 3`, the table transitions through shapes, and the JIT caches the shape ID so subsequent accesses can skip the string lookup.

Here's the thing: the JIT doesn't use the pointer. It uses `shapeID`, a uint32. The inline cache at `tier1_table.go:124` loads the 32-bit `shapeID` at a known struct offset. The Tier 2 emitter at `emit_table_field.go:73` does the same. Nobody loads the `*Shape`. `setShape()` writes it on every field mutation, Go's garbage collector traces it on every GC cycle, and nothing ever reads it back.

That pointer costs us fifty percent.

Not entirely by itself. There's a second contributor: `ScanGCRoots` was changed in the same commit (39b5ef3) to scan the entire VM register file instead of capping at the active frame window. The JIT's self-call mechanism advances the register base without pushing Go stack frames, so the old bounded scan was missing live references — causing occasional SIGSEGV when the GC compacted a table that only existed in a deep self-call register. The fix was correct: scan everything. But `EnsureRegs` allocates at 2x capacity, so "everything" includes a tail of nil slots that the GC dutifully visits.

R35 ran the bisect. Four steps, twenty-eight lines of bash:

```
39b5ef3 — bad  (1.084s)
598bc1e — good (0.745s)
```

One commit. Two changes. Both correctness fixes. Both unrevertable.

The IR tells the rest of the story. `create_and_sum` compiles to 1181 ARM64 instructions through the production Tier 2 pipeline. `transform_chain` to 1572. `new_vec3` — the leaf that allocates a table and writes three fields — to 208. These numbers are *identical* to the pre-regression reference. Zero percent instruction drift. The codegen didn't get worse. The GC got more expensive.

`object_creation` allocates roughly 800,000 tables per run. Each table now carries one extra traced pointer. Go's `gcCompact` fires every million allocations. Each cycle scans the full register file — which might be 32K slots when you've been doing recursive self-calls in a 16K-base register window that got doubled. The two effects compound: more pointers per table × more slots per scan cycle.

The fix is two changes:

1. Delete `shape *Shape` from the Table struct. Rewrite `setShape()` to compute `shapeID` directly via `GetShapeID(skeys)` — the same underlying lookup, minus storing the result in a GC-visible pointer. The shape registry still exists. `LookupShapeByID` still works. We're just not pinning the pointer on every table.

2. Add a `regHighWater` field to the VM. Update it in `EnsureRegs`. Scan `vm.regs[:vm.regHighWater]` instead of `vm.regs[:len(vm.regs)]`. Every register write goes through `EnsureRegs`, so the high-water-mark captures the actual maximum — including self-call windows. The 2x-capacity tail stays unscanned.

Neither change touches JIT codegen. No pass changes. No ARM64 emission changes. Three source files, maybe eighty lines.

*[Implementation next...]*
