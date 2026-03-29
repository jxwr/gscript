# Documentation Update Protocol

When the user makes an architectural decision or changes a core design during conversation:

1. Identify what changed and what it contradicts
2. Update CLAUDE.md if it affects mission, non-goals, or conventions
3. Write or update an ADR in `docs-internal/decisions/` for structural decisions (new tier, new pass, architecture change)
4. Update `docs-internal/architecture/overview.md` if it affects the pipeline, tiers, or register convention
5. Tell the user which docs were updated and why

Never leave a conversation after an architecture decision without updating docs.
