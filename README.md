# ai-sync

<p align="center">
  <img src="logo.png" alt="ai-sync logo" width="250" />
</p>

`ai-sync` is a Go CLI that keeps AI-agent configuration in one canonical `.ai/` directory, then generates the files expected by Claude Code, Codex, and Kiro.

Instead of maintaining three separate sets of rules, MCP settings, and skills by hand, you edit `.ai/` once and let `ai-sync` render each agent-specific format.

## Why use it?

Use `ai-sync` when a repository needs consistent instructions across multiple AI coding agents.

| Problem | What `ai-sync` does |
| --- | --- |
| Claude, Codex, and Kiro each expect different files | Generates the right output files for each target |
| Shared project rules drift between agents | Keeps shared guidance in `.ai/project.md` |
| Agent-specific guidance gets mixed with global rules | Keeps overrides in `.ai/targets/<target>.md` |
| Skills need to be copied into several agent folders | Copies `.ai/skills/` into each target's expected location |
| Generated files can become stale | Uses manifests to prune files it owns safely |

## Install

```sh
go install github.com/gentle-ai/ai-sync/cmd/ai-sync@latest
```

Make sure Go's bin directory is in your `PATH`:

```sh
export PATH="$HOME/go/bin:$PATH"
```

Check that the CLI is available:

```sh
ai-sync --help
```

## Quick start

Run these commands inside the repository you want to configure:

```sh
ai-sync init       # create a starter .ai/ directory
ai-sync            # generate Claude, Codex, and Kiro files
ai-sync --dry-run  # preview changes without writing files
```

Generate only one target when needed:

```sh
ai-sync --target claude
ai-sync --target codex
ai-sync --target kiro
```

## Mental model

```txt
You edit this:

.ai/
  project.md
  mcp.yaml
  targets/
    claude.md
    codex.md
    kiro.md
  skills/
    example/
      SKILL.md

ai-sync generates this:

Claude Code  -> CLAUDE.md, .claude/settings.json, .mcp.json, .claude/skills/
Codex        -> AGENTS.md, .codex/config.toml, .agents/skills/
Kiro         -> .kiro/steering/project-conventions.md, .kiro/settings/mcp.json, .kiro/powers/
```

The rule is simple: **edit `.ai/`, not generated files**.

## Authoring `.ai/`

### Shared project guidance

Put repo-wide instructions in `.ai/project.md`.

Good examples:

- stack and architecture conventions
- build, test, lint, and verification commands
- naming, formatting, and import rules
- commit conventions
- review expectations
- generated-file ownership rules

Example:

```md
# Project Rules

## Commands

- Run tests with `go test ./...`.
- Format Go code with `gofmt`.

## Conventions

- Use conventional commits.
- Do not edit generated agent files directly; update `.ai/` instead.
```

### Target-specific guidance

Use `.ai/targets/<target>.md` only for instructions that apply to one agent.

| File | Use for |
| --- | --- |
| `.ai/targets/claude.md` | Claude Code-specific behavior or wording |
| `.ai/targets/codex.md` | Codex-specific workflow, planning, or review instructions |
| `.ai/targets/kiro.md` | Kiro-specific steering guidance |

If the same rule appears in more than one target file, it probably belongs in `.ai/project.md` instead.

### MCP servers

Define shared MCP servers in `.ai/mcp.yaml`:

```yaml
servers:
  playwright:
    command: npx
    args:
      - -y
      - '@playwright/mcp@latest'
    env:
      PLAYWRIGHT_HEADLESS: "true"
```

`ai-sync` maps that config to each agent's expected file:

| Canonical source | Claude | Codex | Kiro |
| --- | --- | --- | --- |
| `.ai/mcp.yaml` | `.mcp.json` | `.codex/config.toml` | `.kiro/settings/mcp.json` |

Do not put secrets directly in `.ai/mcp.yaml`. Reference environment variables instead.

### Skills

Put reusable workflows in `.ai/skills/<name>/SKILL.md`:

```txt
.ai/skills/playwright-cli/
  SKILL.md
  scripts/
  references/
  assets/
```

Example `SKILL.md`:

```md
---
name: "playwright-cli"
description: "Use when browser automation or Playwright CLI validation is needed."
---

# Playwright CLI

1. Inspect the app route before writing tests.
2. Prefer stable selectors.
3. Capture screenshots only when they help debugging.
```

Generated mapping:

| Source | Generated output |
| --- | --- |
| `.ai/skills/<name>/SKILL.md` | `.claude/skills/<name>/SKILL.md` |
| `.ai/skills/<name>/SKILL.md` | `.agents/skills/<name>/SKILL.md` |
| `.ai/skills/<name>/SKILL.md` | `.kiro/powers/<name>/POWER.md` |

Supporting files inside the skill folder are copied too.

## Generated ownership

`ai-sync` writes generated headers and `.ai-sync-manifest` files for generated skill directories.

Those manifests let the CLI prune stale generated files without deleting user-owned files added manually inside generated folders.

Practical rule:

- files listed in `.ai-sync-manifest` are owned by `ai-sync`
- files you add manually but that are not listed in the manifest are preserved

## Local development

Use this flow when working on `ai-sync` itself:

```sh
git clone git@github.com-home:dialguiba/ai-sync.git
cd ai-sync
go test ./...
go run ./cmd/ai-sync --help
```

Run the CLI from source:

```sh
go run ./cmd/ai-sync init
go run ./cmd/ai-sync
go run ./cmd/ai-sync --target codex
go run ./cmd/ai-sync --dry-run
```

## Current limitations

Kiro Powers are generated as valid importable folders. `ai-sync` does not install or register them in the local Kiro app.

Path-scoped rules are not implemented yet. The recommended future shape is:

```txt
.ai/rules/
  frontend.md
  backend.md
  docs.md
```

Until that feature exists, keep shared guidance in `.ai/project.md` and target-specific guidance in `.ai/targets/`.
