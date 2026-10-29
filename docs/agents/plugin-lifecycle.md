# Plugin lifecycle

`ai-sync` keeps desired plugin configuration in `.ai/plugins.yaml`. Installation belongs to each agent. The catalog inspects packages declared in `.ai/`; a boolean Claude marketplace entry has no package variant to inspect even when Claude has installed it on the current machine.

The `manage-ai-resources` skill follows this plugin procedure. Run these commands directly when the source and target are already known.

For an interactive terminal menu, run `ai-sync plugins tui <project-dir>` (for example, `ai-sync plugins tui .`). It lists declared plugins and cached update status, supports adding, updating, and removing Claude project plugins, and previews Codex or Kiro variants before applying a selected action. It requires an interactive terminal and does not start the browser catalog. The menu uses the same commands and project scope described below.

## Claude marketplace plugins

In a project that already declares the `tools` marketplace:

```sh
ai-sync plugins add . toolkit@tools --target claude
ai-sync plugins status .
```

For a new marketplace, supply its source once:

```sh
ai-sync plugins add . toolkit@tools --target claude --repo example/tools
```

`add` writes `.ai/plugins.yaml`, renders Claude's generated configuration, and runs `claude plugin install toolkit@tools --scope project`. It installs the named plugin; rendering may also refresh other Claude configuration files from `.ai/`. If installation fails, the declaration stays in `.ai/plugins.yaml`; correct the cause and retry the install with `ai-sync plugins` or sync the project. An existing structured declaration needs its Claude target edited directly rather than being added as a second entry.

`status` returns JSON with each declared plugin's target variants and the Claude version installed **for that project path**. The `claude_installed` field does not count installations in other worktrees.

```sh
ai-sync plugins status .
ai-sync plugins updates .
```

`updates` compares the installed project version with the version exposed by the local Claude marketplace checkout. It reports `current`, `update_available`, `local_newer`, `different`, `unknown`, or `not_installed`. Numeric version segments can be ordered; `different` means the versions differ but their order is unknown. The checkout may be stale. To refresh declared remote marketplaces first, request it explicitly:

```sh
ai-sync plugins updates . --refresh
```

For a chosen update:

```sh
ai-sync plugins update . toolkit@tools --target claude
ai-sync plugins status .
```

`update` invokes Claude's native updater for the project. If the same logical plugin has a local package under `.ai/`, refresh that package separately and run `ai-sync inventory .` to verify its inspected version. `ai-sync` does not copy the machine's plugin cache into the project automatically.

To remove a boolean declaration and its project installation:

```sh
ai-sync plugins remove . toolkit@tools --target claude
ai-sync plugins status .
```

The command keeps the marketplace declaration and any other plugins. A structured plugin may have other target variants; edit only the intended target in `.ai/plugins.yaml` and use the agent's native uninstall operation.

## Codex and Kiro package variants

Declare each target's source in `.ai/plugins.yaml`, then use the existing preview and apply path. For example:

```yaml
plugins:
  toolkit:
    enabled: true
    targets:
      codex:
        path: .ai/packages/toolkit-codex
      kiro:
        path: .ai/packages/toolkit-kiro
```

```sh
ai-sync inventory .
ai-sync plugins preview . --target kiro
ai-sync plugins apply . --target kiro --fingerprint <preview-fingerprint> --plugin toolkit
ai-sync plugins preview . --target codex
```

Kiro apply invokes its native installer for a compatible local package. Codex apply returns marketplace registration and installation instructions; complete those in Codex and then inspect the target state. For updates, replace the package at the declared source and preview again. For removals, remove the target from the declaration and uninstall it with that agent's native mechanism. A `git` source stays unverified until a local package is prepared; pin it to a commit when declaring it. See [portable plugin declarations](../portable-plugins.md) for validation rules and statuses.
