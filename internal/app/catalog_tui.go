package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func catalogTUICommand(workdir string, args []string) (string, error) {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return "Usage: ai-sync tui <project-dir>\n", nil
	}
	if len(args) != 1 || args[0] == "" || strings.HasPrefix(args[0], "-") {
		return "", errors.New("usage: ai-sync tui <project-dir>")
	}
	project := args[0]
	if !filepath.IsAbs(project) {
		project = filepath.Join(workdir, project)
	}
	project, err := filepath.Abs(project)
	if err != nil {
		return "", err
	}
	if !terminalFile(os.Stdin) || !terminalFile(os.Stdout) {
		return "", errors.New("tui needs an interactive terminal; use inventory for JSON output")
	}
	return "", runCatalogDashboard(project, os.Stdin, os.Stdout)
}

func runCatalogTUI(current string, input io.Reader, output io.Writer, clear bool) error {
	reader := bufio.NewReader(input)
	active, query, message := current, "", ""
	var inventory inventoryResult
	loaded := false
	for {
		if !loaded {
			raw, err := inventoryProject(current, []string{active})
			if err != nil && raw == "" {
				return err
			}
			if err := json.Unmarshal([]byte(raw), &inventory); err != nil {
				return err
			}
			loaded = true
		}
		entries := catalogTUIEntries(inventory)
		visible := catalogTUIFilter(entries, query)
		if clear {
			fmt.Fprint(output, "\x1b[H\x1b[2J")
		}
		fmt.Fprintf(output, "AI catalog  %s\n", active)
		if active != current {
			fmt.Fprintf(output, "Started in %s\n", current)
		}
		if message != "" {
			fmt.Fprintln(output, "\n"+message)
			message = ""
		}
		if inventory.Status != "ok" || len(inventory.Diagnostics) > 0 {
			fmt.Fprintf(output, "\nInventory: %s\n", inventory.Status)
			for _, diagnostic := range inventory.Diagnostics {
				fmt.Fprintf(output, "  %s %s: %s\n", diagnostic.Path, diagnostic.Unit, diagnostic.Message)
			}
		}
		fmt.Fprintf(output, "\nResources (%d/%d)", len(visible), len(entries))
		if query != "" {
			fmt.Fprintf(output, "  search: %s", query)
		}
		fmt.Fprintln(output)
		lastType := ""
		for i, item := range visible {
			if item.Type != lastType {
				fmt.Fprintf(output, "\n%s\n", item.Type)
				lastType = item.Type
			}
			fmt.Fprintf(output, "%d. %s", i+1, item.Name)
			if item.Status != "" {
				fmt.Fprintf(output, "  [%s]", item.Status)
			}
			fmt.Fprintln(output)
		}
		fmt.Fprintln(output, "\np. Projects    /. Search    a. Add Claude plugin    r. Refresh    q. Quit")
		choice, err := tuiRead(reader, output, "> ")
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch strings.ToLower(choice) {
		case "q":
			return nil
		case "r":
			loaded = false
		case "a":
			message, err = tuiAddClaude(active, reader, output)
			loaded = false
			if err != nil {
				message = "Error: " + err.Error()
			}
		case "/":
			query, err = tuiRead(reader, output, "Search (blank clears): ")
			if err != nil {
				return err
			}
		case "p":
			selected, err := catalogTUIProject(current, active, reader, output)
			if err != nil {
				message = "Could not list projects: " + err.Error()
			} else if selected != "" && selected != active {
				active, loaded, query = selected, false, ""
			}
		default:
			index, parseErr := strconv.Atoi(choice)
			if parseErr != nil || index < 1 || index > len(visible) {
				message = "Choose a listed number or action."
				continue
			}
			message, err = catalogTUIDetail(active, inventory, visible[index-1], reader, output)
			loaded = false
			if err != nil {
				message = "Error: " + err.Error()
			}
		}
	}
}

func catalogTUIEntries(inventory inventoryResult) []inventoryResource {
	entries := append([]inventoryResource{}, inventory.Resources...)
	for _, path := range inventory.AdditionalFiles {
		entries = append(entries, inventoryResource{Type: "additional_file", Name: path, Sources: []string{path}})
	}
	return entries
}

func catalogTUIFilter(entries []inventoryResource, query string) []inventoryResource {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return entries
	}
	var visible []inventoryResource
	for _, item := range entries {
		terms := []string{item.Type, item.Name}
		terms = append(terms, item.Sources...)
		for _, variant := range item.Variants {
			terms = append(terms, variant.Skills...)
			terms = append(terms, variant.MCPServers...)
		}
		for _, term := range terms {
			if strings.Contains(strings.ToLower(term), query) {
				visible = append(visible, item)
				break
			}
		}
	}
	return visible
}

func catalogTUIProject(current, active string, reader *bufio.Reader, output io.Writer) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	projects, err := catalogProjects(ctx, current)
	if err != nil {
		return "", err
	}
	fmt.Fprintln(output, "\nOrca projects")
	for i, project := range projects {
		availability := "no local worktree"
		if project.Path != "" {
			availability = project.WorktreeName
			if !project.HasAI {
				availability += ", no .ai/"
			}
		}
		marker := ""
		if project.Path == active {
			marker = " *"
		}
		fmt.Fprintf(output, "%d. %s  [%s]%s\n", i+1, project.DisplayName, availability, marker)
	}
	choice, err := tuiRead(reader, output, "Project number (blank to cancel): ")
	if err != nil || choice == "" {
		return "", err
	}
	index, err := strconv.Atoi(choice)
	if err != nil || index < 1 || index > len(projects) || projects[index-1].Path == "" {
		return "", errors.New("choose a project with a local worktree")
	}
	return projects[index-1].Path, nil
}

func catalogTUIDetail(project string, inventory inventoryResult, item inventoryResource, reader *bufio.Reader, output io.Writer) (string, error) {
	fmt.Fprintf(output, "\n%s  [%s]\n", item.Name, item.Type)
	if item.Status != "" {
		fmt.Fprintln(output, "Status: "+item.Status)
	}
	if item.Enabled != nil {
		fmt.Fprintf(output, "Enabled: %t\n", *item.Enabled)
	}
	fmt.Fprintln(output, "Sources:")
	for i, source := range item.Sources {
		fmt.Fprintf(output, "  %d. %s\n", i+1, source)
	}
	fmt.Fprintln(output, "Generated destinations:")
	if len(item.Destinations) == 0 {
		fmt.Fprintln(output, "  none")
	}
	for _, destination := range item.Destinations {
		fmt.Fprintln(output, "  "+destination)
	}
	if item.Type == "plugin" {
		catalogTUIPluginDetails(item, output)
		fmt.Fprintln(output, "m. Manage plugin")
	}
	fmt.Fprintln(output, "\no. Open source in Orca    b. Back")
	if (item.Type == "skill" || item.Type == "agent") && item.Status != "invalid" {
		fmt.Fprintln(output, "c. Copy to another worktree")
	}
	choice, err := tuiRead(reader, output, "> ")
	if err != nil {
		return "", err
	}
	switch strings.ToLower(choice) {
	case "o":
		return catalogTUIOpen(project, inventory, item, reader, output)
	case "c":
		if (item.Type == "skill" || item.Type == "agent") && item.Status != "invalid" {
			return catalogTUICopy(project, item, reader, output)
		}
	case "m":
		if item.Type == "plugin" {
			raw, err := pluginLifecycleStatus(project, true)
			if err != nil {
				return "", err
			}
			var status pluginLifecycleDocument
			if err := json.Unmarshal([]byte(raw), &status); err != nil {
				return "", err
			}
			for _, plugin := range status.Plugins {
				if plugin.Name == item.Name {
					return tuiPlugin(project, plugin, reader, output)
				}
			}
			return "", errors.New("plugin is no longer declared")
		}
	}
	return "", nil
}

func catalogTUIPluginDetails(item inventoryResource, output io.Writer) {
	if len(item.Variants) == 0 {
		fmt.Fprintln(output, "Plugin contents: unknown (no inspectable package declared)")
		return
	}
	fmt.Fprintln(output, "Package inspection (not installation status):")
	for _, variant := range item.Variants {
		fmt.Fprintf(output, "  %s: %s\n", variant.Target, variant.Status)
		for _, field := range []struct{ label, value string }{
			{"Identity", variant.PackageName}, {"Version", variant.Version}, {"Path", variant.Source.Path},
			{"Git", variant.Source.Git}, {"Ref", variant.Source.Ref}, {"Subdir", variant.Source.Subdir}, {"Install", variant.Source.Install},
		} {
			if field.value != "" {
				fmt.Fprintf(output, "    %s: %s\n", field.label, field.value)
			}
		}
		for _, group := range []struct {
			label   string
			values  []string
			sources map[string]string
		}{
			{"Skills", variant.Skills, variant.SkillSources},
			{"MCP servers", variant.MCPServers, variant.MCPServerSources},
			{"Requirements", variant.Requirements, nil},
			{"Unsupported", variant.Unsupported, nil},
		} {
			if len(group.values) == 0 {
				if group.label == "Skills" || group.label == "MCP servers" {
					fmt.Fprintf(output, "    %s: %s\n", group.label, map[bool]string{true: "none", false: "unknown"}[variant.Source.Path != "" && variant.Status != "unverified" && variant.Status != "invalid"])
				}
				continue
			}
			fmt.Fprintln(output, "    "+group.label+":")
			for _, value := range group.values {
				fmt.Fprintln(output, "      "+value+" "+group.sources[value])
			}
		}
	}
}

func catalogTUIOpen(project string, inventory inventoryResult, item inventoryResource, reader *bufio.Reader, output io.Writer) (string, error) {
	choice, err := tuiRead(reader, output, "Source number: ")
	if err != nil {
		return "", err
	}
	index, err := strconv.Atoi(choice)
	if err != nil || index < 1 || index > len(item.Sources) {
		return "", errors.New("choose a listed source")
	}
	source := item.Sources[index-1]
	if !catalogSource(inventory, source) {
		return "", errors.New("source is not in the inventory")
	}
	path := filepath.Join(project, filepath.FromSlash(source))
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("source file unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, orcaCLICommand(), "file", "open", path, "--worktree", "path:"+project, "--json")
	cmd.Dir = project
	result, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("opening source in Orca: %w: %s", err, strings.TrimSpace(string(result)))
	}
	return "Opened " + source + " in Orca.", nil
}

func catalogTUICopy(project string, item inventoryResource, reader *bufio.Reader, output io.Writer) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	worktrees, err := catalogWorktrees(ctx, project)
	if err != nil {
		return "", err
	}
	if len(worktrees) == 0 {
		return "No other local Orca worktrees available.", nil
	}
	fmt.Fprintln(output, "\nDestination worktree:")
	for i, worktree := range worktrees {
		fmt.Fprintf(output, "%d. %s  [%s]", i+1, worktree.Path, worktree.DisplayName)
		if !worktree.HasAI {
			fmt.Fprint(output, "  no .ai/")
		}
		fmt.Fprintln(output)
	}
	choice, err := tuiRead(reader, output, "Worktree number (blank to cancel): ")
	if err != nil || choice == "" {
		return "", err
	}
	index, err := strconv.Atoi(choice)
	if err != nil || index < 1 || index > len(worktrees) {
		return "", errors.New("choose a listed worktree")
	}
	target := worktrees[index-1]
	if !target.HasAI {
		fmt.Fprintln(output, "Copying creates .ai/. A resource-only copy may still need project.md and mcp.yaml before ai-sync can run there.")
	}
	scope, err := tuiRead(reader, output, "Copy [r]esource only or [a]ll .ai/ files? ")
	if err != nil {
		return "", err
	}
	switch strings.ToLower(scope) {
	case "r":
		scope = "resource"
	case "a":
		scope = "all"
	default:
		return "", nil
	}
	plan, err := catalogCopyPlan(project, target.Path, item.Type, item.Name, scope)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(output, "\nCopy preview: %d files\n", len(plan.Files))
	conflicts := 0
	for i, file := range plan.Files {
		fmt.Fprintf(output, "%d. %-8s %s\n", i+1, file.Status, file.Path)
		if file.Status == "conflict" {
			conflicts++
		}
	}
	for conflicts > 0 {
		choice, err := tuiRead(reader, output, "Compare a conflict number, or Enter to continue: ")
		if err != nil {
			return "", err
		}
		if choice == "" {
			break
		}
		index, err := strconv.Atoi(choice)
		if err != nil || index < 1 || index > len(plan.Files) || plan.Files[index-1].Status != "conflict" {
			fmt.Fprintln(output, "Choose a conflict number.")
			continue
		}
		file := plan.Files[index-1]
		fmt.Fprintf(output, "\nSource (%s):\n%s\nDestination (%s):\n%s\n", file.Path, catalogTUIText(file.SourceText), file.Path, catalogTUIText(file.TargetText))
	}
	action := "keep"
	if conflicts > 0 {
		choice, err := tuiRead(reader, output, "For all conflicts: [k]eep destination or [r]eplace? ")
		if err != nil {
			return "", err
		}
		switch strings.ToLower(choice) {
		case "k":
		case "r":
			action = "replace"
		default:
			return "", nil
		}
	}
	confirm, err := tuiRead(reader, output, "Copy the reviewed files? [y/N] ")
	if err != nil || !strings.EqualFold(confirm, "y") {
		return "", err
	}
	copied, err := catalogCopyApply(project, target.Path, item.Type, item.Name, scope, plan.Fingerprint, action)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Copied %d files to %s. Run ai-sync there to generate agent files.", copied, target.Path), nil
}

func catalogTUIText(value string) string {
	if value == "" {
		return "(empty or binary file)"
	}
	return value
}
