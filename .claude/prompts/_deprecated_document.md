# DOCUMENT Phase

You are in the DOCUMENT phase of the GScript optimization loop.

## Context
Read these files:
1. `opt/current_plan.md` — plan with results and lessons filled (extract Category, Initiative, Cycle ID)
2. `opt/state.json` — optimization state
3. `opt/INDEX.md` — round index
4. `docs-internal/architecture/overview.md` — architecture docs

## Task

### 1. Update `opt/state.json`
Read current values first, then:
- Set `cycle`, `cycle_id`, `target`, `next_action` to `""`
- Clear `plan_budget`
- Append this round to `previous_rounds`:
  ```json
  {
    "cycle_id": "[from current_plan.md]",
    "category": "[from current_plan.md]",
    "initiative": "[path or standalone]",
    "outcome": "improved|no_change|regressed|abandoned",
    "summary": "[one-line summary]"
  }
  ```
- **Update `category_failures`**:
  - If outcome in (`abandoned`, `no_change`, `regressed`): `category_failures[CATEGORY] += 1`
  - If outcome is `improved`: `category_failures[CATEGORY] = 0` (reset)
- **Increment counters**: `rounds_since_review += 1`, `rounds_since_research += 1`

### 2. Update `opt/INDEX.md`
Prepend a new row to the table (newest first) with:
```
| [next #] | [cycle_id] | [date] | [category] | [target 1-line] | [outcome] | [key commit hash] | [1-line lesson from plan] |
```
Re-number existing rows if needed (leave the "Categories" appendix unchanged).

### 3. Update Initiative (if applicable)
If `current_plan.md` → Initiative is not "standalone":
- Open the initiative file (`opt/initiatives/X.md`)
- Append a row to its `Rounds` table
- Update its `Phases` checkboxes (mark done if phase completed this round)
- Update its `Next Step` based on this round's lessons
- If all phases done, set `Status: complete`
- If this round was abandoned and plan's lessons say "architecture wrong", set `Status: abandoned`

### 4. Archive the plan
```bash
bash .claude/hooks/archive_plan.sh
```

### 5. Append to workflow log
Append one line to `opt/workflow_log.jsonl`:
```json
{"round":"CYCLE_ID","category":"CATEGORY","outcome":"improved|no_change|regressed|abandoned","initiative":"path|standalone","plan_accuracy":"predicted vs actual","budget_used":"N/M commits","drift_events":0}
```

### 6. Update architecture docs (only if architecture changed)
- `docs-internal/architecture/overview.md`
- `CLAUDE.md` (if mission/conventions changed)
- Add ADR to `docs-internal/decisions/` for structural changes

### 7. Commit all changes
Scoped commit message describing what closed out.

Do NOT write any implementation code. Your only job is to clean up and document.
