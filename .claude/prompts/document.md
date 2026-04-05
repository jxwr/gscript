# DOCUMENT Phase

You are in the DOCUMENT phase of the GScript optimization loop.

## Context
Read these files:
1. `.claude/current_plan.md` — plan with results and lessons filled
2. `.claude/state.json` — optimization state
3. `docs-internal/architecture/overview.md` — architecture docs

## Task
1. **Update `.claude/state.json`**:
   - Set `cycle` to `""` (clear active cycle)
   - Set `next_action` to `""`
   - Add this round to `previous_rounds` array:
     ```json
     {
       "cycle_id": "[from current_plan.md]",
       "outcome": "improved|no_change|regressed|abandoned",
       "summary": "[one-line summary]"
     }
     ```
   - Clear `plan_budget`

2. **Archive the plan**:
   ```bash
   bash .claude/hooks/archive_plan.sh
   ```

3. **Update architecture docs** (only if architecture changed):
   - `docs-internal/architecture/overview.md`
   - `CLAUDE.md` (if mission/conventions changed)
   - Add ADR to `docs-internal/decisions/` for structural changes

4. **Append to workflow log**:
   Append one line to `.claude/workflow_log.jsonl`:
   ```json
   {"round":"CYCLE_ID","outcome":"improved|no_change|regressed","plan_accuracy":"predicted vs actual","budget_used":"N/M commits","drift_events":0}
   ```

5. **Commit all changes** with descriptive message if there are uncommitted files.

Do NOT write any implementation code. Your only job is to clean up and document.
