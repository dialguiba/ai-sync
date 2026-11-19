package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Codex and Kiro install plugins per user, which would change the global
// config of every project. Their skills are copied into the project instead,
// the same way .ai/skills is.
func loadLocalPluginSkills(workdir string, config pluginConfig, ownSkills []skill) ([]skill, error) {
	taken := map[string]bool{}
	for _, own := range ownSkills {
		taken[own.Name] = true
	}

	var skills []skill
	for _, identity := range sortedStringMapKeys(config.Plugins) {
		pluginName, marketplaceName, _ := strings.Cut(identity, "@")
		marketplace := config.Marketplaces[marketplaceName]
		if !projectPluginEnabled(config, identity) || resolveMarketplaceSource(marketplace) != "path" {
			continue
		}
		pluginDir, err := localPluginDir(absoluteMarketplacePath(marketplace.Path, workdir), pluginName)
		if errors.Is(err, errNoMarketplaceManifest) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf(".ai/plugins.yaml plugin %q: %w", identity, err)
		}
		components, err := readPluginComponents(pluginDir)
		pluginSkills := components.Skills
		if err != nil {
			return nil, fmt.Errorf("plugin %q skills: %w", identity, err)
		}
		pluginAgents := components.Agents
		siblings := make([]string, 0, len(pluginSkills))
		for _, pluginSkill := range pluginSkills {
			siblings = append(siblings, pluginSkill.Name)
		}
		for _, pluginSkill := range pluginSkills {
			prefixed, err := prefixPluginSkill(pluginName, withReferencedAgents(pluginName, pluginSkill, pluginAgents), siblings)
			if err != nil {
				return nil, fmt.Errorf("plugin %q: %w", identity, err)
			}
			absoluteRoot, err := filepath.Abs(pluginDir)
			if err != nil {
				return nil, err
			}
			for rel, content := range prefixed.Files {
				if utf8.Valid(content) {
					prefixed.Files[rel] = []byte(strings.ReplaceAll(string(content), "${CLAUDE_PLUGIN_ROOT}", absoluteRoot))
				}
			}
			if taken[prefixed.Name] {
				return nil, fmt.Errorf("plugin %q skill %q collides with another skill of the same name", identity, prefixed.Name)
			}
			taken[prefixed.Name] = true
			skills = append(skills, prefixed)
		}
	}
	return skills, nil
}

// Claude shows plugin skills as <plugin>:<skill>. Agent Skills names allow only
// lowercase letters, digits and hyphens, so Codex and Kiro get <plugin>-<skill>
// and the references between sibling skills follow the new names.
func prefixPluginSkill(pluginName string, original skill, siblings []string) (skill, error) {
	name := pluginName + "-" + original.Name
	if len(name) > maxSkillNameLength {
		return skill{}, fmt.Errorf("skill name %q is longer than %d characters", name, maxSkillNameLength)
	}

	qualified := regexp.MustCompile(regexp.QuoteMeta(pluginName) + `:([a-z$])`)
	relatives := make([]*regexp.Regexp, len(siblings))
	for i, sibling := range siblings {
		relatives[i] = regexp.MustCompile(`\.\./` + regexp.QuoteMeta(sibling) + `/`)
	}

	files := make(map[string][]byte, len(original.Files))
	for rel, contents := range original.Files {
		if !utf8.Valid(contents) {
			files[rel] = contents
			continue
		}
		text := qualified.ReplaceAllString(string(contents), pluginName+"-$1")
		for i, sibling := range siblings {
			text = relatives[i].ReplaceAllString(text, "../"+pluginName+"-"+sibling+"/")
		}
		if rel == "SKILL.md" {
			text = skillNameLine.ReplaceAllString(text, "${1}name: "+name)
		}
		files[rel] = []byte(text)
	}
	return skill{Name: name, Files: files, Executable: original.Executable}, nil
}

const maxSkillNameLength = 64

var skillNameLine = regexp.MustCompile(`(?m)\A(---\n(?:[^\n]*\n)*?)name:[^\n]*`)

func localPluginDir(marketplaceRoot, pluginName string) (string, error) {
	contents, err := os.ReadFile(filepath.Join(marketplaceRoot, ".claude-plugin", "marketplace.json"))
	if err != nil {
		return "", errNoMarketplaceManifest
	}
	var manifest struct {
		Plugins []struct {
			Name   string          `json:"name"`
			Source json.RawMessage `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return "", fmt.Errorf("invalid marketplace.json: %v", err)
	}
	for _, entry := range manifest.Plugins {
		if entry.Name != pluginName {
			continue
		}
		var source string
		if json.Unmarshal(entry.Source, &source) != nil || source == "" || filepath.IsAbs(source) {
			return "", fmt.Errorf("%s needs a relative path source in marketplace.json", pluginName)
		}
		clean := filepath.Clean(source)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("%s source escapes the marketplace", pluginName)
		}
		return filepath.Join(marketplaceRoot, clean), nil
	}
	return "", fmt.Errorf("%s is not listed in marketplace.json", pluginName)
}

var errNoMarketplaceManifest = errors.New("no .claude-plugin/marketplace.json")

// Codex and Kiro have no plugin agents. A skill that spawns one of them by its
// <plugin>:<agent> name gets the agent's instructions as a reference file, so
// the skill can hand them to a generic subagent.
func withReferencedAgents(pluginName string, original skill, agents []agent) skill {
	files := make(map[string][]byte, len(original.Files))
	for rel, contents := range original.Files {
		files[rel] = contents
	}
	var spawned []string
	for _, candidate := range agents {
		agentName := strings.TrimSuffix(candidate.Name, ".md")
		mention := regexp.MustCompile(regexp.QuoteMeta(pluginName+":"+agentName) + `($|[^a-z0-9-])`)
		reference := "references/" + agentName + ".md"
		if _, exists := files[reference]; exists {
			continue
		}
		for _, contents := range original.Files {
			if mention.Match(contents) {
				files[reference] = agentBody(candidate.Content)
				spawned = append(spawned, "`"+pluginName+":"+agentName+"` uses `"+reference+"`")
				break
			}
		}
	}
	if len(spawned) > 0 {
		files["SKILL.md"] = append(append([]byte(nil), files["SKILL.md"]...), outsideClaudeNote(spawned)...)
	}
	return skill{Name: original.Name, Files: files, Executable: original.Executable}
}

// Codex gives a spawned subagent the whole parent conversation unless
// fork_turns is "none", and a subagent whose instructions open with a persona
// line can stop after it. Both were seen in tests, so the note says both.
func outsideClaudeNote(spawned []string) []byte {
	var b strings.Builder
	b.WriteString("\n## Outside Claude Code\n\n")
	b.WriteString("Plugin agents exist only in Claude Code. Wherever this skill says to spawn one of these agents, spawn a fresh subagent instead and give it the contents of the matching file as its instructions, plus the scope.\n\n")
	for _, entry := range spawned {
		b.WriteString("- " + entry + "\n")
	}
	b.WriteString("\nThe subagent must not inherit this conversation. In Codex, call `spawn_agent` with `fork_turns=\"none\"`. Kiro subagents start clean.\n\n")
	b.WriteString("Tell the subagent that any greeting or persona line in its instructions is only an opening line: it must continue in the same turn, read the scope, and return its full report.\n")
	return []byte(b.String())
}

func agentBody(content []byte) []byte {
	text := string(content)
	if strings.HasPrefix(text, "---\n") {
		if end := strings.Index(text[4:], "\n---\n"); end >= 0 {
			text = text[4+end+len("\n---\n"):]
		}
	}
	return []byte(strings.TrimLeft(text, "\n"))
}
