# Portable plugin declarations

`.ai/plugins.yaml` accepts Claude's existing boolean entries and structured entries identified by a logical name. The logical name groups the package variants; it does not have to match the `name` in `plugin.json` or Claude's marketplace identity.

```yaml
marketplaces:
  tools:
    repo: example/tools
plugins:
  old-plugin@tools: true
  toolkit:
    enabled: true
    targets:
      claude:
        install: toolkit@tools
      codex:
        path: .ai/packages/toolkit
      kiro:
        path: .ai/packages/toolkit
  remote-tool:
    enabled: false
    targets:
      codex:
        git: https://example.com/remote-tool.git
        ref: 0123456789012345678901234567890123456789
        subdir: plugin
```

`enabled` and `targets` are required in a structured entry. Each target needs `path`, `git` or, for Claude only, `install`. `path` is relative to the project and must be inside `.ai/`. `git` is an HTTPS URL with a `ref` of 40 hexadecimal digits that identifies a commit; `subdir` is optional and cannot escape the repository. You can declare `install` together with a local package origin to keep the Claude installation and allow inspection. Two entries cannot claim the same Claude install identity.

The `ai-sync inventory <project-dir>` command inspects local packages without running commands. For Codex and Kiro it looks for the Agent Plugins `plugin.json` at the root, skills in `skills/<name>/SKILL.md` and servers in `mcp.json`. For a Claude-only package it recognizes `.claude-plugin/plugin.json` and `.mcp.json`, and explains why it cannot be used as an Agent Plugins package in Codex or Kiro. A Git source and a marketplace installation without a local copy stay `unverified`: their skill and MCP lists remain empty. Nothing is downloaded during the inventory.

Each structured entry produces a single `plugin` resource in `resources`. It keeps `type`, `name`, `sources`, `destinations` and `enabled` from the previous contract and adds `variants`, in the order Claude, Codex, Kiro. Each variant has `target`, `status`, `source`, `skills`, `mcp_servers`, `unsupported` and `requirements`; `manifest`, `package_name` and `version` appear when they can be verified. The statuses are `compatible`, `partial`, `incompatible`, `pending`, `unverified` and `invalid`. A missing local path stays `unverified` with the exact path in `requirements`, so the declaration can be copied without installing it. A package that is present but invalid produces a diagnostic and leaves the overall inventory at `invalid`; `unverified`, `pending`, `partial` and `incompatible` describe compatibility without turning the declaration into invalid YAML.

The inventory attributes the package's files to its plugin resource, so they do not reappear in `additional_files`. Skill and MCP names are shown inside the owning variant, without copying them as standalone canonical resources. An MCP name match across plugins is not treated as a collision because Kiro identifies them with a namespace. Two logical plugins that claim the same `package_name` for the same target do produce a collision diagnostic. The inventory does not install, register or run the inspected content.

Sync and `ai-sync plugins` keep the current Claude installation for boolean entries and for `targets.claude.install`. Codex and Kiro targets are not installed during sync: `ai-sync plugins preview` and `ai-sync plugins apply` use the states, requirements and omitted components from `variants` and keep installation as an explicit action.

For declarative Claude add, update, remove, project-scoped status, and cached marketplace version comparison, see [plugin lifecycle](agents/plugin-lifecycle.md).

Format references: [Agent Plugins plugin schema](https://agent-plugins.org/schemas/1.0.0/plugin.schema.json), [Agent Plugins MCP schema](https://agent-plugins.org/schemas/1.0.0/mcp.schema.json), [Codex plugin packaging](https://developers.openai.com/plugins/build/plugins), [Kiro Powers](https://kiro.dev/docs/powers/create/).

## Per-target preview

`ai-sync plugins preview <project-dir> --target codex|kiro` returns a JSON object on a single line. The project path is required; the target can only be `codex` or `kiro`. It does not modify files, install packages, run commands or download Git sources. `ai-sync plugins` without `preview` remains the explicit Claude installer. Ordinary sync still copies the declarations and keeps its current behaviour for Claude, without enabling Codex or Kiro plugins.

The object contains `target`, `fingerprint`, `plugins` and `diagnostics`. `plugins` is sorted by logical name and contains only the variants declared for the target. Each element keeps `name`, `enabled`, `package` (the declared origin), `manifest`, `version`, `native_identity` (`plugin.json.name`), `skills`, `mcp_servers`, `omitted_components`, `missing_requirements`, `state`, `action` and `conflicts`. Inventory states are preserved: `compatible`, `partial`, `incompatible`, `pending`, `unverified` or `invalid`. `partial` means the package contains unsupported components; its action is `pending` until a target-specific package is produced and validated. A Git source is not downloaded: it stays `unverified` with no install action. Disabled variants also have no action.

`action.kind` can be `command`, `manual`, `pending`, `blocked` or `none`. A compatible local Kiro variant shows `command: ["kiro-cli", "powers", "install", "<absolute package path>"]`, compatible with Kiro CLI 2.25 or later; in the IDE you can use Powers → Add Custom Power → Import power from a folder. For local Codex, `manual` means creating an entry in `.agents/plugins/marketplace.json` whose `source.path` points to the package relative to the project root, restarting Codex and running `codex plugin add <native_identity>@<marketplace-name>`. The preview does not promise that a loose folder can be installed directly with `codex plugin add`: Codex requires a marketplace. A `native_identity` or `package.path` collision between enabled plugins for the same target sets `action.kind: blocked` for both until you choose owners or different paths and identities. Skills and MCP servers stay inside their plugin; names repeated across packages are not treated as collisions.

`fingerprint` is the hexadecimal SHA-256 of the inventory contract and of the names and bytes of all files in `.ai/`, sorted by relative path. The fingerprint changes when package content, declarations, detected requirements or any other canonical file change, and it avoids depending on the project's absolute path. The preview of remote sources does not pin bytes that have not been fetched yet.

Native mechanisms: [Codex plugin packaging and marketplaces](https://developers.openai.com/plugins/build/plugins), [Codex plugin CLI](https://github.com/openai/codex/blob/main/codex-rs/cli/src/plugin_cmd.rs), [Kiro Powers installation](https://kiro.dev/docs/powers/installation/), [Kiro CLI 2.25 release](https://kiro.dev/changelog/cli/2-25/).

## Per-target apply

`ai-sync plugins apply <project-dir> --target codex|kiro --fingerprint <fingerprint> (--all | --plugin <name>)` applies the actions of a reviewed preview and returns a JSON object on a single line with `target`, `fingerprint` and `plugins`. Each element reports `name`, `state`, `status`, `error`, `command` and `instructions`. The apply statuses are `installed`, `manual`, `pending`, `unverified`, `incompatible`, `disabled` and `failed`.

The command re-inspects the project and compares the fingerprint with `--fingerprint`. If `.ai/` changed, the apply is rejected without installing anything and you must preview again: what gets installed is exactly what was reviewed. `--fingerprint` and the selection (`--all` or one or more `--plugin`) are required; logical names without a variant for the target are rejected.

A real `native_identity` or `package.path` collision cancels the whole apply with an error, without running any installer, until an owner or a different identity is chosen. `incompatible` and `unverified` variants are reported with their reason and are not installed: a Git source is still not downloaded during the apply. A `partial` package stays `pending` because partial installation needs a prepared and validated target-specific package, which is not produced yet; that is why there is no acceptance that forces it.

For Kiro, a `compatible` local variant runs `kiro-cli powers install <absolute path>`. For Codex, the apply runs nothing: it returns the concrete marketplace registration and `codex plugin add` instructions as `manual`, because installation happens inside the agent. No disabled variant is applied, and the apply does not install plugins during ordinary sync: copying `.ai/` and running `ai-sync` remain separate actions from installing plugins.
