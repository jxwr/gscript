# REVIEW Phase (harness self-audit)

You audit the **workflow itself** — not the compiler code.
Key input: the user's session log. The user's interventions are the highest-signal data about what the workflow gets wrong.

---

## Context — Read These (in order)

1. **Main session JSONL log** — extract user's typed messages to understand their interventions:
   ```bash
   # Find the main session (most lines, non-phase-prompt first message)
   MAIN=$(ls -t ~/.claude/projects/$(pwd | sed 's|[/_.]|-|g')/*.jsonl | head -5 \
     | while read f; do
       first=$(head -50 "$f" | jq -r 'select(.type=="user") | .message.content | if type=="string" then . else "" end' 2>/dev/null | head -1)
       [[ "$first" != "# "* ]] && echo "$f" && break
     done)
   # Extract user messages
   jq -r 'select(.type=="user" and (.message.content|type)=="string") | "\(.timestamp | split("T")[1][:8]) \(.message.content)"' "$MAIN" | tail -100
   ```
   Read the last ~100 user messages. These tell you what the user corrected, asked for, or redirected.

2. `opt/INDEX.md` — round history
3. `opt/state.json` — counters
4. `opt/plans/` — archived plans (last 3-5)
5. `opt/workflow_log.jsonl` — per-round metrics
6. `opt/initiatives/*.md` — active initiatives
7. `.claude/prompts/*.md` — current prompts (read them to see what's already been changed)
8. `.claude/optimize.sh` — current orchestrator config

---

## Task

### A. User Intervention Analysis (MOST IMPORTANT)

Read the user's session messages. For each message that **corrects, redirects, or requests a change** to the workflow:

1. **What** did the user say?
2. **Why** did they intervene? (What was the workflow doing wrong?)
3. **Was it already addressed?** Check if the relevant prompt/hook/script was already modified.
4. **Is there a pattern?** Multiple interventions pointing at the same root cause?

Classify interventions:
- **Already implemented** — user asked, it was done. No action needed.
- **Partially addressed** — the spirit was captured but the implementation is incomplete.
- **Not yet addressed** — user flagged an issue that hasn't been fixed in prompts/scripts.
- **Implicit feedback** — user's behavior (skipping phases, running ad-hoc diagnostics) reveals workflow gaps even without explicit complaints.

**DO NOT re-request changes the user already asked for and that are already implemented.**
The core goal is to **understand** the user's thinking, not to generate a to-do list.

### B. Workflow Statistics

Quick scan (subordinate to Section A):

| Check | What to look at |
|-------|----------------|
| Category distribution | Is one category dominating? |
| Outcome distribution | Too many no_change/abandoned? |
| Plan accuracy | Predictions vs actuals — are they converging after calibration? |
| Initiative health | Active but stalled? |
| Budget adherence | Overruns? |

### C. Process Understanding

Synthesize A + B into an understanding of:
- **What the workflow does well** (don't break these)
- **What the user keeps having to fix manually** (these are automation gaps)
- **What the user's implicit priorities are** (speed? correctness? architecture? breadth?)
- **Where the user's judgment differs from the harness's defaults** (these are calibration issues)

---

## Output

Write `opt/reviews/<date>-round<N>.md`:

```markdown
## Harness Review — Round [N]

### User Intervention Log
| Time | User Said | Why | Status |
|------|-----------|-----|--------|
| HH:MM | "..." | [interpretation] | implemented / partial / pending |

### Key Patterns in User Feedback
1. [pattern]: [what it means for the workflow]
2. [pattern]: ...

### Workflow Statistics
| Metric | Value | Healthy? |
|--------|-------|----------|
| Category concentration | X% in Y | ... |
| Plan accuracy (last 3) | ... | ... |
| Outcome distribution | ... | ... |

### What's Working (don't touch)
- [list things the user hasn't complained about or explicitly praised]

### Remaining Gaps
1. [specific gap, backed by user intervention data, NOT already implemented]
2. [...]

### Process Understanding
[2-3 sentences: what is the user trying to achieve at the meta level?
 What kind of workflow would make them stop intervening?]
```

---

## Reset Counters

After writing the review, update `opt/state.json`:
- Set `rounds_since_review` to `0`

## Rules

1. **User interventions are the #1 data source.** Statistics are secondary.
2. **Don't re-request what's done.** Read the current prompts/scripts BEFORE recommending changes. If the user asked for X and it's already in analyze.md, say "already implemented" not "recommend adding X".
3. **Understand before prescribing.** The goal is to model the user's thinking, not generate action items.
4. **Apply changes directly** if they're small and clearly warranted (you have Write access to `.claude/`). But note what you changed.
5. Keep it lightweight. Read, think, write. No sub-agents.
