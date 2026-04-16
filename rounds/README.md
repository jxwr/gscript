# rounds/

Structured round cards — one per round, append-only, never edited after close.

## Lifecycle

```
Step 3 (Direction):   Create rounds/NNN.yaml from TEMPLATE.yaml.
                      Fill: identity, type, target, hypothesis, class_gate.
                      Commit-unfriendly at this stage (outcome still pending).

Step 4 (Pre-flight):  Fill pre_flight.required and .result. (Wave 3)

Step 5 (Act):         No edits to the round card during coding.

Step 6 (Verify):      Fill outcome (status, bench_deltas, regressions).
                      If status=revert, fill the four revert_* fields.

Step 7 (Close):       Fill ledger_update. Commit the card alongside the
                      closing round commit (same commit = atomic history).
```

## Rules

1. **Never edit a closed card.** History is append-only. Mistakes get fixed in the NEXT round's card.
2. **`class` must match `program/ledger.yaml`.** Introducing a new class is allowed but requires setting `ledger_update.new_class_introduced`.
3. **`class_gate.ledger_consulted` must be `true`.** Step 3 is invalid otherwise.
4. **`counterfactual_check` is not optional.** This is the written-simulation gate for model-capability (Pillar 3). Empty = the round didn't run Step 3.
5. **`outcome.commit` is filled last.** Card commits and gets the sha written in a follow-up amendment only if the outcome commit itself is too early to know its own sha — otherwise commit the card with the final sha in the same commit.

## Naming

`rounds/R001.yaml` through `rounds/R999.yaml`. Zero-padded to 3 digits.

## Reading discipline (Step 0, Recap)

Each round's Step 0 reads:
- Last N = 8 round cards
- All revert cards from the last 12 rounds (for autopsy context)
- `program/ledger.yaml` in full

This list is enumerated in `CLAUDE.md` and is the core of v5 Pillar 3. The agent uses this reading to produce the recap artifact (written into the round card's `class_gate` section, not a separate file).

## What does NOT go in a round card

- Prose narrative ("I was thinking...") — belongs in the commit message
- Code snippets — they live in diffs
- KB edits — KB cards are the authority for invariants
- Benchmarks or plots — `benchmarks/data/` and `diag/` own those
- Any field not in `TEMPLATE.yaml` — extend the template via a meta-round

## Backfill

R001–R006 are backfilled from commit messages (see each card's header). They
use the best-effort reconstruction of what the hypothesis and class *were* at
the time. Their `class_gate.ledger_consulted` is `false` because the ledger
didn't exist yet — this is the one permitted violation and marks the v4→v5
transition.
