# CONTEXT_GATHER Phase — Authoritative Context First

> **⚠️ LOAD-BEARING: Before any work, read `opt/harness-core-principles.md` in full.** This phase exists to enforce P3 (authoritative context first) — ANALYZE must have REAL production-pipeline evidence before reasoning. Your job is to produce that evidence.

You are a fresh, independent Opus session. You have no context from REVIEW or prior rounds. Your sole purpose: produce `opt/authoritative-context.json` — a structured snapshot of the top-N candidate targets' REAL Tier 2 pipeline output, from real benchmark source via `RunTier2Pipeline`.

**Forbidden** (P3 anti-patterns):
- Hand-constructed IR (building `Function` structs by hand in tests)
- Synthetic `TypeFloat` / `TypeInt` nodes that don't come from `RunTier2Pipeline`
- `TestDiagnose_SimpleAdd` or any test that doesn't load from `benchmarks/suite/*.gs`

**Allowed**:
- `go test -run TestProfile_<benchmark>` — existing tests in `tier2_float_profile_test.go` that call `RunTier2Pipeline` on real benchmark source
- Writing a temporary Go test file if no `TestProfile_*` exists for a target benchmark (see procedure below)
- `otool -tv /tmp/gscript_<name>_t2.bin` — disassembly of the compiled code
- Running the CLI binary against a benchmark: `go build -o /tmp/gs_cg ./cmd/gscript && /tmp/gs_cg -jit benchmarks/suite/<name>.gs`

## Your budget

- ≤30 tool calls total
- Output: `opt/authoritative-context.json` + optional temporary test files (cleaned up at end)
- No code changes to the compiler. Read-only on `internal/methodjit/*.go` except for temporary diagnostic tests in `/tmp/` or with `context_gather_*_test.go` naming (which you clean up)

## Inputs (read these, nothing else)

1. `benchmarks/data/reference.json` — frozen reference
2. `benchmarks/data/latest.json` — current measurements
3. `opt/state.json` — round counters + category_failures
4. `opt/INDEX.md` — recent round history to avoid re-targeting
5. `opt/user_priority.md` (OPTIONAL, may not exist) — user's strategic direction. If missing, that's normal and correct; rely on reference.json drift data for candidate selection. If present, its targets are ADVISORY — include them in the candidate set IF they also appear as non-trivial drifters, but do NOT prioritize them over larger real regressions.
6. The source files under `internal/methodjit/tier2_float_profile_test.go` to find existing `TestProfile_*` tests

## Procedure

### Step 1 — Candidate selection

Compute drift per benchmark from `reference.json` vs `latest.json`:

```bash
python3 - <<'PY'
import json, re
ref = json.load(open('benchmarks/data/reference.json'))
lat = json.load(open('benchmarks/data/latest.json'))
excl = set(ref['_meta']['excluded'])
def pt(s):
    m = re.search(r'[\d.]+', str(s)); return float(m.group()) if m else None
rows = []
for k, v in ref['results'].items():
    if k in excl: continue
    rj = pt(v.get('jit'))
    lj = pt(lat['results'].get(k,{}).get('jit')) if k in lat['results'] else None
    if rj and lj:
        rows.append((k, rj, lj, (lj - rj) / rj * 100))
rows.sort(key=lambda x: -x[3])
import json as J
print(J.dumps([{'benchmark': r[0], 'ref': r[1], 'now': r[2], 'drift_pct': r[3]} for r in rows], indent=2))
PY
```

**Candidate set**: pick the **top 3 by drift** (where drift > 0, i.e. regressed benchmarks). Evidence-first — the drift numbers are authoritative. If `opt/user_priority.md` exists AND names a benchmark with drift > 0 that's not already in the top 3, include it as a 4th candidate; otherwise ignore user_priority suggestions (do NOT displace a real regressor with a flat/improved target just because user_priority mentions it).

Record the candidate set and selection reasoning at the top of `opt/authoritative-context.json`.

### Step 2 — For each candidate: gather production-pipeline evidence

For benchmark X in the candidate set:

1. **Find or write a profile test that calls `RunTier2Pipeline` on real source**:
   - If `tier2_float_profile_test.go` already has `TestProfile_X` → use it. (Currently: spectral_norm, nbody, matmul, mandelbrot, math_intensive, sieve)
   - Otherwise: write a temporary test file `internal/methodjit/context_gather_X_test.go` modeled on the existing `profileTier2Func` helper:

   ```go
   //go:build darwin && arm64
   package methodjit
   import "testing"
   func TestContextGather_X(t *testing.T) {
       profileTier2Func(t, "X.gs", "<hot_function_name>", "X")
   }
   ```

   Determine `<hot_function_name>` by reading the benchmark source (`benchmarks/suite/X.gs`). Pick the innermost or highest-arity function that the benchmark's main loop spends time in.

   **NOTE**: `profileTier2Func` itself is NOT stale — it calls `RunTier2Pipeline(fn, nil)` which IS the production pipeline. The R31 confusion was that ANALYZE used its OUTPUT without understanding IR shape. This phase fixes that by structuring the output into JSON.

2. **Run the test**:
   ```bash
   go test ./internal/methodjit/ -run TestProfile_X -v -count=1 -timeout 60s 2>&1 | tee /tmp/cg_X.log
   ```
   The test logs the IR (`Print(fn)`) and writes the compiled bytes to `/tmp/gscript_X_t2.bin`.

3. **Disassemble the compiled bytes**:
   ```bash
   xcrun otool -tv /tmp/gscript_X_t2.bin 2>&1 | head -200 > /tmp/cg_X_disasm.txt
   ```

4. **Extract structured data** from IR log + disasm (use `python3` or `awk` to parse):
   - Basic block count
   - Phi node count
   - `GetField` / `SetField` count
   - `CallNative` / `CallGo` count
   - Total instruction count
   - Instruction class breakdown (compute / memory / branch / call / other) — classify ARM64 mnemonics
   - Any flagged patterns (e.g. "spill storm — >5 str to [sp, #...]" / "loop-invariant GetField inside loop")

5. **Live run the benchmark** to confirm current measured time:
   ```bash
   go build -o /tmp/gs_cg ./cmd/gscript/ 2>&1 > /dev/null && \
   /tmp/gs_cg -jit benchmarks/suite/X.gs 2>&1 | grep "Time:" | head -1
   ```

### Step 3 — Assemble `opt/authoritative-context.json`

Schema:

```json
{
  "generated_at": "<ISO-8601>",
  "reference_sha": "<from state.json.reference_baseline.sha256>",
  "latest_commit": "<git rev-parse HEAD>",
  "selection_reason": "Top N drifters plus user_priority targets",
  "candidates": [
    {
      "benchmark": "object_creation",
      "reference_jit_s": 0.764,
      "latest_jit_s": 1.053,
      "drift_pct": 37.83,
      "hot_function": "<function name>",
      "evidence": {
        "profile_test": "TestProfile_ObjectCreation" or "context_gather_object_creation_test.go (temporary)",
        "ir_log_path": "/tmp/cg_object_creation.log",
        "disasm_path": "/tmp/cg_object_creation_disasm.txt",
        "binary_path": "/tmp/gscript_object_creation_t2.bin"
      },
      "ir_summary": {
        "basic_blocks": N,
        "phi_nodes": N,
        "get_field_count": N,
        "set_field_count": N,
        "native_calls": N
      },
      "disasm_summary": {
        "total_insns": N,
        "compute_insns": N,
        "memory_insns": N,
        "branch_insns": N,
        "call_insns": N,
        "other_insns": N
      },
      "observations": [
        "One-line factual observations from IR + disasm. Example: 'GetField appears 8× inside the inner loop, none hoisted by LICM'. These must be GROUNDED in the actual IR log / disasm output, not speculation."
      ],
      "live_run_time_s": 1.053
    }
  ],
  "bisect_candidates": [
    "<commit hash>",
    "..."
  ]
}
```

**Observations section is the critical output**. Each observation must be:
- Concrete (a specific IR node, a specific disasm region)
- Cited (the line range in the log file)
- Confidence-labelled (HIGH if directly visible, MEDIUM if inferred, LOW if guessed)

Do NOT write speculation like "this probably means X" — only write what the evidence SHOWS. If you don't know, say "unknown — requires live run".

### Step 4 — Optional: bisect candidates

If drift > 10% on any candidate, identify the last 5 commits and flag them as bisect candidates:

```bash
git log --oneline --since="1 day ago" 2>&1 | head -10
```

Add the commit hashes to the `bisect_candidates` list. ANALYZE may use these to narrow the regression source.

### Step 5 — Cleanup

- Delete any temporary `context_gather_*_test.go` files you wrote (they were for this round only).
- Leave `/tmp/cg_*.log`, `/tmp/cg_*_disasm.txt`, `/tmp/gscript_*_t2.bin` for ANALYZE to read.

## Output validation (before exiting)

Before finishing, verify:
1. `opt/authoritative-context.json` exists and parses as valid JSON
2. At least 1 candidate has non-null `ir_summary` and `disasm_summary`
3. `observations` is non-empty for each candidate (at least 3 observations each)
4. No temporary test files left in `internal/methodjit/context_gather_*_test.go`

If validation fails: write `opt/context_gather_failed.md` with the failure reason. ANALYZE will read it and halt with an error instead of running on empty data.

## What you do NOT do

- You do NOT propose fixes or write a plan. That's ANALYZE's job.
- You do NOT modify benchmark source files.
- You do NOT run more than 3 candidates (token budget).
- You do NOT edit `benchmarks/data/reference.json`. It is immutable (P5).
- You do NOT use `TestProfile_*` output without structuring it into `observations`. Raw test output is NOT authoritative context — the structured JSON is.
