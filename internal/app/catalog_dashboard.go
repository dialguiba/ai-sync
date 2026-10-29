package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type catalogDashboard struct {
	current, active string
	width, height   int
	inventory       inventoryResult
	entries         []inventoryResource
	visible         []inventoryResource
	selected        int
	listOffset      int
	detailOffset    int
	flowOffset      int
	flowCursor      int
	query           string
	kindFilter      string
	searching       bool
	mode            string
	projects        []catalogProject
	worktrees       []catalogWorktree
	target          catalogWorktree
	copyItem        inventoryResource
	scope           string
	plan            catalogCopyPreview
	conflictAction  string
	compareIndex    int
	plugin          pluginLifecycleItem
	pluginPreview   pluginPreview
	pluginTarget    string
	pendingAction   string
	formStage       int
	formValue       string
	addIdentity     string
	addSourceKind   string
	addSourceValue  string
	resultText      string
	message         string
	busy            bool
	hits            []dashboardHit
}

type dashboardHit struct {
	x1, x2, y int
	action    string
}

type dashboardProjectsMsg struct {
	projects []catalogProject
	err      error
}
type dashboardWorktreesMsg struct {
	worktrees []catalogWorktree
	err       error
}
type dashboardPreviewMsg struct {
	plan catalogCopyPreview
	err  error
}
type dashboardApplyMsg struct {
	copied int
	err    error
}
type dashboardActionMsg struct {
	message string
	err     error
}

var (
	dashBlue    = lipgloss.Color("#70B7FF")
	dashTeal    = lipgloss.Color("#64D2C0")
	dashMuted   = lipgloss.Color("#8997A5")
	dashText    = lipgloss.Color("#EAF0F5")
	dashBorder  = lipgloss.Color("#455563")
	dashSurface = lipgloss.Color("#20313D")
)

func runCatalogDashboard(project string, input io.Reader, output io.Writer) error {
	model := &catalogDashboard{current: project, active: project, width: 100, height: 30, mode: "catalog"}
	if err := model.loadInventory(); err != nil {
		return err
	}
	program := tea.NewProgram(model, tea.WithInput(input), tea.WithOutput(output), tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := program.Run()
	return err
}

func (m *catalogDashboard) loadInventory() error {
	raw, err := inventoryProject(m.current, []string{m.active})
	if err != nil && raw == "" {
		return err
	}
	if err := json.Unmarshal([]byte(raw), &m.inventory); err != nil {
		return err
	}
	m.entries = catalogTUIEntries(m.inventory)
	m.filter()
	return nil
}

func (m *catalogDashboard) filter() {
	var selectedName, selectedType string
	if m.selected >= 0 && m.selected < len(m.visible) {
		selectedName, selectedType = m.visible[m.selected].Name, m.visible[m.selected].Type
	}
	filtered := catalogTUIFilter(m.entries, m.query)
	m.visible = nil
	for _, item := range filtered {
		if m.kindFilter == "" || item.Type == m.kindFilter {
			m.visible = append(m.visible, item)
		}
	}
	m.selected = 0
	for i, item := range m.visible {
		if item.Name == selectedName && item.Type == selectedType {
			m.selected = i
			break
		}
	}
	m.keepSelectedVisible()
}

func (m *catalogDashboard) keepSelectedVisible() {
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= len(m.visible) && len(m.visible) > 0 {
		m.selected = len(m.visible) - 1
	}
	capacity := max(1, m.height-7)
	if m.selected < m.listOffset {
		m.listOffset = m.selected
	}
	if m.selected >= m.listOffset+capacity {
		m.listOffset = m.selected - capacity + 1
	}
}

func (m *catalogDashboard) selectedItem() (inventoryResource, bool) {
	if m.selected < 0 || m.selected >= len(m.visible) {
		return inventoryResource{}, false
	}
	return m.visible[m.selected], true
}

func (m *catalogDashboard) Init() tea.Cmd { return nil }

func (m *catalogDashboard) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(60, msg.Width), max(14, msg.Height)
		m.keepSelectedVisible()
	case tea.KeyMsg:
		if m.mode == "add" {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			case "esc":
				m.mode = "catalog"
			case "enter":
				return m, m.activate("form-next")
			case "backspace", "ctrl+h":
				runes := []rune(m.formValue)
				if len(runes) > 0 {
					m.formValue = string(runes[:len(runes)-1])
				}
			default:
				if len(msg.Runes) > 0 {
					m.formValue += string(msg.Runes)
				}
			}
			return m, nil
		}
		if m.searching {
			switch msg.String() {
			case "enter", "esc":
				m.searching = false
			case "backspace", "ctrl+h":
				if len(m.query) > 0 {
					m.query = string([]rune(m.query)[:len([]rune(m.query))-1])
					m.filter()
				}
			default:
				if len(msg.Runes) > 0 {
					m.query += string(msg.Runes)
					m.filter()
				}
			}
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc", "b":
			switch m.mode {
			case "compare", "confirm":
				m.mode = "review"
			case "scope":
				m.mode = "targets"
			default:
				m.mode = "catalog"
			}
			m.flowOffset, m.flowCursor = 0, 0
			m.busy = false
		case "/":
			if m.mode == "catalog" {
				m.searching = true
			}
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case "enter":
			return m, m.enter()
		case "p":
			if m.mode == "catalog" {
				return m, m.activate("projects")
			}
		case "r":
			if m.mode == "catalog" {
				return m, m.activate("refresh")
			}
		case "c":
			if m.mode == "catalog" {
				return m, m.activate("copy")
			}
		case "o":
			if m.mode == "catalog" {
				return m, m.activate("open:0")
			}
		case "1", "2", "3", "4", "5", "6", "7":
			if m.mode == "catalog" {
				filters := []string{"", "skill", "agent", "rule", "mcp_server", "plugin", "additional_file"}
				index, _ := strconv.Atoi(msg.String())
				return m, m.activate("filter:" + filters[index-1])
			}
		}
	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonWheelDown {
			m.scrollAt(msg.X, 1)
			return m, nil
		}
		if msg.Button == tea.MouseButtonWheelUp {
			m.scrollAt(msg.X, -1)
			return m, nil
		}
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			for _, hit := range m.hits {
				if msg.Y == hit.y && msg.X >= hit.x1 && msg.X <= hit.x2 {
					return m, m.activate(hit.action)
				}
			}
		}
	case dashboardProjectsMsg:
		m.busy = false
		m.flowOffset, m.flowCursor = 0, 0
		if msg.err != nil {
			m.message = "Projects: " + msg.err.Error()
			m.mode = "catalog"
		} else {
			m.projects = msg.projects
			m.mode = "projects"
		}
	case dashboardWorktreesMsg:
		m.busy = false
		m.flowOffset, m.flowCursor = 0, 0
		if msg.err != nil {
			m.message = "Worktrees: " + msg.err.Error()
			m.mode = "catalog"
		} else {
			m.worktrees = msg.worktrees
			m.mode = "targets"
		}
	case dashboardPreviewMsg:
		m.busy = false
		m.flowOffset, m.flowCursor = 0, 0
		if msg.err != nil {
			m.message = "Copy preview: " + msg.err.Error()
			m.mode = "catalog"
		} else {
			m.plan = msg.plan
			m.conflictAction = ""
			m.mode = "review"
		}
	case dashboardApplyMsg:
		m.busy = false
		m.mode = "catalog"
		if msg.err != nil {
			m.message = "Copy: " + msg.err.Error()
		} else {
			m.message = fmt.Sprintf("Copied %d files to %s. Run ai-sync there to generate agent files.", msg.copied, m.target.Path)
		}
	case dashboardActionMsg:
		m.busy = false
		if msg.err != nil {
			m.message = msg.err.Error()
		} else {
			m.message = msg.message
			if m.message == "" {
				m.message = "Plugin action completed"
			}
			if strings.Contains(msg.message, "\n") || len(msg.message) > 140 {
				m.resultText = msg.message
				m.mode = "result"
			}
		}
		if m.mode != "result" {
			m.mode = "catalog"
		}
		if err := m.loadInventory(); err != nil {
			m.message = err.Error()
		}
	}
	return m, nil
}

func (m *catalogDashboard) move(delta int) {
	if m.mode == "catalog" {
		m.selected += delta
		m.keepSelectedVisible()
		m.detailOffset = 0
		return
	}
	_, rows := m.flowRows()
	for next := m.flowCursor + delta; next >= 0 && next < len(rows); next += delta {
		if rows[next].action != "" {
			m.flowCursor = next
			break
		}
	}
	capacity := max(1, m.height-7)
	if m.flowCursor < m.flowOffset {
		m.flowOffset = m.flowCursor
	}
	if m.flowCursor >= m.flowOffset+capacity {
		m.flowOffset = m.flowCursor - capacity + 1
	}
}

func (m *catalogDashboard) scrollAt(x, delta int) {
	if m.mode != "catalog" {
		_, rows := m.flowRows()
		m.flowOffset = min(max(0, len(rows)-(m.height-7)), max(0, m.flowOffset+delta))
		return
	}
	left := max(30, min(43, m.width*2/5))
	if x < left {
		m.selected += delta
		m.keepSelectedVisible()
		m.detailOffset = 0
	} else {
		m.detailOffset = min(max(0, len(m.detailRows())-(m.height-7)), max(0, m.detailOffset+delta))
	}
}

func (m *catalogDashboard) enter() tea.Cmd {
	if m.mode == "catalog" {
		m.detailOffset = 0
		return nil
	}
	_, rows := m.flowRows()
	if m.flowCursor >= 0 && m.flowCursor < len(rows) {
		return m.activate(rows[m.flowCursor].action)
	}
	return nil
}

func (m *catalogDashboard) activate(action string) tea.Cmd {
	if m.busy && action != "quit" {
		return nil
	}
	switch {
	case action == "quit":
		return tea.Quit
	case action == "search":
		if m.mode == "catalog" {
			m.searching = true
		}
	case strings.HasPrefix(action, "filter:"):
		if m.mode != "catalog" {
			return nil
		}
		m.kindFilter = strings.TrimPrefix(action, "filter:")
		m.selected, m.listOffset, m.detailOffset = 0, 0, 0
		m.filter()
	case action == "refresh":
		if m.mode != "catalog" {
			return nil
		}
		if err := m.loadInventory(); err != nil {
			m.message = err.Error()
		} else {
			m.message = "Inventory refreshed"
		}
	case action == "projects":
		if m.mode != "catalog" {
			return nil
		}
		m.busy = true
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			projects, err := catalogProjects(ctx, m.current)
			return dashboardProjectsMsg{projects, err}
		}
	case action == "back":
		m.mode = "catalog"
		m.flowOffset, m.flowCursor = 0, 0
	case action == "add-plugin":
		if m.mode != "catalog" {
			return nil
		}
		m.mode, m.formStage, m.formValue = "add", 0, ""
		m.addIdentity, m.addSourceKind, m.addSourceValue = "", "", ""
	case action == "form-next":
		value := strings.TrimSpace(m.formValue)
		if value == "" {
			m.message = "Enter a value to continue"
			return nil
		}
		switch m.formStage {
		case 0:
			if err := validatePluginKey(value); err != nil {
				m.message = err.Error()
				return nil
			}
			m.addIdentity = value
			config, err := loadPlugins(filepath.Join(m.active, ".ai", "plugins.yaml"))
			if err != nil {
				m.message = err.Error()
				return nil
			}
			_, marketplace, _ := strings.Cut(value, "@")
			if _, exists := config.Marketplaces[marketplace]; exists {
				m.mode = "plugin-confirm"
				m.pendingAction = "add"
			} else {
				m.formStage = 1
			}
		case 1:
			if value != "repo" && value != "url" && value != "path" {
				m.message = "Source must be repo, url, or path"
				return nil
			}
			m.addSourceKind = value
			m.formStage = 2
		case 2:
			m.addSourceValue = value
			m.pendingAction = "add"
			m.mode = "plugin-confirm"
		}
		m.formValue = ""
		m.flowOffset, m.flowCursor = 0, 0
	case action == "manage-plugin":
		item, ok := m.selectedItem()
		if !ok || item.Type != "plugin" {
			return nil
		}
		raw, err := pluginLifecycleStatus(m.active, true)
		if err != nil {
			m.message = err.Error()
			return nil
		}
		var document pluginLifecycleDocument
		if err := json.Unmarshal([]byte(raw), &document); err != nil {
			m.message = err.Error()
			return nil
		}
		for _, plugin := range document.Plugins {
			if plugin.Name == item.Name {
				m.plugin = plugin
				m.mode = "plugin"
				m.flowOffset, m.flowCursor = 0, 0
				break
			}
		}
	case action == "plugin-update" || action == "plugin-remove":
		m.pendingAction = strings.TrimPrefix(action, "plugin-")
		m.mode = "plugin-confirm"
		m.flowOffset, m.flowCursor = 0, 0
	case strings.HasPrefix(action, "plugin-preview:"):
		m.pluginTarget = strings.TrimPrefix(action, "plugin-preview:")
		raw, err := previewPlugins(m.active, []string{m.active, "--target", m.pluginTarget})
		if err != nil {
			m.message = err.Error()
			return nil
		}
		if err := json.Unmarshal([]byte(raw), &m.pluginPreview); err != nil {
			m.message = err.Error()
			return nil
		}
		m.mode = "plugin-preview"
		m.flowOffset, m.flowCursor = 0, 0
	case action == "plugin-apply":
		m.pendingAction = "apply"
		m.mode = "plugin-confirm"
		m.flowOffset, m.flowCursor = 0, 0
	case action == "plugin-execute":
		project, operation, identity := m.active, m.pendingAction, m.plugin.Name
		if operation == "add" {
			identity = m.addIdentity
		}
		args := []string{identity, "--target", "claude"}
		if operation == "add" && m.addSourceKind != "" {
			args = append(args, "--"+m.addSourceKind, m.addSourceValue)
		}
		fingerprint, target := m.pluginPreview.Fingerprint, m.pluginTarget
		m.busy = true
		return func() tea.Msg {
			var output string
			var err error
			if operation == "apply" {
				output, err = applyPlugins(project, []string{project, "--target", target, "--fingerprint", fingerprint, "--plugin", identity})
			} else {
				output, err = mutateClaudePlugin(project, operation, args)
			}
			return dashboardActionMsg{message: strings.TrimSpace(output), err: err}
		}
	case action == "review":
		m.mode = "review"
		m.flowOffset, m.flowCursor = 0, 0
	case strings.HasPrefix(action, "resource:"):
		index, _ := strconv.Atoi(strings.TrimPrefix(action, "resource:"))
		if index >= 0 && index < len(m.visible) {
			m.selected, m.detailOffset = index, 0
			m.keepSelectedVisible()
		}
	case strings.HasPrefix(action, "project:"):
		index, _ := strconv.Atoi(strings.TrimPrefix(action, "project:"))
		if index >= 0 && index < len(m.projects) && m.projects[index].Path != "" {
			m.active = m.projects[index].Path
			m.query, m.kindFilter, m.selected, m.listOffset, m.detailOffset = "", "", 0, 0, 0
			m.mode = "catalog"
			if err := m.loadInventory(); err != nil {
				m.message = err.Error()
			}
		}
	case strings.HasPrefix(action, "open:"):
		item, ok := m.selectedItem()
		if !ok {
			return nil
		}
		index, _ := strconv.Atoi(strings.TrimPrefix(action, "open:"))
		if index < 0 || index >= len(item.Sources) {
			return nil
		}
		path := item.Sources[index]
		if !catalogSource(m.inventory, path) {
			m.message = "Source is not in the inventory"
			return nil
		}
		project := m.active
		m.busy = true
		return func() tea.Msg {
			file := filepath.Join(project, filepath.FromSlash(path))
			info, err := os.Stat(file)
			if err != nil || !info.Mode().IsRegular() {
				return dashboardActionMsg{err: fmt.Errorf("source file unavailable: %s", path)}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, orcaCLICommand(), "file", "open", file, "--worktree", "path:"+project, "--json")
			cmd.Dir = project
			result, err := cmd.CombinedOutput()
			if err != nil {
				return dashboardActionMsg{err: fmt.Errorf("opening in Orca: %w: %s", err, strings.TrimSpace(string(result)))}
			}
			return dashboardActionMsg{message: "Opened " + path + " in Orca"}
		}
	case action == "copy":
		item, ok := m.selectedItem()
		if !ok || (item.Type != "skill" && item.Type != "agent") || item.Status == "invalid" {
			return nil
		}
		m.busy = true
		m.copyItem = item
		project := m.active
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			worktrees, err := catalogWorktrees(ctx, project)
			return dashboardWorktreesMsg{worktrees, err}
		}
	case strings.HasPrefix(action, "target:"):
		index, _ := strconv.Atoi(strings.TrimPrefix(action, "target:"))
		if index >= 0 && index < len(m.worktrees) {
			m.target = m.worktrees[index]
			m.mode = "scope"
			m.flowOffset, m.flowCursor = 0, 0
		}
	case strings.HasPrefix(action, "scope:"):
		m.scope = strings.TrimPrefix(action, "scope:")
		if m.scope != "resource" && m.scope != "all" {
			return nil
		}
		project, target, scope := m.active, m.target.Path, m.scope
		item := m.copyItem
		m.busy = true
		return func() tea.Msg {
			plan, err := catalogCopyPlan(project, target, item.Type, item.Name, scope)
			return dashboardPreviewMsg{plan, err}
		}
	case strings.HasPrefix(action, "compare:"):
		index, _ := strconv.Atoi(strings.TrimPrefix(action, "compare:"))
		if index >= 0 && index < len(m.plan.Files) && m.plan.Files[index].Status == "conflict" {
			m.compareIndex, m.mode = index, "compare"
			m.flowOffset, m.flowCursor = 0, 0
		}
	case action == "keep" || action == "replace":
		m.conflictAction = action
	case action == "confirm":
		if m.conflicts() == 0 && m.conflictAction == "" {
			m.conflictAction = "keep"
		}
		if m.conflictAction != "" {
			m.mode = "confirm"
			m.flowOffset, m.flowCursor = 0, 0
		}
	case action == "apply":
		item := m.copyItem
		project, target, scope := m.active, m.target.Path, m.scope
		fingerprint, choice := m.plan.Fingerprint, m.conflictAction
		m.busy = true
		return func() tea.Msg {
			copied, err := catalogCopyApply(project, target, item.Type, item.Name, scope, fingerprint, choice)
			return dashboardApplyMsg{copied, err}
		}
	}
	return nil
}

func (m *catalogDashboard) conflicts() int {
	count := 0
	for _, file := range m.plan.Files {
		if file.Status == "conflict" {
			count++
		}
	}
	return count
}

func (m *catalogDashboard) View() string {
	m.hits = nil
	if m.width < 60 || m.height < 14 {
		return "Terminal too small for catalog (minimum 60×14)\n"
	}
	header := m.header()
	search := m.searchBar()
	tabs := m.tabs()
	var body string
	if m.mode == "catalog" {
		body = m.catalogBody()
	} else {
		body = m.flowBody()
	}
	footer := m.footer()
	return header + "\n" + search + "\n" + tabs + "\n" + body + "\n" + footer
}

func (m *catalogDashboard) header() string {
	left := lipgloss.NewStyle().Foreground(dashTeal).Bold(true).Render("◆ AI CATALOG")
	project := lipgloss.NewStyle().Foreground(dashMuted).Render("  " + filepath.Base(m.active))
	base := left + project
	buttons := []struct{ label, action string }{{"Projects", "projects"}, {"Add plugin", "add-plugin"}, {"Refresh", "refresh"}, {"Quit", "quit"}}
	if m.width < 78 {
		buttons = []struct{ label, action string }{{"P", "projects"}, {"+", "add-plugin"}, {"R", "refresh"}, {"Q", "quit"}}
	}
	buttonsWidth := 0
	for _, button := range buttons {
		buttonsWidth += len(button.label) + 4
	}
	start := max(lipgloss.Width(base)+1, m.width-buttonsWidth)
	line := base + strings.Repeat(" ", max(1, start-lipgloss.Width(base)))
	x := start
	for _, button := range buttons {
		label := "[" + button.label + "] "
		m.hits = append(m.hits, dashboardHit{x, x + len(label) - 1, 0, button.action})
		line += lipgloss.NewStyle().Foreground(dashBlue).Render(label)
		x += len(label)
	}
	return dashboardFit(line, m.width)
}

func (m *catalogDashboard) searchBar() string {
	marker := "Search  / "
	if m.searching {
		marker = "Search  ❯ "
	}
	text := marker + m.query
	if m.searching {
		text += "▌"
	}
	m.hits = append(m.hits, dashboardHit{0, m.width - 1, 1, "search"})
	return lipgloss.NewStyle().Foreground(dashText).Background(dashSurface).Render(dashboardFit(text, m.width))
}

func (m *catalogDashboard) tabs() string {
	tabs := []struct{ label, kind string }{
		{"1 All", ""}, {"2 Skills", "skill"}, {"3 Agents", "agent"}, {"4 Rules", "rule"},
		{"5 MCP", "mcp_server"}, {"6 Plugins", "plugin"}, {"7 Files", "additional_file"},
	}
	var line string
	x := 0
	for _, tab := range tabs {
		label := " " + tab.label + " "
		if x+len(label) > m.width {
			break
		}
		m.hits = append(m.hits, dashboardHit{x, x + len(label) - 1, 2, "filter:" + tab.kind})
		style := lipgloss.NewStyle().Foreground(dashMuted)
		if tab.kind == m.kindFilter && m.mode == "catalog" {
			style = style.Foreground(dashTeal).Background(dashSurface).Bold(true)
		}
		line += style.Render(label)
		x += len(label)
	}
	return dashboardFit(line, m.width)
}

type dashboardRow struct {
	text   string
	action string
	muted  bool
}

func (m *catalogDashboard) catalogBody() string {
	leftWidth := max(30, min(43, m.width*2/5))
	rightWidth := m.width - leftWidth
	bodyHeight := m.height - 4
	leftRows := make([]dashboardRow, 0, len(m.visible))
	for i, item := range m.visible {
		prefix := "  "
		if i == m.selected {
			prefix = "❯ "
		}
		leftRows = append(leftRows, dashboardRow{prefix + item.Name + "  · " + item.Type, "resource:" + strconv.Itoa(i), i != m.selected})
	}
	if len(leftRows) == 0 {
		leftRows = append(leftRows, dashboardRow{"No matching resources", "", true})
	}
	left := m.panel("RESOURCES  "+strconv.Itoa(len(m.visible))+"/"+strconv.Itoa(len(m.entries)), leftRows, leftWidth, bodyHeight, m.listOffset, 0)
	rightRows := m.detailRows()
	right := m.panel("DETAIL", rightRows, rightWidth, bodyHeight, m.detailOffset, leftWidth)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m *catalogDashboard) detailRows() []dashboardRow {
	item, ok := m.selectedItem()
	if !ok {
		return []dashboardRow{{"Select a resource", "", true}}
	}
	rows := []dashboardRow{{item.Name, "", false}, {strings.ToUpper(item.Type) + "  " + item.Status, "", true}}
	if item.Enabled != nil {
		rows = append(rows, dashboardRow{fmt.Sprintf("Enabled: %t", *item.Enabled), "", true})
	}
	if (item.Type == "skill" || item.Type == "agent") && item.Status != "invalid" {
		rows = append(rows, dashboardRow{"[ Copy to another worktree ]", "copy", false})
	}
	if item.Type == "plugin" {
		rows = append(rows, dashboardRow{"[ Manage plugin ]", "manage-plugin", false})
	}
	if len(item.Sources) > 0 {
		rows = append(rows, dashboardRow{"[ Open first source in Orca ]", "open:0", false})
	}
	rows = append(rows, dashboardRow{"", "", false}, dashboardRow{"SOURCE FILES", "", true})
	for i, path := range item.Sources {
		rows = append(rows, dashboardRow{"↗ " + path, "open:" + strconv.Itoa(i), false})
	}
	rows = append(rows, dashboardRow{"", "", false}, dashboardRow{"GENERATED DESTINATIONS", "", true})
	if len(item.Destinations) == 0 {
		rows = append(rows, dashboardRow{"None", "", true})
	}
	for _, path := range item.Destinations {
		rows = append(rows, dashboardRow{path, "", true})
	}
	if item.Type == "plugin" {
		rows = append(rows, dashboardRow{"", "", false}, dashboardRow{"PACKAGE INSPECTION", "", true})
		if len(item.Variants) == 0 {
			rows = append(rows, dashboardRow{"Contents unknown; no local package declared", "", true})
		}
		for _, variant := range item.Variants {
			rows = append(rows, dashboardRow{variant.Target + "  ·  " + variant.Status + "  " + variant.Version, "", false})
			if variant.PackageName != "" {
				rows = append(rows, dashboardRow{"Identity: " + variant.PackageName, "", true})
			}
			if variant.Source.Path != "" {
				rows = append(rows, dashboardRow{"Path: " + variant.Source.Path, "", true})
			}
			if variant.Source.Git != "" {
				rows = append(rows, dashboardRow{"Git: " + variant.Source.Git, "", true})
			}
			if variant.Source.Install != "" {
				rows = append(rows, dashboardRow{"Install: " + variant.Source.Install, "", true})
			}
			for _, skill := range variant.Skills {
				rows = append(rows, dashboardRow{"Skill: " + skill, "", true})
			}
			for _, server := range variant.MCPServers {
				rows = append(rows, dashboardRow{"MCP: " + server, "", true})
			}
			for _, requirement := range variant.Requirements {
				rows = append(rows, dashboardRow{"Needs: " + requirement, "", true})
			}
			for _, unsupported := range variant.Unsupported {
				rows = append(rows, dashboardRow{"Unsupported: " + unsupported, "", true})
			}
		}
	}
	if len(m.inventory.Diagnostics) > 0 {
		rows = append(rows, dashboardRow{"", "", false}, dashboardRow{"DIAGNOSTICS", "", true})
		for _, diagnostic := range m.inventory.Diagnostics {
			rows = append(rows, dashboardRow{diagnostic.Path + ": " + diagnostic.Message, "", true})
		}
	}
	return rows
}

func (m *catalogDashboard) flowBody() string {
	title, rows := m.flowRows()
	return m.panel(title, rows, m.width, m.height-4, m.flowOffset, 0)
}

func (m *catalogDashboard) flowRows() (string, []dashboardRow) {
	rows := []dashboardRow{}
	title := ""
	switch m.mode {
	case "add":
		title = "ADD CLAUDE PLUGIN"
		prompt := "Plugin identity (name@marketplace)"
		if m.formStage == 1 {
			prompt = "New marketplace source type: repo, url, or path"
		} else if m.formStage == 2 {
			prompt = "Marketplace source value"
		}
		rows = append(rows, dashboardRow{prompt, "", true}, dashboardRow{"❯ " + m.formValue + "▌", "", false}, dashboardRow{"[ Continue ]", "form-next", false})
	case "plugin":
		title = "PLUGIN · " + m.plugin.Name
		rows = append(rows, dashboardRow{"Targets: " + strings.Join(m.plugin.DeclaredTargets, ", "), "", true})
		if m.plugin.ClaudeIdentity != "" {
			state := "not installed in this project"
			if m.plugin.ClaudeInstalled {
				state = "installed " + m.plugin.InstalledVersion
			}
			rows = append(rows, dashboardRow{"Claude: " + state, "", true})
			if m.plugin.UpdateState == "update_available" {
				rows = append(rows, dashboardRow{"Available: " + m.plugin.AvailableVersion, "", true})
			}
			config, _ := loadPlugins(filepath.Join(m.active, ".ai", "plugins.yaml"))
			if _, simple := config.Plugins[m.plugin.Name]; simple {
				if m.plugin.ClaudeInstalled {
					rows = append(rows, dashboardRow{"[ Update Claude plugin ]", "plugin-update", false})
				}
				rows = append(rows, dashboardRow{"[ Remove Claude plugin ]", "plugin-remove", false})
			}
		}
		for _, target := range m.plugin.DeclaredTargets {
			if target == "codex" || target == "kiro" {
				rows = append(rows, dashboardRow{"[ Preview " + target + " action ]", "plugin-preview:" + target, false})
			}
		}
	case "plugin-preview":
		title = "PLUGIN · PREVIEW " + strings.ToUpper(m.pluginTarget)
		for _, item := range m.pluginPreview.Plugins {
			if item.Name != m.plugin.Name {
				continue
			}
			rows = append(rows, dashboardRow{item.Name + "  ·  " + item.State, "", false}, dashboardRow{"Action: " + item.Action.Kind, "", true})
			for _, value := range append(append(append([]string{}, item.Missing...), item.Omitted...), item.Conflicts...) {
				rows = append(rows, dashboardRow{value, "", true})
			}
			for _, instruction := range item.Action.Instructions {
				for _, line := range dashboardWrap(instruction, m.width-4) {
					rows = append(rows, dashboardRow{line, "", true})
				}
			}
			if len(item.Action.Command) > 0 {
				for _, line := range dashboardWrap(strings.Join(item.Action.Command, " "), m.width-4) {
					rows = append(rows, dashboardRow{line, "", true})
				}
			}
			if item.Enabled && (item.Action.Kind == "command" || item.Action.Kind == "manual") {
				rows = append(rows, dashboardRow{"[ Apply reviewed action ]", "plugin-apply", false})
			}
		}
	case "plugin-confirm":
		title = "PLUGIN · CONFIRM"
		rows = append(rows, dashboardRow{"Action: " + m.pendingAction, "", true})
		if m.pendingAction == "add" {
			rows = append(rows, dashboardRow{"Plugin: " + m.addIdentity, "", true})
			if m.addSourceKind != "" {
				rows = append(rows, dashboardRow{"Source: " + m.addSourceKind + " " + m.addSourceValue, "", true})
			}
		} else {
			rows = append(rows, dashboardRow{"Plugin: " + m.plugin.Name, "", true})
		}
		rows = append(rows, dashboardRow{"[ Confirm action ]", "plugin-execute", false})
	case "result":
		title = "RESULT"
		value := m.resultText
		if json.Valid([]byte(value)) {
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, []byte(value), "", "  "); err == nil {
				value = pretty.String()
			}
		}
		for _, line := range strings.Split(value, "\n") {
			for _, wrapped := range dashboardWrap(line, m.width-4) {
				rows = append(rows, dashboardRow{wrapped, "", true})
			}
		}
	case "projects":
		title = "PROJECTS"
		for i, project := range m.projects {
			label := project.DisplayName + "  ·  " + project.WorktreeName
			if project.Path == "" {
				label += "  (no local worktree)"
			}
			rows = append(rows, dashboardRow{label, "project:" + strconv.Itoa(i), project.Path == ""})
		}
	case "targets":
		title = "COPY · DESTINATION"
		for i, worktree := range m.worktrees {
			label := worktree.DisplayName + "  ·  " + worktree.Path
			if !worktree.HasAI {
				label += "  (creates .ai/)"
			}
			rows = append(rows, dashboardRow{label, "target:" + strconv.Itoa(i), false})
		}
		if len(rows) == 0 {
			rows = append(rows, dashboardRow{"No other local Orca worktrees", "", true})
		}
	case "scope":
		title = "COPY · SCOPE"
		rows = append(rows, dashboardRow{"Destination: " + m.target.DisplayName, "", true})
		for _, line := range dashboardWrap(m.target.Path, m.width-4) {
			rows = append(rows, dashboardRow{line, "", true})
		}
		rows = append(rows, dashboardRow{"", "", false})
		rows = append(rows,
			dashboardRow{"Only this skill or agent", "scope:resource", false},
			dashboardRow{"Entire .ai/ configuration", "scope:all", false},
		)
		if !m.target.HasAI {
			rows = append(rows, dashboardRow{"The destination has no .ai/. A resource-only copy may need project.md and mcp.yaml.", "", true})
		}
	case "review":
		title = "COPY · REVIEW"
		rows = append(rows, dashboardRow{fmt.Sprintf("%d files → %s", len(m.plan.Files), m.target.DisplayName), "", true})
		for _, line := range dashboardWrap(m.target.Path, m.width-4) {
			rows = append(rows, dashboardRow{line, "", true})
		}
		for i, file := range m.plan.Files {
			action := ""
			if file.Status == "conflict" {
				action = "compare:" + strconv.Itoa(i)
			}
			rows = append(rows, dashboardRow{file.Status + "  ·  " + file.Path, action, file.Status == "same"})
		}
		if m.conflicts() > 0 {
			rows = append(rows, dashboardRow{"", "", false},
				dashboardRow{"[ Keep destination conflicts ]", "keep", m.conflictAction != "keep"},
				dashboardRow{"[ Replace destination conflicts ]", "replace", m.conflictAction != "replace"})
		}
		rows = append(rows, dashboardRow{"", "", false}, dashboardRow{"[ Continue to confirmation ]", "confirm", false})
	case "compare":
		title = "COPY · COMPARE"
		file := m.plan.Files[m.compareIndex]
		rows = append(rows, dashboardRow{file.Path, "", false}, dashboardRow{"SOURCE", "", true})
		for _, line := range strings.Split(catalogTUIText(file.SourceText), "\n") {
			for _, wrapped := range dashboardWrap(line, m.width-4) {
				rows = append(rows, dashboardRow{wrapped, "", true})
			}
		}
		rows = append(rows, dashboardRow{"DESTINATION", "", true})
		for _, line := range strings.Split(catalogTUIText(file.TargetText), "\n") {
			for _, wrapped := range dashboardWrap(line, m.width-4) {
				rows = append(rows, dashboardRow{wrapped, "", true})
			}
		}
		rows = append(rows, dashboardRow{"[ Back to review ]", "review", false})
	case "confirm":
		title = "COPY · CONFIRM"
		rows = append(rows,
			dashboardRow{fmt.Sprintf("Copy %d reviewed files to %s?", len(m.plan.Files), m.target.DisplayName), "", false},
			dashboardRow{"Conflicts: " + m.conflictAction, "", true},
		)
		for _, line := range dashboardWrap(m.target.Path, m.width-4) {
			rows = append(rows, dashboardRow{line, "", true})
		}
		rows = append(rows, dashboardRow{"[ Apply copy ]", "apply", false}, dashboardRow{"[ Cancel ]", "back", true})
	}
	rows = append(rows, dashboardRow{"", "", false}, dashboardRow{"[ Back to catalog ]", "back", true})
	return title, rows
}

func (m *catalogDashboard) panel(title string, rows []dashboardRow, width, height, offset, x int) string {
	if width < 4 {
		return ""
	}
	inner := width - 2
	lines := []string{lipgloss.NewStyle().Foreground(dashBorder).Render("╭" + strings.Repeat("─", inner) + "╮")}
	lines = append(lines, m.panelLine(" "+title, inner, true, false))
	capacity := max(0, height-3)
	for i := 0; i < capacity; i++ {
		index := offset + i
		if index >= len(rows) {
			lines = append(lines, m.panelLine("", inner, false, false))
			continue
		}
		row := rows[index]
		selected := (strings.HasPrefix(row.action, "resource:") && index == m.selected && m.mode == "catalog") ||
			(m.mode != "catalog" && index == m.flowCursor && row.action != "")
		lines = append(lines, m.panelLine(" "+row.text, inner, false, selected || (row.action != "" && !row.muted)))
		if row.action != "" {
			m.hits = append(m.hits, dashboardHit{x + 1, x + width - 2, 5 + i, row.action})
		}
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(dashBorder).Render("╰"+strings.Repeat("─", inner)+"╯"))
	return strings.Join(lines, "\n")
}

func (m *catalogDashboard) panelLine(value string, width int, title, highlight bool) string {
	content := dashboardFit(value, width)
	style := lipgloss.NewStyle().Foreground(dashText)
	if title {
		style = style.Foreground(dashTeal).Bold(true)
	} else if highlight {
		style = style.Foreground(dashBlue).Background(dashSurface)
	} else {
		style = style.Foreground(dashMuted)
	}
	border := lipgloss.NewStyle().Foreground(dashBorder).Render("│")
	return border + style.Render(content) + border
}

func dashboardFit(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) > width {
		var result string
		for _, r := range value {
			if lipgloss.Width(result+string(r)) > width-1 {
				break
			}
			result += string(r)
		}
		value = result + "…"
	}
	return value + strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
}

func dashboardWrap(value string, width int) []string {
	if value == "" {
		return []string{""}
	}
	if width < 1 {
		return []string{value}
	}
	var lines []string
	var line string
	for _, r := range value {
		part := string(r)
		if lipgloss.Width(line+part) > width && line != "" {
			lines = append(lines, line)
			line = ""
		}
		line += part
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func (m *catalogDashboard) footer() string {
	status := "↑↓ navigate  •  click select  •  scroll  •  / search  •  p projects  •  r refresh  •  q quit"
	if m.mode != "catalog" {
		status = "click an option  •  Esc back  •  q quit"
	}
	if m.message != "" {
		status = m.message
	}
	if m.busy {
		status = "Working… " + status
	}
	return lipgloss.NewStyle().Foreground(dashMuted).Render(dashboardFit(status, m.width))
}
