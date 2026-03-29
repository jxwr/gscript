#!/bin/bash
# log_prompt.sh — Log every user prompt to a timestamped file
# Triggered by UserPromptSubmit hook

LOG_DIR="$(cd "$(dirname "$0")/../.." && pwd)/.claude/logs"
mkdir -p "$LOG_DIR"

# Get today's log file
LOG_FILE="$LOG_DIR/prompts_$(date +%Y-%m-%d).md"

# Read the JSON payload from stdin
INPUT=$(cat)

# Extract the prompt text (handle multi-line)
PROMPT=$(echo "$INPUT" | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    print(data.get('prompt', ''))
except:
    print(sys.stdin.read() if hasattr(sys.stdin, 'read') else '')
" 2>/dev/null || echo "$INPUT")

# Append to log with timestamp
echo "## $(date '+%H:%M:%S')" >> "$LOG_FILE"
echo "" >> "$LOG_FILE"
echo "$PROMPT" >> "$LOG_FILE"
echo "" >> "$LOG_FILE"
echo "---" >> "$LOG_FILE"
echo "" >> "$LOG_FILE"

# Allow the prompt through (exit 0)
exit 0
