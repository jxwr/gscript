---
module: runtime.coroutine
description: Coroutine support — goroutine-backed stackful coroutines with channel handoff. Blocked from Tier 2 compilation.
files:
  - path: internal/runtime/coroutine.go
  - path: internal/runtime/channel.go
last_verified: 2026-04-13
---

# Runtime Coroutine

## Purpose

Stackful coroutines implemented as Go goroutines plus a channel for value handoff. `coroutine.create(f) → co` wraps a function in a suspended goroutine; `coroutine.resume(co, args...)` sends args and blocks until the coroutine yields or returns; `coroutine.yield(vals...)` sends values back and blocks until the next resume.

Goroutine / channel bytecodes (`GO`, `MAKECHAN`, `SEND`, `RECV`) are blocked from Tier 2 promotion by `canPromoteToTier2` — they require Go runtime interaction that the JIT does not model.

## Public API

- `type Coroutine struct` — internal state: goroutine ID, resume channel, yield channel, status
- `func NewCoroutine(body Value) *Coroutine`
- `func (c *Coroutine) Resume(args ...Value) ([]Value, error)`
- `func (c *Coroutine) Yield(vals ...Value) []Value`
- `func (c *Coroutine) Status() string` — `"suspended" | "running" | "normal" | "dead"`
- `type Channel struct` — make/send/recv primitives used by coroutines internally and exposed to scripts via `channel` stdlib

## Invariants

- **MUST**: coroutines are Go goroutines. They run on the Go scheduler with no cooperative scheduling of our own.
- **MUST**: a coroutine's body runs at Tier 0 (interpreter) until it calls a function that can be JIT-compiled; then that inner function may run at Tier 1 or Tier 2.
- **MUST**: `GO`/`MAKECHAN`/`SEND`/`RECV` bytecodes are rejected by `canPromoteToTier2`. The function containing them stays at Tier 1.
- **MUST**: coroutine state is per-coroutine; cross-coroutine reference sharing goes through channels or shared tables (with their own concurrency opt-in via `Table.SetConcurrent`).
- **MUST NOT**: attempt to inline a coroutine body at Tier 2. The goroutine handoff is not a modeled SSA op.

## Hot paths

- `coroutine_bench` — the only benchmark that stresses this path. Known high-variance due to goroutine scheduling latency; excluded from strict drift thresholds.

## Known gaps

- **High-variance benchmark**: `coroutine_bench` routinely swings ±10% between runs. Median-of-N doesn't fully stabilize it because the underlying distribution is bimodal (GC pause hits one run, not another).
- **No inline support**: every coroutine call pays a Go channel send/recv (~200 ns on M4). Short coroutines are disproportionately expensive.
- **No Tier 2 for bodies that reach `GO`**: the whole function stays Tier 1 even if the goroutine spawn is cold. A finer-grained "block containing GO can't be in Tier 2, but the rest can" analysis would unlock Tier 2 for mixed bodies.

## Tests

- `runtime/coroutine_test.go` — create/resume/yield/status correctness
- `benchmarks/suite/coroutine_bench.gs` — end-to-end timing (excluded from strict drift checks per `reference.json` `_meta.excluded`)
