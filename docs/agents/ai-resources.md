# Managing AI resources

Use `.ai/` as the source for plugins, skills, rules, and MCP servers. The `manage-ai-resources` skill follows this convention when an agent handles an installation, update, or removal. Generated files belong to `ai-sync`; editing one directly is overwritten by the next sync.

For every change, inspect the current project and verify the result:

```sh
ai-sync convention
ai-sync inventory .
# Edit the relevant .ai/ source.
ai-sync inventory .
ai-sync --dry-run
ai-sync
ai-sync inventory .
```

`inventory` is read-only. `ai-sync` also runs entries from `.ai/setup.yaml` and installs missing enabled Claude plugins, even when a target is selected. Review its dry run before syncing a project with those declarations.

## Skill

To install a reusable skill from a reviewed local source, copy its **whole directory** to `.ai/skills/<name>/`. The directory needs a `SKILL.md` with `name` and `description` frontmatter. Keep scripts, references, and assets that the skill uses.

```sh
cp -R /path/to/reviewed-skill .ai/skills/reviewed-skill
ai-sync inventory .
ai-sync --dry-run
ai-sync
```

For an update, replace the canonical skill files while preserving project-specific edits and rerun the checks. For removal, delete `.ai/skills/<name>/` and sync; `ai-sync` removes generated copies it owns from Claude, Codex, and Kiro. Check the inventory's `sources` and `destinations` to confirm auxiliary files travel with the skill.

## Rule

A path-scoped rule lives at `.ai/rules/<name>.md`:

```md
---
paths:
  - "src/**/*.tsx"
---

# React component rule

Use the project's shared component library in this path.
```

Sync generates Claude rules, Kiro steering, and Codex `AGENTS.md` guidance. Codex attaches a glob to the nearest stable parent directory, so inspect its generated destination in `ai-sync inventory .` when the scope matters. Update the canonical Markdown to change the rule; delete that file and sync to remove it. Repo-wide rules belong in `.ai/project.md`, while agent-specific guidance belongs in `.ai/targets/<target>.md`.

## MCP server

Declare local command servers or remote HTTP servers under `servers` in `.ai/mcp.yaml`:

```yaml
servers:
  example:
    command: npx
    args:
      - -y
      - '@example/mcp-server@1.2.3'
    env:
      EXAMPLE_MODE: "readonly"
  docs:
    url: https://example.com/mcp
    bearer_token_env_var: DOCS_TOKEN
    headers:
      X-Region: us
```

`ai-sync` renders both entries into `.mcp.json`, `.codex/config.toml`, and `.kiro/settings/mcp.json`. A server uses either `command` (local stdio) or `url` (remote HTTP). HTTP URLs must use HTTPS, except `localhost` and loopback addresses. `bearer_token_env_var` names an environment variable available to each agent process; put the token in that environment, never in `.ai/`. `headers` is for nonsecret literal values. Native OAuth login remains a target-specific step when the remote server requires it.

Update the named entry to change its command, URL, or options. Remove the entry and sync to remove its generated settings. Verify that each target agent can start or reach the server; generation does not install local runtime dependencies, supply credentials, or complete OAuth login.

## Plugin

Plugins may need a different native install path for each agent. Keep the declaration in `.ai/plugins.yaml`, inspect any project-local package, and use the target's native installer through the [plugin lifecycle](plugin-lifecycle.md). For example, a Claude marketplace plugin:

```sh
ai-sync plugins add . toolkit@tools --target claude --repo example/tools
ai-sync plugins status .
ai-sync plugins updates . --refresh
```

The plugin lifecycle guide also covers updating, removing, and previewing Codex and Kiro variants. A plugin's bundled skills and MCP servers stay associated with that plugin unless they are deliberately maintained as independent canonical resources.
