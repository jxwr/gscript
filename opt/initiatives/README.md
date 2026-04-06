# Initiatives — Multi-Round Engineering Projects

Most single-round optimizations are tactical (one file, one fix). But closing the gap
to LuaJIT requires **architectural moves that span many rounds**:

- Tier 2 native BLR call (remove spill/reload overhead)
- Proper variadic IR model (`OpCallV` + top tracker)
- Escape analysis + scalar replacement
- LICM for Tier 2 float loops
- ...

Each of these is an **initiative** — a coordinated effort across multiple rounds,
tracked here. One file per initiative.

## Lifecycle

```
draft → active → complete | abandoned | paused
```

- **draft** — proposed but not started
- **active** — being worked on in rounds; ANALYZE should check if this should be the next round's target
- **paused** — temporarily shelved; blocked on something
- **complete** — delivered, success
- **abandoned** — tried, didn't work, lesson recorded

## Rules

1. **ANALYZE** must read all active initiatives before picking a target. If an initiative has a concrete next step, it's a strong candidate.
2. **PLAN** must link to the parent initiative in `Initiative:` header if applicable.
3. **DOCUMENT** must update the initiative's `Rounds` list and `Next Step` after each round that touched it.
4. An initiative's `Next Step` is what the next round should do. If empty, the initiative is stalled.

## See also

- `_template.md` — copy this when creating a new initiative
- `../INDEX.md` — flat index of all rounds across all initiatives
