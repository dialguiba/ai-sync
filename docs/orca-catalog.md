# Orca catalog extension

## Terminal catalog

Run `ai-sync tui <project-dir>` (for example, `ai-sync tui .`) to browse the same `.ai/` inventory without opening a browser. The full-screen terminal interface has clickable resource and detail panels, mouse-wheel scrolling, live search (`/`), type filters (`1`–`7`), and keyboard navigation (`↑`/`↓`, Enter, Esc, `q`). It lists every resource and additional file, searches names, paths, and plugin components, and shows sources, generated destinations, diagnostics, and inspected plugin variants. Click a source to open it in Orca, or review and confirm a skill or agent copy to another local Orca worktree. Plugin actions are available from each plugin's detail and the **Add plugin** button. The Orca project list loads only when you select **Projects**.

The command requires an interactive terminal. For scripts, use `ai-sync inventory <project-dir>` instead.

`orca-plugin/` contains a native extension for Orca 1.4 or later. It adds the **Open ai-sync Catalog** command to the worktree context. The command identifies the focused worktree, starts the local catalog and opens its UI in an Orca tab.

## Local installation

1. Install a version of `ai-sync` that includes `catalog`, for example from this repository with `go install ./cmd/ai-sync`.
2. In Orca, open **Settings → Plugins** and choose to install from a local folder. Select the `orca-plugin/` folder of this repository and review the extension authorization.
3. Focus a worktree and run **Open ai-sync Catalog** from Orca's commands.

The extension needs the `orca` CLI and the `ai-sync` binary on the same machine that runs the worker. It looks for `ai-sync` on `PATH` and in the usual location `~/go/bin/ai-sync`. On Linux it uses `orca-ide`, the name of the CLI installed by Orca.

## Using the catalog directly

You can also open it without installing the extension. From the project directory:

```sh
ai-sync catalog --orca .
```

With this repository's source code, the equivalent is `go run ./cmd/ai-sync catalog --orca .`. The command prints a local URL and keeps the server running until you stop it with Ctrl+C. If you omit `--orca`, you can open the URL in any browser. The server only listens on `127.0.0.1`.

The left panel lists Orca projects, with the current project expanded when the catalog opens. Expanding another project reads the `.ai/` inventory from a local worktree for that project; its worktree name appears under the project. If a project has several worktrees, the catalog prefers the focused one, then a worktree with `.ai/`, then `main`. Projects without an available local worktree remain visible but cannot be opened. **Refresh** reloads the project list and the expanded project's inventory. The view lets you search resources, see source files, destinations and diagnostics, and open a source file in the corresponding Orca worktree.

The UI is in English by default. The language selector in the header switches it to Spanish, and the choice is stored in the browser. Adding `?lang=es` to the catalog URL forces Spanish for that view without changing the stored preference.

Selecting a plugin shows one section per declared agent target. Each section lists the package origin, identity and version when known, compatibility status, skills, MCP servers, and any missing requirements or unsupported components. The search also finds a plugin by the name of a skill or MCP server it contains; the plugin remains one item in the resource list. A local package with no such components has an explicit empty list. For a remote or missing package, and for older marketplace declarations without a local package, the catalog says its contents are unknown. These are package inspection results, not confirmation that a plugin or its components have been installed in an agent. A disabled plugin remains visible with its discovered contents but will not be installed by ai-sync.

To move a skill or agent to another project, select it in the catalog, press **Load projects** and choose a local Orca worktree. If the target has no `.ai/`, the interface warns you and asks you to choose between copying only the resource or the entire `.ai/` configuration from the source. That option is also available when the target already has `.ai/`. Press **Review copy** to see which files are new, the same or different. For different files you can compare the content and choose whether to keep the target version or replace it. **Confirm copy** creates `.ai/` when needed and copies the chosen files without deleting the other files in the target. Afterwards, run `ai-sync` in that project if you want to generate the Claude Code, Codex and Kiro configurations. The copy does not run sync, setup or plugin installation.

The command is native to Orca and the UI opens in a tab. The current plugin panel API can read the worktree context, but it has no operation to receive the `ai-sync inventory` JSON directly. When that bridge exists, the UI will be able to move to a side panel without changing the inventory contract.

Opening the catalog again in the same worktree selects its existing tab when it is served by the installed build, as long as Orca answers the tab lookup within a few seconds. When that lookup is slow, a new tab opens instead of keeping you waiting. After an update, a new build opens a new tab. The local server checks whether its Orca tab still exists and exits after the tab has been absent for about one minute. The standalone catalog without `--orca` continues to run until stopped from the terminal.

The server sends the current project's inventory with the page, so the first view paints immediately instead of waiting for a request. The Orca project list and later refreshes load in the background.
