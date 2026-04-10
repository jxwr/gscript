# KB: Tier 1 CALL/RETURN overhead anatomy

**Last updated**: 2026-04-11 (R26 diagnostic)

## Measured facts

- `internal/methodjit/tier1_call.go:95` `emitBaselineNativeCall` emits the CALL sequence
- Ackermann hot self-call fast path: **~60 caller-side insns** per CALL (measured via disasm of `/tmp/gscript_ack_tier1.bin`, offsets 608→1176)
- `self_call_entry` prologue: 4 insns (`tier1_call.go:526`)
- `direct_epilogue`: 4 insns (`tier1_call.go:545`)
- Total per self-recursive call: **~68 insns**
- Full normal-path call: **~100 insns** (96-byte frame save/restore)
- Interpreter equivalent (`internal/vm/vm.go:1136` OP_CALL VMClosure): inline `continue`, no ARM64 BL, no stack frame change, no RET — stays in the Go dispatch function

## Breakdown of the 60-insn self-call fast path

| Block | Insns | Bytes (in ack.bin) | Role |
|---|---|---|---|
| NativeCallDepth pre-check (LDR/CMP/b.ge) | 3 | 608–616 | Runaway recursion guard — **removable via SP-floor prologue check** |
| Load R(A) + self-closure cmp (LDR/CMP/b.eq) | 3 | 620–628 | Required |
| Load callerProto into X1 (movz×4 + B) | 5 | 812–828 | Required (must pass proto to callee/tiering) |
| Bounds + CallCount tier2 (12 insns) | 12 | 764–808 | CallCount required for tiering; bounds check required |
| afterNormalChecks CBNZ | 1 | 832 | Flag routing (flag X20=self-call) |
| Self-call save (48-byte frame) | 6 | 892–912 | BL+RET ABI; required for recursive call |
| R(0) pin flush (2× LDR/STR) | 4 | 916–928 | Flush X22 pinned slot — required for callee arg window |
| CBNZ→setup | 1 | 932 | Flag routing |
| Advance regs + publish `ctx.Regs` + set `ctx.CallMode=1` | 4 | 984–996 | `ctx.CallMode=1` REQUIRED (`tier1_control.go:224` RETURN reads it); `ctx.Regs` STR required for slow exits in callee |
| NativeCallDepth++ (LDR/ADD/STR) | 3 | 1000–1008 | **Removable on self-call path** |
| `mov x0, ctx` | 1 | 1012 | ABI |
| CBNZ→self BL | 1 | 1016 | Flag routing |
| BL self_call_entry | 1 | 1028 | Required (real recursive branch) |
| NativeCallDepth-- | 3 | 1032–1040 | **Removable on self-call path** |
| CBNZ→restore | 1 | 1044 | Flag routing |
| Self-call restore (48-byte frame) | 6 | 1104–1124 | BL+RET ABI |
| Re-publish `ctx.Regs`/`ctx.Constants` | 2 | 1128–1132 | `ctx.Constants` STR is **dead on self-call path** (X27 never clobbered); `ctx.Regs` STR required for slow exits post-return |
| Exit-code check + load return + store to R(A) | 5 | 1136–1148 | Required |
| B done | 1 | 1176 | Required |

**Dead/removable on self-call fast path (R26 analysis)**:
- NativeCallDepth pre-check + inc + dec: **9 insns** (replaced by one-time SP-floor check in prologue)
- `ctx.Constants` STR in shared restore: **1 insn** (self-call path never clobbers X27)

**Load-bearing but expensive (deferred)**:
- `ctx.Regs` STR on setup (line 370) and re-publish on restore (line 437): required by slow-exit code that reads `ctx.Regs` to find the live register window. Could be removed via exit-lazy flush (audit ~10 exit sites). Deferred.
- `ctx.CallMode=1` STR (line 372): required by RETURN at `tier1_control.go:224` which branches on `ctx.CallMode` to choose `direct_epilogue` (returns to BL site) vs `baseline_epilogue` (exits JIT). Removing requires restructuring RETURN emission. Deferred.

## Comparison to interpreter

`internal/vm/vm.go:1136` OP_CALL VMClosure:
1. Type-assert `fnVal.Ptr().(*Closure)` (Go interface check)
2. Compute `newBase`
3. Slice-len check + amortized grow
4. N slice-element copies for args
5. Frame struct writes (6 fields)
6. `vm.frameCount++`
7. `continue` — **no BL — stays in the switch**

No ARM64 function call, no BL/RET pair, no stack frame save/restore for the interpreter. The JIT's BL+RET pair is an inherent cost of using ARM64 function calls for GScript calls. The only way to match the interpreter on pure call-overhead is to **not BL for self-recursion** — use a B (branch) back to entry, reusing the stack frame (Item 5 in the initiative).

## Why CALL is hot in these benchmarks

- ackermann: 5.15M recursive calls, tiny body (≤10 bytecodes), call-overhead dominates 90%+
- mutual_recursion: 2 protos alternating, tight base-case recursion
- method_dispatch: vcall through table, ~4 cross-function calls per iter
- binary_trees: allocation + recursive calls per tree node
- coroutine_bench: yield/resume via function-like mechanism
- object_creation: 2-3 cross-function calls per iter in `create_and_sum`/`transform_chain`

All share: call-site count / total-insn-count > ~5%, so per-call overhead is load-bearing on wall time.

## Calibration notes

- R23 lesson: M4 hides **predicted branches** (zero wall-time cost even with tens of branches/iter). This work removes mostly LDR/ADD/STR chains to the same cache line — dependent memory ops — which are NOT hidden. Expect roughly `insns × 0.3ns` at wall-time, halved for superscalar = `insns × 0.15ns`.
- R24 lesson: per-op guards have a cost floor on tight functions. `mutual_recursion +4.9%` regression came from guard overhead on small protos. Don't add per-call guards this initiative.
- R22 lesson: guard hoisting insn savings were fully hidden by M4 IPC. If R26 predicted savings don't materialize in wall time, the NEW lesson is that ctx-memory stores are also hidden — pivot to RETURN restructuring instead.

## Open questions (for future diagnostics)

1. Does the M4 store buffer absorb the 10 saved LDR/STR as well as it absorbs branches? R26 will answer empirically.
2. What fraction of ackermann's 0.563s is in `self_call_entry` prologue + `direct_epilogue` epilogue vs the body? (Unknown — need a PC-sample profile, not just insn count.)
3. Does LuaJIT actually eliminate BL for recursive calls on ARM64, or does it use something else (trampolines, guards on C stack)? Research for Item 5.
