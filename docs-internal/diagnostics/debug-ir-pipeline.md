# Debug: IR Pipeline Issues

## Symptom

An optimization pass produces wrong IR: missing instructions, wrong types, orphan blocks, or unattached phi nodes.

## Step 1: Run validator

```go
errs := methodjit.Validate(fn)
for _, e := range errs {
    t.Log(e)
}
```

The validator checks:
- All blocks terminated by exactly one branch instruction
- Successor/predecessor consistency (bidirectional)
- SSA dominance (every value used is defined before use)
- Type consistency (no mixing int and float in same instruction)
- No orphan blocks (every block reachable from entry)
- Unique value IDs

Run `Validate()` after EVERY pass in the pipeline. The first pass that produces errors is the culprit.

## Step 2: Dump IR before/after

```go
// Before pass
before := methodjit.Print(fn)

// Run pass
fn = MyPass(fn)

// After pass
after := methodjit.Print(fn)

// Compare
t.Logf("Before:\n%s", before)
t.Logf("After:\n%s", after)
```

## Step 3: Read only the offending pass

Each pass is in `pass_<name>.go`. Read ONLY that file. Do not read other passes — the pipeline is designed so each pass is independent.

Common pass bugs:
- **Forgot to update predecessors/successors** when modifying block structure
- **Created value without unique ID** — use `fn.NewValue()`
- **Left block unterminated** — every block must end with a branch
- **Phi node with wrong block count** — phi inputs must match predecessor count

## Step 4: Use IR interpreter as oracle

```go
result, err := methodjit.Interpret(fn, args)
// Compare with VM
vmResult := vm.Execute(proto, args)
```

If `Interpret()` matches `VM.Execute()`, the IR is correct. The bug is in a later stage (regalloc or emit).

If `Interpret()` differs, the bug is in BuildGraph or a pass. Use `Validate()` to find which pass.

## Pass pipeline order

```
BuildGraph → Validate → TypeSpecialize → Validate → ConstProp → Validate → DCE → Validate → Inline → Validate → RegAlloc → Emit
```

Validation after EVERY pass. If a pass breaks the IR, you know exactly which one.
