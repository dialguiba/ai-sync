# JSON inventory

Run `ai-sync inventory <project-dir>` to inspect the `.ai/` of an explicit project. The path can be absolute or relative to the directory you run the CLI from. `inventory` only reads files: it does not sync, install plugins or run `setup.yaml` commands.

Standard output contains a JSON document terminated by a newline. `resources` includes shared and per-agent instructions, rules, agents, canonical skills, MCP servers, declared plugins, hook events and setup entries. Each skill groups `SKILL.md` and its auxiliary files in `sources`; `destinations` lists the paths currently generated. Files associated with resources are not repeated in `additional_files`. The remaining files in `.ai/` appear there until their resource types are added. The paths in `sources`, `destinations`, `additional_files` and `diagnostics.path` are relative to the project and use `/`.

```json
{
  "status": "ok",
  "resources": [
    {
      "type": "shared_instruction",
      "name": "project",
      "sources": [".ai/project.md"],
      "destinations": [".kiro/steering/project-conventions.md", "AGENTS.md", "CLAUDE.md"]
    },
    {
      "type": "agent_instruction",
      "name": "codex",
      "sources": [".ai/targets/codex.md"],
      "destinations": ["AGENTS.md"]
    },
    {
      "type": "mcp_server",
      "name": "playwright",
      "sources": [".ai/mcp.yaml"],
      "destinations": [".codex/config.toml", ".kiro/settings/mcp.json", ".mcp.json"]
    },
    {
      "type": "plugin",
      "name": "formatter@tools",
      "sources": [".ai/plugins.yaml"],
      "destinations": [".claude/settings.json"],
      "enabled": false
    }
  ],
  "plugin_configuration": {
    "marketplaces": {
      "tools": {"source": "github", "repo": "example/tools"}
    },
    "shared_env_names": ["TOOLS_TOKEN"]
  },
  "additional_files": [],
  "diagnostics": []
}
```

Each rule appears once as `path_rule`, with its name taken from the file without `.md`. Its destinations include the Claude and Kiro rules and the `AGENTS.md` files that Codex generates for the `paths` globs. Each agent appears once and points to its Claude destination. A malformed rule leaves `status` at `invalid`; the rule stays in `additional_files` and the other readable resources remain visible.

Each skill appears as a resource with `SKILL.md` and auxiliary files. Its destinations include Claude, Codex, Kiro Skills and Kiro Powers. For the inventory, `SKILL.md` must have YAML frontmatter with a non-empty `name` and `description`. This validation is stricter than the file copy performed by the sync command. Resources of type `skill` carry `status: "ok"` or `status: "invalid"`; a skill without `SKILL.md` stays visible with an invalid status, along with the other readable resources.

MCP servers are sorted by name and transformed to the three destinations. Plugins are sorted by identity; `enabled` reflects exactly the declared value, including `false`. Boolean entries keep their previous contract. Structured declarations add `variants` with per-target inspection, as described in [portable plugins](portable-plugins.md). The inventory does not verify installations or download remote packages. `plugin_configuration.marketplaces` preserves the source and location of each marketplace in the shared configuration; `shared_env_names` lists the `env` keys in order, without revealing their values, which may contain secrets. This data is not a standalone resource and is not attributed to an individual plugin. If `plugins.yaml` is missing, both fields contain empty collections.

A hook event has `.claude/settings.json` as its destination. A setup entry has `destinations: []`: its commands are used during sync but produce no destination file. A file with valid entries is represented by its resources and is not repeated in `additional_files`. The inventory never runs the declared commands.

`status` is `ok`, `missing` or `invalid`. If `.ai/` is missing, the status is `missing`. A required source that is absent or unreadable, a malformed canonical file or an invalid declaration leaves the status at `invalid`. Both statuses produce a non-zero exit code and keep the JSON document on stdout. Each diagnostic includes the affected path and the cause in `message`; an invalid MCP, plugin, marketplace, hook event or setup entry declaration also identifies the unit in `unit`. Resources follow the order shared instructions, per-agent instructions, rules, agents, skills, MCP servers, plugins, hook events and setup entries, with names sorted within each type. Additional files are sorted by path. Diagnostics are sorted by path, unit and message.

When a YAML declaration has an invalid shape or type, the inventory keeps the other valid declarations from the same file. This applies to MCP servers, marketplaces, plugins, hook events and setup entries. The diagnostic points at the file and the invalid unit; a YAML file with broken syntax produces a diagnostic for the whole file.
