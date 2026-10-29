# Plugin portability through native packages

A plugin stays a single logical unit with variants for Claude Code, Codex and Kiro. ai-sync will use each agent's native mechanism when it can validate a compatible package, instead of turning the plugin-provided skills into standalone canonical resources in `.ai/skills/`. This preserves the plugin's provenance and its update as a whole; the cost is inspecting and, where needed, preparing one variant per target before installing it.
