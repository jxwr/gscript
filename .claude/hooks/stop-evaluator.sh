#!/bin/bash
# Stop Hook Evaluator
# When Claude stops and waits for user input, this hook launches a sub-agent
# (Claude Haiku) to evaluate whether to auto-continue or genuinely wait.
#
# - Prevents infinite loops via stop_hook_active flag
# - Extracts last assistant message from transcript
# - Asks Haiku to judge: continue or wait?
# - Defaults to continue unless serious issue detected

set -o pipefail

INPUT=$(cat)

# ── Guard: prevent infinite loops ──
STOP_HOOK_ACTIVE=$(echo "$INPUT" | jq -r '.stop_hook_active // false')
if [ "$STOP_HOOK_ACTIVE" = "true" ]; then
  exit 0
fi

# ── Read transcript ──
TRANSCRIPT_PATH=$(echo "$INPUT" | jq -r '.transcript_path // empty')
if [ -z "$TRANSCRIPT_PATH" ] || [ ! -f "$TRANSCRIPT_PATH" ]; then
  exit 0
fi

# Extract last assistant message text (from last 200KB of transcript)
LAST_ASSISTANT=$(tail -c 200000 "$TRANSCRIPT_PATH" 2>/dev/null \
  | jq -R 'fromjson? // empty' 2>/dev/null \
  | jq -s '
    [.[] | select(.role == "assistant")] | last |
    if .content | type == "array" then
      [.content[] | select(.type == "text") | .text] | join("\n")
    elif .content | type == "string" then
      .content
    else
      ""
    end
  ' 2>/dev/null)

# Nothing to evaluate
if [ -z "$LAST_ASSISTANT" ] || [ "$LAST_ASSISTANT" = '""' ] || [ "$LAST_ASSISTANT" = "null" ]; then
  exit 0
fi

# Truncate to 3000 chars to keep Haiku call fast and cheap
LAST_ASSISTANT=$(echo "$LAST_ASSISTANT" | head -c 3000)

# ── Sub-agent evaluation via Claude Haiku ──
read -r -d '' EVAL_PROMPT << 'PROMPT_END' || true
You are a stop-hook evaluator for Claude Code. The main agent stopped and is waiting for user input. Analyze the LAST ASSISTANT MESSAGE below and decide whether work should auto-continue.

Reply with ONLY valid JSON (no markdown, no fences):
{"continue": true, "reason": "brief message"}
or
{"continue": false, "reason": "brief message"}

DECISION RULES:
continue=TRUE (default) when:
- Agent asked a simple yes/no or "should I continue?" question
- Agent reported partial progress and seems to have more work to do
- Agent is asking for confirmation on a routine/safe action
- Agent finished one step and could proceed to the next
- Any ambiguous case — default to true

continue=FALSE only when:
- Task is genuinely COMPLETE (final summary, all work done, nothing left)
- Agent is at a GATE between optimization phases (waiting for human approval to proceed)
- Agent is asking a question that requires genuine user decision (not a rubber stamp)
- Serious error/exception that agent cannot recover from
- Agent needs credentials, secrets, or access it cannot obtain
- Action involves irreversible data loss or destructive operations
- Agent explicitly asks user to choose between fundamentally different approaches

Keep "reason" under 80 chars. If continue=true, reason should be an encouraging instruction like "请继续" or describe what to do next.

LAST ASSISTANT MESSAGE:
PROMPT_END

# ── Inject optimization cycle context ──
CYCLE_CONTEXT=""
STATE_FILE="$(cd "$(dirname "$0")/../.." && pwd)/.claude/state.json"
if [ -f "$STATE_FILE" ]; then
    CYCLE=$(python3 -c "import json; d=json.load(open('$STATE_FILE')); print(d.get('cycle',''))" 2>/dev/null || echo "")
    TARGET=$(python3 -c "import json; d=json.load(open('$STATE_FILE')); print(d.get('target',''))" 2>/dev/null || echo "")
    NEXT=$(python3 -c "import json; d=json.load(open('$STATE_FILE')); print(d.get('next_action',''))" 2>/dev/null || echo "")
    if [ -n "$CYCLE" ]; then
        CYCLE_CONTEXT="
CURRENT OPTIMIZATION PHASE: $CYCLE
TARGET: $TARGET
NEXT ACTION: $NEXT
IMPORTANT: If the agent is asking for approval before proceeding to the next phase, continue=FALSE (this is a GATE that requires human input). If the agent has finished the current phase's work and needs to move on, continue=TRUE."
    fi
fi

FULL_PROMPT="${EVAL_PROMPT}${CYCLE_CONTEXT}

LAST ASSISTANT MESSAGE:
${LAST_ASSISTANT}"

EVALUATION=$(echo "$FULL_PROMPT" | claude -p --model haiku 2>/dev/null) || {
  # If sub-agent call fails, default to continue
  jq -n '{
    hookSpecificOutput: {
      hookEventName: "Stop",
      decision: "block",
      reason: "Sub-agent evaluation failed, defaulting to continue. 请继续。"
    }
  }'
  exit 0
}

# ── Parse evaluation result ──
SHOULD_CONTINUE=$(echo "$EVALUATION" | jq -r '.continue // true' 2>/dev/null)

if [ "$SHOULD_CONTINUE" != "false" ]; then
  REASON=$(echo "$EVALUATION" | jq -r '.reason // "请继续"' 2>/dev/null)
  [ -z "$REASON" ] && REASON="请继续"
  jq -n --arg reason "$REASON" '{
    hookSpecificOutput: {
      hookEventName: "Stop",
      decision: "block",
      reason: $reason
    }
  }'
else
  # Genuinely needs user input — let it stop
  exit 0
fi
