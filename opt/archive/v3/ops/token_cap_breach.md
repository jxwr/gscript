# Tripwire T1 breach



Harness v3 Stage 1 tripwire T1 halted the round because cumulative token consumption exceeded the per-round hard cap. Investigate which phase(s) are excessive; the most likely cause is a PLAN_CHECK rewrite loop that's eating more than expected, or a sub-agent that's exploring instead of executing. Raise T1_PER_ROUND_TOKEN_CAP env var if the cap itself is wrong.
