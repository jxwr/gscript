# V8 Speculative Optimization and Deoptimization Research

## Executive Summary

This document analyzes V8's speculative optimization and deoptimization mechanisms, with a focus on applicability to GScript's trace JIT. Key findings:

1. **V8's speculation is feedback-driven**: Ignition interpreter collects type feedback, TurboFan makes assumptions, guards validate at runtime
2. **Deoptimization is expensive**: V8 has moved from Sea of Nodes to CFG (Turboshaft) to reduce complexity and compile time
3. **OSR is critical for real-world performance**: On-Stack Replacement enables optimization of hot loops mid-execution
4. **Frame compatibility matters**: Sparkplug's success comes from matching interpreter frame layouts

## 1. Speculative Optimization

### 1.1 V8's Speculation Model

**Assumptions TurboFan Makes:**

1. **Type Stability**: Variables maintain consistent types across calls
2. **Hidden Class Stability**: Object shapes (maps) don't change
3. **Array Homogeneity**: Arrays contain elements of the same type
4. **Property Existence**: Properties exist at expected offsets
5. **No Prototype Pollution**: Prototypes don't gain new properties unexpectedly

### 1.2 Type Speculation

V8's type speculation follows this pipeline:

```
JavaScript Source
    ↓
Ignition Interpreter (executes + collects feedback)
    ↓
Feedback Vector (type profiles)
    ↓
TurboFan Speculation (makes assumptions)
    ↓
Optimized Machine Code
```

**Feedback Types:**
- `None`: No information yet
- `SmallInteger`: 31-bit signed integers
- `String`: JavaScript strings
- `Number`: Double-precision floats
- `Boolean`: True/false values
- `Object`: Reference to object with specific map
- `Function`: Reference to callable function

**Speculation Examples:**

```javascript
// TurboFan speculates: x is always a SmallInteger
function add(x, y) {
  return x + y;  // Emits CheckedInt32Add
}

// TurboFan speculates: obj has map {prop: offset 8}
function getProp(obj) {
  return obj.prop;  // Emits CheckMaps + LoadField
}

// TurboFan speculates: arr is a SMICheckedArray (small integers)
function sum(arr) {
  let total = 0;
  for (let i = 0; i < arr.length; i++) {
    total += arr[i];  // Emits LoadElement with bounds check
  }
  return total;
}
```

### 1.3 Bounds Check Elimination

V8 eliminates bounds checks through:
1. **Loop Analysis**: Proving `i < arr.length` always holds
2. **Range Analysis**: Proving indices are within valid range
3. **Hoisting**: Moving checks outside loops when provable

**Example:**
```javascript
// Bounds check elided: i always < 10
function sumTen() {
  let arr = new Array(10);
  let total = 0;
  for (let i = 0; i < 10; i++) {
    total += arr[i];  // No bounds check
  }
  return total;
}
```

### 1.4 Guard Insertion

V8 inserts guards at speculation points:

| Guard Type | Purpose | Example Check |
|------------|---------|----------------|
| CheckMaps | Object shape hasn't changed | Compare map pointer |
| CheckSmi | Value is small integer | Check bit pattern |
| CheckString | Value is a string | Check object type tag |
| CheckBounds | Index is within array bounds | Compare to length |
| CheckNotHole | Array slot is not empty | Check element type tag |

**Guard Placement Strategy:**
1. **Early guards**: Check inputs immediately
2. **Hoisted guards**: Move common checks out of loops
3. **Specialized guards**: Use monomorphic feedback for fast checks
4. **Guard fusion**: Combine related checks

### 1.5 V8's Recent Changes (Turboshaft)

V8 is migrating from Sea of Nodes to CFG-based Turboshaft because:

1. **Too many nodes on effect chain**: JavaScript operations have many side effects
2. **Effect mirrors control chain**: Most effectful operations end up constrained like CFG
3. **Compile time is 2x slower**: SoN requires complex scheduling
4. **Hard to reason about**: Engineers struggle debugging SoN graphs

**Key Quote from V8 Blog (2025-03-25):**
> "In practice, the control nodes and control chain always mirror the structure of the equivalent CFG... SoN is just CFG where pure nodes float."

## 2. Deoptimization

### 2.1 When Deoptimization Happens

**Common Triggers:**

1. **Type Violation**: Variable receives unexpected type
   ```javascript
   function foo(x) {
     return x + 1;  // Speculated x is integer
   }
   foo(42);    // OK
   foo("hi");  // DEOPT!
   ```

2. **Hidden Class Change**: Object shape changes
   ```javascript
   function get(obj) {
     return obj.x;  // Speculated {x} map
   }
   let o = {x: 1};
   get(o);      // OK
   o.y = 2;     // Map changes to {x, y}
   get(o);      // DEOPT!
   ```

3. **Array Type Change**: Array element type changes
   ```javascript
   function sum(arr) {
     return arr[0] + arr[1];  // Speculated SMI array
   }
   sum([1, 2]);      // OK
   sum([1, "hello"]); // DEOPT!
   ```

4. **Prototype Modification**: Adding properties to prototype
   ```javascript
   Object.prototype.newProp = 123;  // DEOPTS all dependent code
   ```

### 2.2 Eager vs Lazy Deoptimization

**Eager Deoptimization:**
- Immediate bailout to interpreter
- Used when safety-critical assumption violated
- Example: Type mismatch in hot path

```javascript
// Eager deopt example
function hotFunction(x) {
  // TurboFan emitted: CheckInt32(x)
  // If x is string, immediate bailout
  return x + 1;
}
```

**Lazy Deoptimization:**
- Mark function for deoptimization
- Continue in optimized code
- Actually deoptimize on next function entry or at safepoint
- Used when speculation is less critical

```javascript
// Lazy deopt example
function withTryCatch(x) {
  try {
    return x + 1;  // If overflows, mark lazy deopt
  } catch (e) {
    return "error";
  }
}
// Next call enters interpreter
```

### 2.3 Deoptimization State Reconstruction

V8 stores deoptimization metadata to reconstruct interpreter state:

**Deoptimization Input Data:**
1. **Frame state mapping**: Which optimized values map to which interpreter registers
2. **Bailout ID**: Where in the bytecode to resume
3. **Materialization instructions**: How to reconstruct object structures
4. **Closure context**: Captured variables for nested functions

**Reconstruction Process:**
```
Optimized Frame Crash
    ↓
Deoptimization Handler (reads metadata)
    ↓
Materialize Objects (rebuild structures)
    ↓
Fill Interpreter Registers (map values)
    ↓
Set Bytecode Offset (resume point)
    ↓
Jump to Interpreter (continue execution)
```

### 2.4 Deoptimization Loops (V8's Past Problem)

V8's Crankshaft compiler suffered from optimization-deoptimization loops:

```javascript
// Problematic pattern for Crankshaft
function foo(x) {
  if (x.type === 'A') {
    return x.a;  // Optimizes assuming {type: 'A', a: 1}
  } else {
    return x.b;  // But then sees {type: 'B', b: 2}
  }
}

foo({type: 'A', a: 1});  // Optimizes to {type: 'A', a: 1}
foo({type: 'B', b: 2});  // Deoptimizes
foo({type: 'A', a: 1});  // Re-optimizes to {type: 'A', a: 1}
// Endless cycle!
```

**V8's Solution:**
- Track deoptimization history
- Disable optimization for "bouncy" functions
- Use polymorphic ICs instead of deoptimizing

## 3. On-Stack Replacement (OSR)

### 3.1 What is OSR?

OSR allows changing code while it's executing on the stack. V8 uses OSR for:
1. **Tier-up**: Interpreter → Sparkplug → TurboFan for hot loops
2. **Tier-down**: Optimized → Interpreter on deoptimization

### 3.2 How V8's OSR Works

**Trigger Conditions:**
1. Loop executes enough times (default: ~1000 iterations)
2. Function is already executing when optimization completes

**OSR Entry Points:**
- V8 generates special entry points at loop headers
- State mapping from optimized values to interpreter frame
- No full stack walk needed (only current frame)

**Frame Migration:**
```
Interpreter Frame:
[registers r0..rn, bytecode_offset, feedback_vector]

Sparkplug Frame (same layout!):
[registers r0..rn, address_mapping, feedback_vector]

Optimized Frame (different layout):
[optimized locals, spilled values, safepoints]
```

### 3.3 Sparkplug's OSR Innovation

Sparkplug maintains "interpreter-compatible frames":
- Same register layout as interpreter
- Same stack frame structure
- Simple address → bytecode offset mapping
- Near-zero OSR cost

**Why This Matters:**
- No frame reconstruction needed
- OSR is just a jump + register load
- Enables aggressive tier-up

### 3.4 OSR Implementation Challenges

1. **Live Variable Analysis**: Which values need to be preserved?
2. **Register Mapping**: Map optimized registers to interpreter registers
3. **State Materialization**: Rebuild heap objects from optimized values
4. **Safepoint Placement**: Where is OSR legal?

## 4. Applicability to GScript Trace JIT

### 4.1 Current GScript JIT Status

GScript has:
- Trace JIT with SSA IR
- ARM64 backend
- 3-11x speedup (vs interpreter)
- No deoptimization framework
- No speculation (all operations are safe)

### 4.2 What GScript Should Adopt

**Low-Hanging Fruit (Phase 1):**
1. **Type Speculation for Integers**: Most GScript code is integer-heavy
2. **Array Bounds Check Elimination**: Already partially implemented
3. **Simple Guards**: Integer checks, null checks

**Medium Effort (Phase 2):**
1. **Table Shape System**: Analog to V8's hidden classes
2. **Function Shape Speculation**: Assume consistent argument types
3. **Eager Deoptimization**: Simple bailout mechanism

**High Effort (Phase 3):**
1. **Lazy Deoptimization**: More complex, less common cases
2. **OSR for Hot Loops**: Enable optimization mid-execution
3. **Polymorphic Inline Caching**: Handle multiple types gracefully

### 4.3 What GScript Should Avoid (Initially)

1. **Sea of Nodes**: V8 is abandoning it for CFG
   - Too complex for trace JIT
   - GScript's SSA IR is already CFG-like

2. **Complex Effect Tracking**: V8's effect chain is error-prone
   - Keep side effects explicit in SSA
   - Don't add implicit effect edges

3. **Heavy Guard Fusion**: Too complex for initial implementation
   - Start with simple, explicit guards
   - Consider fusion after framework is stable

## 5. Recommended Deoptimization Framework Design

### 5.1 Core Components

```
┌─────────────────────────────────────────────────────────────┐
│                     Deoptimization Framework                  │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌──────────────────┐         ┌─────────────────────────┐  │
│  │  Type Feedback   │───────▶│  Speculation Engine     │  │
│  │  Collector       │         │  (makes assumptions)    │  │
│  └──────────────────┘         └───────────┬─────────────┘  │
│                                      │                       │
│                                      ▼                       │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  Guard Generator                                     │   │
│  │  - CheckInt32                                        │   │
│  │  - CheckFloat64                                      │   │
│  │  - CheckBounds                                       │   │
│  │  - CheckTableShape                                   │   │
│  └──────────────────────┬──────────────────────────────┘   │
│                         │                                     │
│                         ▼                                     │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  Deoptimization Metadata Builder                     │   │
│  │  - Frame state mapping                              │   │
│  │  - Bailout ID generation                            │   │
│  │  - Value materialization info                      │   │
│  └──────────────┬──────────────────────────────────────┘   │
│                 │                                            │
│                 ▼                                            │
│  ┌─────────────────────────────────────────────────────┐   │
│  │  Deoptimization Runtime Handler                     │   │
│  │  - Materialize objects                              │   │
│  │  - Fill interpreter registers                        │   │
│  │  - Set bytecode offset                              │   │
│  │  - Jump to interpreter                              │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

### 5.2 Data Structures

```go
// Type feedback collected during interpretation
type TypeFeedback struct {
    ObservedTypes   map[int]ValueType  // slot → types seen
    CallCount       int
    LastUpdated     uint64
}

// Speculation metadata
type Speculation struct {
    Guard      *GuardNode
    BailoutID  int
    DependsOn  []ValueID  // values needed for deopt
}

// Deoptimization metadata per compiled trace
type DeoptMetadata struct {
    BailoutPoints map[int]*BailoutInfo  // guard ID → info
    FrameLayout  FrameMapping
}

type BailoutInfo struct {
    BytecodeOffset int
    LiveValues      []LiveValueInfo
    Materializations []MaterializationInfo
}

type LiveValueInfo struct {
    ValueID    int
    RegSlot    int      // interpreter register slot
    ValueType  ValueType
}

type MaterializationInfo struct {
    ValueID      int
    Reconstruct   func(*Frame, int) Value
}

// Runtime deoptimization context
type DeoptContext struct {
    TraceID     int
    BailoutID   int
    LiveValues  []Value
    Frame       *Frame
}
```

### 5.3 Guard Types

```go
type GuardNode struct {
    GuardType   GuardKind
    Value       *SSAValue
    Expected    interface{}  // expected value/type
    BailoutID   int
    GuardCode   []byte        // generated guard code
}

type GuardKind int

const (
    GuardInt32 GuardKind = iota
    GuardFloat64
    GuardNotNil
    GuardBounds
    GuardTableShape
    GuardString
)

// Guard implementation (pseudo-code)
func emitCheckInt32(value *SSAValue, bailoutID int) []byte {
    return []byte{
        // Compare value tag to INT32_TAG
        BNE(value, Immediate(INT32_TAG), Address(bailoutID)),
    }
}
```

### 5.4 Eager Deoptimization Flow

```
1. Guard Fails (e.g., CheckInt32 sees string)
    ↓
2. Jump to Deopt Handler (pass BailoutID)
    ↓
3. Handler looks up BailoutInfo from metadata
    ↓
4. Materialize heap objects from live values
    ↓
5. Fill interpreter frame slots with live values
    ↓
6. Set interpreter PC to BailoutInfo.BytecodeOffset
    ↓
7. Continue in interpreter
```

### 5.5 Lazy Deoptimization Flow (Future)

```
1. Guard Fails (non-critical speculation)
    ↓
2. Set flag: Trace[traceID].DeoptPending = true
    ↓
3. Continue in optimized code
    ↓
4. At next safe point / function exit
    ↓
5. If DeoptPending, trigger eager deopt
    ↓
6. Disable trace for future compilations
```

## 6. Implementation Steps

### Phase 1: Foundation (1-2 weeks)
- [ ] Define deoptimization data structures
- [ ] Create guard generation framework
- [ ] Implement `CheckInt32` guard
- [ ] Implement simple eager deopt handler
- [ ] Add trace metadata storage

### Phase 2: Integer Speculation (1-2 weeks)
- [ ] Add type feedback collector to interpreter
- [ ] Speculate integer arithmetic operations
- [ ] Add integer overflow guards
- [ ] Benchmark integer-heavy workloads

### Phase 3: Array Bounds Elimination (1 week)
- [ ] Implement loop analysis pass
- [ ] Hoist bounds checks when provable
- [ ] Add guard for fallback cases
- [ ] Test with array benchmarks

### Phase 4: Table Shapes (2-3 weeks)
- [ ] Design table shape system (similar to hidden classes)
- [ ] Add shape tracking to interpreter
- [ ] Implement `CheckTableShape` guard
- [ ] Speculate on table property access

### Phase 5: OSR (3-4 weeks, optional)
- [ ] Analyze GScript frame layout
- [ ] Design interpreter-compatible frame
- [ ] Implement OSR entry points for loops
- [ ] Add frame migration logic

### Phase 6: Advanced (future)
- [ ] Lazy deoptimization
- [ ] Polymorphic inline caches
- [ ] Speculative inlining

## 7. Risk Mitigation

### 7.1 Deoptimization Overhead
**Risk**: Too many deoptimizations kill performance
**Mitigation**:
- Track deoptimization rate per function
- Disable speculation for "bouncy" code
- Prefer polymorphic paths over deopt

### 7.2 Memory Overhead
**Risk**: Deoptimization metadata increases memory usage
**Mitigation**:
- Store metadata in compact format
- Drop metadata for cold traces
- Share metadata where possible

### 7.3 Correctness Bugs
**Risk**: State reconstruction bugs are hard to debug
**Mitigation**:
- Extensive testing of deopt paths
- Add debug mode that validates reconstruction
- Built-in observability (dump deopt info on failure)

## 8. References

### V8 Official Documentation
- [Leaving the Sea of Nodes](https://v8.dev/blog/leaving-the-sea-of-nodes) - 2025-03-25
- [Sparkplug](https://v8.dev/blog/sparkplug) - 2021-05-27
- [Ignition Interpreter](https://v8.dev/blog/ignition-interpreter) - 2016-12-12

### Research Papers
- [Translation Validation for JIT Compiler in V8](https://kihongheo.kaist.ac.kr/publications/icse24.pdf) - ICSE 2024
- [Formally Verified Speculation and Deoptimization](https://dl.acm.org/doi/10.1145/3434327)
- [Type-Aware Optimizations with Imperfect Types](https://uwspace.uwaterloo.ca/bitstreams/074ed628-d39c-4316-963b-8b2cd73b8693/download)

### Technical Blog Posts
- [浅析V8-turboFan](https://blog.kiprey.io/2021/01/v8-turboFan/)
- [Understanding JIT Compilation in V8](https://medium.com/@rahul.jindal57/understanding-just-in-time-jit-compilation-in-v8-a-deep-dive-c98b09c6bf0c)
- [V8 Engine Type Speculative Optimization Principles](https://developer.cloud.tencent.com/article/2329790)

### Security Context
- [A Mere Mortal's Introduction to JIT Vulnerabilities](https://trustfoundry.net/2025/01/14/a-mere-mortals-introduction-to-jit-vulnerabilities-in-javascript-engines/) - 2025-01-14
- [CVE-2025-13223: Chrome V8 Type Confusion](https://www.freebuf.com/articles/network/418722.html) - 2025-11-12

---

**Document Version**: 1.0
**Last Updated**: 2026-03-27
**Researcher**: Claude (Anthropic)
**Context**: GScript Trace JIT Optimization Project
