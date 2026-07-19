# ai-sync

<p align="center">
  <img src="logo.png" alt="ai-sync logo" width="250" />
</p>

`ai-sync` is a Go CLI that reads a canonical `.ai/` directory in a repository and generates agent-specific configuration for Claude Code, Codex, and Kiro.

## Installation

### Install with Go

```sh
go install github.com/dialguiba/ai-sync/cmd/ai-sync@latest
```

Make sure Go's bin directory is in your `PATH`:

```sh
export PATH="$HOME/go/bin:$PATH"
```

Verify the binary is available:

```sh
ai-sync --help
```

### Local development

Use this flow when you want to work on the `ai-sync` source code:

```sh
git clone git@github.com-home:dialguiba/ai-sync.git
cd ai-sync
go test ./...
go run ./cmd/ai-sync --help
```

## Quick start

Run these commands inside any repository where you want to generate agent configuration:

```sh
ai-sync init            # scaffold a starter .ai/ directory
ai-sync                 # generate all targets
ai-sync --target codex  # generate one target: claude, codex, or kiro
ai-sync --dry-run       # show what would be written
```

During local development, use `go run` instead of the installed binary:

```sh
go run ./cmd/ai-sync init
go run ./cmd/ai-sync
```

## Canonical source

```txt
.ai/
  project.md
  mcp.yaml
  skills/
    example/
      SKILL.md
  targets/
    claude.md
    codex.md
    kiro.md
```

## Generated outputs

| Target | Outputs |
| --- | --- |
| Claude Code | `CLAUDE.md`, `.claude/settings.json`, `.mcp.json`, `.claude/skills/` |
| Codex | `AGENTS.md`, `.codex/config.toml`, `.agents/skills/` |
| Kiro | `.kiro/steering/project-conventions.md`, `.kiro/settings/mcp.json`, `.kiro/powers/<skill>/` |

Kiro Powers are generated as valid importable folders. `ai-sync` does not install or register them in the local Kiro app.

## `.ai/` authoring standard

Use `.ai/` as the only hand-edited source of truth. Generated files should not be edited directly.

### Project rules: `.ai/project.md`

Use this file for repo-wide instructions that should apply to every agent.

Good content:

- stack and architecture conventions
- build, test, lint, and verification commands
- naming, imports, formatting, and commit conventions
- review expectations and “definition of done”
- constraints such as “do not edit generated files”

Example:

```md
# Project Rules

## Commands

- Run tests with `go test ./...`.
- Format Go code with `gofmt`.

## Conventions

- Use conventional commits.
- Keep generated agent files out of manual edits; update `.ai/` instead.
```

### Target overrides: `.ai/targets/<target>.md`

Use target overrides only when one agent needs extra instructions that should not apply everywhere.

| File | Use for |
| --- | --- |
| `.ai/targets/claude.md` | Claude Code-specific behavior or wording |
| `.ai/targets/codex.md` | Codex-specific workflow, planning, or review instructions |
| `.ai/targets/kiro.md` | Kiro-specific steering guidance |

Keep shared rules in `.ai/project.md`. If you copy the same rule into multiple target files, it probably belongs in `project.md`.

### MCP servers: `.ai/mcp.yaml`

Use `.ai/mcp.yaml` for MCP servers that should be available to generated targets.

Supported shape today:

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

Mapping:

| Canonical field | Claude | Codex | Kiro |
| --- | --- | --- | --- |
| `servers.<name>.command` | `.mcp.json` | `.codex/config.toml` | `.kiro/settings/mcp.json` |
| `servers.<name>.args` | `.mcp.json` | `.codex/config.toml` | `.kiro/settings/mcp.json` |
| `servers.<name>.env` | `.mcp.json` | `.codex/config.toml` | `.kiro/settings/mcp.json` |

Do not put secrets directly in `mcp.yaml`. Reference environment variables instead.

### Universal skills: `.ai/skills/<name>/SKILL.md`

Use skills for repeatable workflows that are bigger than a normal rule.

```txt
.ai/skills/playwright-cli/
  SKILL.md
  scripts/
  references/
  assets/
```

`SKILL.md` should use frontmatter:

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

Mapping:

| Canonical skill | Generated output |
| --- | --- |
| `.ai/skills/<name>/SKILL.md` | `.claude/skills/<name>/SKILL.md` |
| `.ai/skills/<name>/SKILL.md` | `.agents/skills/<name>/SKILL.md` |
| `.ai/skills/<name>/SKILL.md` | `.kiro/powers/<name>/POWER.md` |

Supporting files inside the skill folder are copied with the skill.

## Proposed rule scopes

Today, `ai-sync` supports repo-wide rules and target-specific overrides. Path-scoped rules are the next convention to add.

Recommended future shape:

```txt
.ai/rules/
  frontend.md
  backend.md
  docs.md
```

Proposed frontmatter:

```md
---
applies_to:
  - "frontend/**"
  - "*.tsx"
targets:
  - claude
  - codex
  - kiro
---

# Frontend Rules

- Use atomic components.
- Keep container and presentational responsibilities separate.
```

Proposed mapping:

| Scope | Claude | Codex | Kiro |
| --- | --- | --- | --- |
| Repo-wide | `CLAUDE.md` | `AGENTS.md` | `.kiro/steering/project-conventions.md` |
| Path-scoped | nested `CLAUDE.md` or sectioned root file | nested `AGENTS.md` or sectioned root file | steering file with inclusion metadata |
| Target-specific | `.ai/targets/claude.md` | `.ai/targets/codex.md` | `.ai/targets/kiro.md` |

Do not implement a path-scoped rule by duplicating it manually into every target file. Add it once under `.ai/rules/` once the feature exists, then let `ai-sync` render each target format.

## Generated ownership

`ai-sync` writes generated headers and `.ai-sync-manifest` files for generated skill directories. Those manifests let the CLI prune stale generated files without deleting user-owned files added manually inside the generated folders.
