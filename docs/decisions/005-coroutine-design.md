# 005 - Coroutine Design

## Status
Implemented (Phase 6)

## Context
GScript needs Lua-style coroutines for cooperative multitasking, generator patterns, and producer-consumer workflows. Coroutines must support create, resume, yield, status, wrap, and isyieldable operations.

## Decision
Implement coroutines using Go goroutines and channels for suspension/resumption. Each coroutine runs in a dedicated goroutine, with channel-based synchronization replacing traditional stack-switching.

## Architecture

### Coroutine Lifecycle

A coroutine progresses through these states:
- **suspended** - Created but not started, or yielded and waiting to be resumed
- **running** - Actively executing code
- **dead** - Function has returned or errored; cannot be resumed
- **normal** - Currently not executing, but another coroutine it resumed is running

### Channel Protocol

Each `Coroutine` struct holds two channels:
- `resumeCh chan []Value` (buffered, size 1): Carries arguments from the `resume` caller into the coroutine goroutine. On the first resume this delivers the initial function arguments; on subsequent resumes it delivers the values returned from `yield`.
- `yieldCh chan yieldResult` (buffered, size 1): Carries yielded values (or completion/error signals) from the coroutine goroutine back to the `resume` caller.

The protocol for each resume/yield cycle:
1. Caller sends args to `resumeCh`
2. Coroutine goroutine receives args, executes until it yields or returns
3. Coroutine goroutine sends result to `yieldCh`
4. Caller receives result from `yieldCh`

### Goroutine-Local Coroutine Tracking

A key challenge: `coroutine.yield` is registered as a GoFunction closure that captures the main interpreter instance. When called from within a coroutine goroutine, it needs to find the correct `*Coroutine` for that goroutine.

Solution: A package-level `sync.Map` (`goroutineCoMap`) maps Go goroutine IDs to their active `*Coroutine`. When a coroutine goroutine starts, it registers itself; when it finishes, it deregisters. The `yield` and `isyieldable` functions use `getCurrentCoroutine()` to look up the correct coroutine for the calling goroutine.

Goroutine IDs are extracted from `runtime.Stack()` output, which always begins with `"goroutine NNN [..."`.

### Thread Safety

Each coroutine goroutine creates its own `Interpreter` instance that shares the global `Environment` with the main interpreter but has its own `currentCo` field. The channel-based protocol ensures that only one goroutine (either the caller or the coroutine) is actively executing at any given time, avoiding concurrent access to the shared interpreter state.

The `sync.Map` used for goroutine-to-coroutine mapping is inherently thread-safe.

### Nested Coroutines

When a coroutine resumes another coroutine, the outer coroutine's goroutine blocks on the inner coroutine's `yieldCh`. Each goroutine retains its own entry in `goroutineCoMap`, so yield always finds the correct coroutine regardless of nesting depth.

## API

| Function | Description |
|---|---|
| `coroutine.create(f)` | Creates a new suspended coroutine from function f |
| `coroutine.resume(co, args...)` | Resumes co; returns (ok, values...) |
| `coroutine.yield(values...)` | Suspends the current coroutine; returns resume args |
| `coroutine.status(co)` | Returns "suspended", "running", "dead", or "normal" |
| `coroutine.wrap(f)` | Creates a coroutine and returns an iterator function |
| `coroutine.isyieldable()` | Returns true if called from within a coroutine |

## Files Changed
- `internal/runtime/coroutine.go` - Coroutine struct, status enum, goroutine-local map
- `internal/runtime/interpreter.go` - Library registration, resume/yield methods, currentCo field
- `internal/runtime/value.go` - Removed placeholder Coroutine struct (replaced by real implementation)
- `internal/runtime/coroutine_test.go` - 16 comprehensive tests

## Trade-offs

**Goroutine per coroutine**: Each coroutine consumes a Go goroutine (initial stack ~2-8KB). This is lightweight for typical use but means thousands of coroutines will consume megabytes of stack space. Go's goroutine scheduler handles this efficiently.

**Goroutine ID extraction via runtime.Stack()**: This is an unofficial API and technically an implementation detail of Go. However, it is stable across Go versions and widely used in practice (e.g., by the `grpc` package). The alternative of thread-local storage or passing context through every function call was rejected as too invasive.

**Buffered channels (size 1)**: The channels are buffered to size 1 to prevent goroutine leaks. This ensures that a send will not block indefinitely if the receiver has exited.
