# Plan: catalog and `.ai/` portability

Status: exploration. This document preserves the product idea, the agreed decisions and the open ones; it is not yet an implementation specification.

## Objective

Let a person view, create, copy and share AI configurations maintained by ai-sync. The `.ai/` folder remains the editable source. The experience must work with projects open in Orca and with any accessible directory, even if the final interface is a standalone desktop application.

## Use cases

1. **Explore a project.** See the resources in its `.ai/`: project instructions, per-agent instructions, rules, skills, MCP servers, declared plugins, hooks, agents and setup configuration. Open each resource's source file and see which targets ai-sync generates files for.
2. **Create `.ai/`.** Choose a project or directory without `.ai/`, review which initial files will be created and run the initialization. You can start from the current `ai-sync init` template or from an existing `.ai/` folder.
3. **Replicate a whole folder.** Choose a source `.ai/` and a target project or directory. Compare before copying, resolve matching files and then sync the target if the person asks for it.
4. **Copy selected resources.** Choose one or more skills, agents, rules or other resources from the source and copy them to the target without carrying the whole folder. Dependencies between resources, if any, must be shown before copying.
5. **Preview.** Show separately (a) the changes to the target's `.ai/` and (b) the Claude Code, Codex and Kiro files that ai-sync would generate or delete. Distinguish new, modified, identical and conflicting files; allow viewing content differences.
6. **Detect changes** (desirable, not essential for the first version). Indicate when a project's `.ai/` has changed since the last inspection or import and allow refreshing the preview. Define later whether drift between `.ai/` and generated files should also be detected.
7. **Share with another person.** Export a whole `.ai/` folder or a selected set of resources in a portable format. Whoever receives it must be able to inspect the content and the proposed changes before importing it, whether or not they have Orca.

## Scope of the current resources

| Canonical source | What it represents | Current targets |
| --- | --- | --- |
| `.ai/project.md` | Shared instructions | Claude Code, Codex, Kiro |
| `.ai/targets/*.md` | Target-specific instructions | Corresponding target |
| `.ai/rules/*.md` | Path rules | Claude Code, Codex, Kiro |
| `.ai/skills/<name>/` | Skills and auxiliary files | Claude Code, Codex, Kiro |
| `.ai/agents/*.md` | Subagents in Claude Code format | Claude Code |
| `.ai/mcp.yaml` | MCP servers | Claude Code, Codex, Kiro |
| `.ai/plugins.yaml` | Marketplaces and Claude Code plugins | Claude Code |
| `.ai/hooks.yaml` | Hooks | Claude Code |
| `.ai/setup.yaml` | Per-machine preparation commands | Run during `ai-sync` |

Plugins declared in `.ai/plugins.yaml` currently use the **Claude Code plugins** contract and only generate configuration for that target. This describes a current limitation of ai-sync, not of Codex or Kiro: Codex supports plugins and Kiro supports Powers in the Agent Plugins format. A plugin's portability between agents depends on its package and the capabilities it uses; it cannot be inferred from its name in `plugins.yaml` alone. A possible Orca plugin would only be a way to access the catalog. ai-sync also has no `.ai/commands/` source today; that concept must be defined before promising reusable commands as a resource type.

References for the current capabilities: [Codex plugins](https://developers.openai.com/plugins/build/plugins), [Kiro Powers](https://kiro.dev/docs/powers/installation/) and [Claude Code plugins](https://code.claude.com/docs/en/plugins).

## Product approach

The catalog opens on a chosen project and lists the projects registered in Orca in the left panel. Selecting another project reads its `.ai/` from an available local worktree. It must distinguish the origin and the owner of each resource. The engine remains independent of Orca and accepts explicit paths.

Separate the logic for reading, comparing, copying and previewing from the interface:

```text
.ai/ folders ──> ai-sync engine ──> CLI with structured output
                                     ├── Orca integration
                                     └── desktop application
```

The engine must accept explicit source and target paths. It must not depend on a directory being registered in Orca. The Orca integration can make it easier to choose the active project or worktree; the standalone app can let you pick any directory. Both must use the same operations and the same preview format.

A first interface in Orca could open a local view in a tab. A plugin with a panel and commands is a later option, subject to the real capabilities of the plugin API in the chosen version. The standalone desktop app remains an alternative without changing the engine.

## Proposed engine operations

The names are tentative, to describe contracts rather than fix the CLI syntax yet.

- `inventory`: read an `.ai/` and return resources, source files, available metadata and compatible targets as JSON.
- `init preview` / `init`: plan and create an `.ai/` in a chosen directory.
- `copy preview` / `copy`: compare source and target, select resources and apply the approved plan.
- `sync preview` / `sync`: show and apply generated outputs. The current `--dry-run` covers part of this need, but the interface will need structured changes and content differences.
- `export` / `import preview` / `import`: prepare a portable package, inspect it and apply it to a target.

Read and preview operations must not install plugins, run `setup.yaml` or modify files. Applying a plan must re-check that the target has not changed since the preview; if it has, it must ask for a fresh review of the conflicts. Copying `.ai/` and running sync are separate actions so that their effects are visible.

Copying or importing `.ai/` leaves plugin installation pending. The interface offers the explicit "Install plugins" action after showing, per target, what will be installed and what will be left out.

The catalog shows a plugin as a single entity even when it has different packages for Claude, Codex and Kiro. Each variant keeps its origin and its per-target compatibility state. If a variant needs a variable or path that is missing in the target, the preview states the exact requirement and leaves its installation pending.

In the interface, "Generate files" applies only the generated outputs and "Install plugins" is a separate action; both show their effects before being applied. The `ai-sync` command keeps, for now, its existing behaviour of installing Claude plugins during sync. The interface must not invoke that whole flow for an action labelled only as generation.

After importing `setup.yaml`, its `check` and `install` commands require explicit acceptance in the target before running for the first time. The preview shows the exact commands. This rule must also protect CLI usage, where `ai-sync` currently runs the checks during ordinary sync.

Acceptance of a setup entry is tied to the content of its commands. If `check` or `install` changes, the previous acceptance expires and the new version must be reviewed before running it.

## Copy and conflicts

"Replicate" means, for now, a **one-off copy**: the target is not linked to the source and does not automatically receive its later changes. **TODO:** allow comparing the projects again after a copy.

- A full copy must preserve the `.ai/` structure and each skill's auxiliary files.
- Selective copy must work with recognizable resources and also show the files each resource comprises.
- In a target with matching files, the preview must offer at least keeping the target, replacing it or reviewing the difference. An automatic merge policy will only be added where the format and its rules are clear; YAML and Markdown must not be concatenated blindly.
- In a selective copy, if the target already contains a resource with the same identity, the person chooses whether to keep or replace it after seeing the difference. The other resources in a shared YAML are preserved. Before applying, the target is checked again; if it changed since the preview, a new review is required.
- If two resources would produce the same identity or path in a target, the preview identifies both and asks for a resolution before applying. It does not assign automatic suffixes such as `-2`. When the target distinguishes resources by namespace, their full names are shown and a non-existent collision is not reported.
- Never delete target resources just because they do not appear in the source during an ordinary copy. An exact replica with deletions, if wanted, must be an explicit operation with its own preview.
- Change detection can start with a comparison on open or refresh; continuous file system watching is left for a later stage.

A plugin-provided resource is also shown under its type (for example, in the skills view), with its source plugin. It can be inspected but not managed or copied as a standalone resource: the operation selects the responsible plugin and shows the resources it provides. The interface must avoid calling a usable skill "disabled" if the only thing restricted is its individual management.

Copying a selected plugin includes its declaration and the marketplace it depends on. The shared `env` block of `plugins.yaml` is shown so the person chooses its values separately; they are not automatically attributed to a plugin.

Selection within the other YAML files uses units with stable identity: each `mcp.yaml` server by name, each `setup.yaml` entry by name and each `hooks.yaml` event as a whole. Copying a unit preserves the other entries in the target file.

## Sharing

You will be able to share a whole `.ai/`, an individual resource or several selected resources in a single package. A full export also preserves the `.ai/` files that are not identified resources; the inventory shows them as additional files. A selective export includes the chosen resources and the approved dependencies.

The base format must be portable and usable without Orca: a folder or compressed archive containing `.ai/` and, if needed, an export manifest with the format version and the resource list. It must not depend on a hosted service to be opened or imported.

Distribution options to evaluate: exported file, Git repository or a link generated by an optional service. Before exporting or importing, show files and references to environment variables. Check especially `env` values, commands and local paths in MCP, plugins, hooks and setup; a shared package should not disclose secrets or run commands when inspected. Installing plugins or running setup would still be a later, explicit action.

On import, the preview shows the absolute paths and the literal `env` values so the person explicitly accepts them. They are not rewritten automatically.

An export of plugins records the exact version or commit of the inspected package; if it cannot pin it, it warns. If the source is a local folder, the export includes its inspected content so the package works on another machine, subject to the review of files and references before sharing it.

A new version of a plugin does not replace the pinned version automatically: the person requests the update and reviews a new preview. If the export finds literal `env` values or files that could contain secrets, it stops until the person reviews each flagged item and chooses whether to include or exclude it.

When a target supports only part of a plugin, ai-sync prepares a target-specific package and previews the kept and omitted components. Partial installation requires express acceptance. If it cannot produce a valid package for the target, it leaves the installation pending.

## Suggested sequence

1. **Portable base:** structured inventory and resource models; access to any directory.
2. **Creation and copy:** preview of `.ai/` changes, full and selective copy, conflict resolution.
3. **Plugin portability:** register plugins compatible with Codex and Kiro through their native mechanisms, with provenance, compatibility and effects visible before installing.
4. **Project view:** catalog with search, filters, detail, differences and preview of generated outputs.
5. **Sharing:** export and import of the portable format, with prior inspection.
6. **Integrations:** entry from Orca and, depending on its API capabilities, a plugin with commands or a panel; standalone desktop application if it offers a better experience.
7. **Improvements:** automatic change detection, drift comparison and optional distribution channels.

## Tasks

- [ ] [Support plugins compatible with Codex and Kiro from `.ai/`](https://github.com/dialguiba/ai-sync/issues/1): define the portable declaration, inventory the provided resources, preview compatibility and conflicts per target, and apply the native registration or installation when possible. Copying `.ai/` and installing remain separate steps.

## Getting ready to start

You can already start with the read-only inventory. The choice of interface, the distribution mechanism and the meaning of a continuous replica do not block that work.

**First testable milestone:** from any project directory, get a stable JSON that lists the resources declared in `.ai/`, their type, name, source path and compatible targets. It must distinguish a missing `.ai/` from an invalid `.ai/`, give useful errors for malformed YAML or files, and neither create files nor run commands. The current listing of generated files must keep its behaviour.

Inventory and import validation checks structure, required fields, referenced files and that ai-sync can compute the outputs without running commands. Active checks of MCP servers, plugins or setup commands are a later, explicit action. The concrete validity criteria for each resource type must still be specified.

A folder in `.ai/skills/` without `SKILL.md` is shown as an invalid skill without hiding the rest of the catalog. It cannot be copied selectively until it is fixed. This rule is stricter than the current reader, which accepts skill folders without that file.

A full copy or export can preserve invalid files so they can be repaired in the target. The preview points out the errors and the target's sync stays blocked until they are fixed.

The inventory includes `setup.yaml` entries and separately shows the additional files that have no resource identity. Implementing the inventory and the new previews cannot simply reuse the current `--dry-run` flow: that flow runs the `setup.yaml` `check` commands.

**Initial values to move forward later:** copy is a one-off operation; the target keeps its unselected resources; conflicts require an explicit choice; copying and running `ai-sync` are separate actions; the shareable package can be opened without Orca. These values can change while designing each flow without redoing the inventory.

**Technical aspects that must be resolved before applying copies or imports:** pin down the identity of `plugins.yaml` entries, how content is compared and a concurrent change is detected, and how paths outside the target or dangerous symbolic links in received packages are prevented. Inspecting a package must never run its `setup.yaml`, hooks or MCP commands.

## Open technical details

- What exact syntax will the extended plugin declaration in `plugins.yaml` have, how are its entries identified across marketplaces and targets, and how are each package's resources inspected?
- Which parts of Codex and Kiro plugin installation can be automated and which require an action by the person in the agent?
- How does the CLI register and check acceptance of imported `setup.yaml` commands before their first run?
- What are the concrete validity criteria for each resource type?

## Agreed decisions

- The catalog starts with the current Orca project expanded and lists the other registered projects. Each project's resources come from an available local worktree; other global agent installations are not part of the initial catalog.
- Copy is one-off; a later comparison between projects remains a future extension.
- The inventory includes `setup.yaml` entries and shows the additional `.ai/` files without presenting them as separately selectable resources.
- You can share the whole `.ai/`, an individual resource or several selected resources in a single package.
- The initial interface opens source files in the editor; it does not need to edit them internally.
- "Commands" are out of scope for now because a canonical resource with that name does not exist yet.
- Orca makes it easy to open the active project's catalog; the engine and the shareable format work with any accessible directory.
- Import shows absolute paths and literal `env` values for express acceptance, with no automatic rewriting.
- An identity conflict in a selective copy requires choosing between keeping and replacing after seeing the difference; other resources in the YAML are preserved. A change to the target after the preview requires reviewing it again.
- Real collisions between resources are reported and require an explicit choice; ai-sync does not rename automatically with suffixes.
- Plugin-provided resources appear under their type, but are only managed or copied through the responsible plugin.
- Inventory and import validate without running commands; active checks are later, explicit actions.
- ai-sync must manage plugins compatible with Codex and Kiro through their native mechanisms. The preview states, per target, which components are compatible, which are not and what action is needed to install them.
- The decision to keep the plugin as a unit and use native packages per agent is recorded in [ADR 0001](adr/0001-native-plugin-portability.md).
- `.ai/` declares each plugin's origin and the targets it is intended for. ai-sync inspects the package and checks that intention against its real capabilities; an incompatibility is shown before applying, without asserting compatibility from the declaration alone.
- The extension of `plugins.yaml` keeps the current boolean declarations and accepts a richer form to express origin and intended targets.
- A selective copy of a plugin carries its declaration and the required marketplace. The values in the shared `env` block are chosen separately.
- If the package cannot be inspected, the declaration can still be copied, but compatibility stays "unverified" and installation pending.
- If only part of the package is compatible with a target, the preview lists the omitted components and partial installation requires express acceptance.
- Selective copy identifies MCP servers and setup entries by name; hooks are selected as a whole event. The other entries in the target YAML are preserved.
- The export pins the inspected version or commit when possible and warns if it cannot. A plugin whose origin is a local folder is included in the exported package.
- A partial installation uses a target-specific package, with kept and omitted components visible; if that package cannot be validated, the installation stays pending.
- Copying or importing `.ai/` does not install plugins. The interface offers an explicit install action after a per-target preview.
- Pinned versions are only updated after requesting it and reviewing a new preview.
- An export with literal `env` values or possibly sensitive files stops until the person chooses to include or exclude them.
- A skill folder without `SKILL.md` is shown as invalid without preventing the rest of the inventory from being seen, and cannot be copied selectively.
- In the interface, "Generate files" does not install plugins; "Install plugins" is a separate action. The `ai-sync` command keeps, for now, its current automatic installation of Claude plugins.
- A full copy or export can keep invalid files for repair, but the target cannot be synced until they are fixed.
- Imported `setup.yaml` commands require explicit acceptance in the target before their first run, including when the CLI is used.
- Editing `check` or `install` invalidates the previous acceptance of that setup entry.
- A logical plugin can have a different variant for each agent; the catalog keeps a single entity with per-target provenance and compatibility.
- If variables or paths a variant needs are missing, installation stays pending and the preview states the specific requirement.

## Repository state when this plan was written

ai-sync already offers `init`, `list`, `--dry-run`, `clean` and generation for Claude Code, Codex and Kiro. `list` returns output paths, not a semantic inventory. `--dry-run` previews the sync of a single project, not the comparison between two `.ai/` folders. Loading and rendering are concentrated in `internal/app/app.go`; extracting reusable contracts will be part of the implementation work.

Today ai-sync reads `.ai/plugins.yaml`, generates the Claude Code configuration and can install the declared plugins. It only checks whether a plugin's key appears in `~/.claude/plugins/installed_plugins.json`; it does not inventory the files or resources that plugin provides, nor does it record a relationship between those resources and their plugin. The canonical skills in `.ai/skills/` follow a different flow.

Installing a plugin does not unpack its skills into `.ai/skills/`: Claude Code loads the plugin's content on its own. Plugin skills have names namespaced by the plugin, for example `/plugin:skill`, and can coexist with a project skill `/skill` without renaming it. Materializing plugin resources for Codex or Kiro would be a new capability and would require a different identity and collision policy.

Codex and Kiro already have their own mechanisms for installing packages with skills and MCP. ai-sync does not use them yet. Portability between Claude Code packages and Agent Plugins packages is not guaranteed: some components or settings need adaptation per target. The agreed plan is to use the native mechanisms when the package or a validated variant is compatible, with omitted components visible before applying.
