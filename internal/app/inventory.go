package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type inventoryResource struct {
	Type         string   `json:"type"`
	Name         string   `json:"name"`
	Sources      []string `json:"sources"`
	Destinations []string `json:"destinations"`
}

type inventoryDiagnostic struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type inventoryResult struct {
	Status          string                `json:"status"`
	Resources       []inventoryResource   `json:"resources"`
	AdditionalFiles []string              `json:"additional_files"`
	Diagnostics     []inventoryDiagnostic `json:"diagnostics"`
}

func inventoryProject(workdir string, args []string) (string, error) {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return "Usage: ai-sync inventory <project-dir>\nPrint a read-only JSON inventory of the project's .ai/ directory.\n", nil
	}
	if len(args) != 1 || strings.HasPrefix(args[0], "-") || strings.TrimSpace(args[0]) == "" {
		return "", errors.New("inventory requires exactly one explicit project directory: ai-sync inventory <project-dir>")
	}
	project := args[0]
	if !filepath.IsAbs(project) {
		project = filepath.Join(workdir, project)
	}
	result := inventoryResult{
		Status: "ok", Resources: []inventoryResource{},
		AdditionalFiles: []string{}, Diagnostics: []inventoryDiagnostic{},
	}
	root := filepath.Join(project, ".ai")
	info, err := os.Stat(root)
	if err != nil {
		result.Status = "invalid"
		if os.IsNotExist(err) {
			result.Status = "missing"
		}
		result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: ".ai", Message: inventoryCause(err)})
		return encodeInventory(result)
	}
	if !info.IsDir() {
		result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: ".ai", Message: "expected a directory"})
		result.Status = "invalid"
		return encodeInventory(result)
	}

	known := map[string]bool{}
	addInstruction := func(kind, name, source, destination string) {
		path := filepath.Join(project, filepath.FromSlash(source))
		info, err := os.Stat(path)
		if err != nil {
			if kind == "shared_instruction" || !os.IsNotExist(err) {
				result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: source, Message: inventoryCause(err)})
			}
			return
		}
		if !info.Mode().IsRegular() {
			result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: source, Message: "expected a regular file"})
			return
		}
		if _, err := os.ReadFile(path); err != nil {
			result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: source, Message: inventoryCause(err)})
			return
		}
		known[source] = true
		result.Resources = append(result.Resources, inventoryResource{
			Type: kind, Name: name, Sources: []string{source}, Destinations: []string{destination},
		})
	}
	addInstruction("shared_instruction", "project", ".ai/project.md", "CLAUDE.md")
	if len(result.Resources) > 0 {
		result.Resources[0].Destinations = []string{".kiro/steering/project-conventions.md", "AGENTS.md", "CLAUDE.md"}
	}
	for _, target := range []struct{ name, destination string }{
		{"claude", "CLAUDE.md"}, {"codex", "AGENTS.md"}, {"kiro", ".kiro/steering/project-conventions.md"},
	} {
		addInstruction("agent_instruction", target.name, ".ai/targets/"+target.name+".md", target.destination)
	}
	if _, err := os.ReadFile(filepath.Join(root, "mcp.yaml")); err != nil {
		result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: ".ai/mcp.yaml", Message: inventoryCause(err)})
	}
	canonicalYAML := map[string]bool{
		".ai/mcp.yaml": true, ".ai/plugins.yaml": true,
		".ai/hooks.yaml": true, ".ai/setup.yaml": true,
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		rel, relErr := filepath.Rel(project, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if walkErr != nil {
			result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: rel, Message: inventoryCause(walkErr)})
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !known[rel] {
			result.AdditionalFiles = append(result.AdditionalFiles, rel)
		}
		if canonicalYAML[rel] {
			contents, err := os.ReadFile(path)
			if err != nil {
				result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: rel, Message: inventoryCause(err)})
			} else {
				var data any
				if err := yaml.Unmarshal(contents, &data); err != nil {
					result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: rel, Message: err.Error()})
				}
			}
		}
		return nil
	}); err != nil {
		result.Diagnostics = append(result.Diagnostics, inventoryDiagnostic{Path: ".ai", Message: inventoryCause(err)})
	}
	sort.Strings(result.AdditionalFiles)
	sort.Slice(result.Diagnostics, func(i, j int) bool {
		if result.Diagnostics[i].Path == result.Diagnostics[j].Path {
			return result.Diagnostics[i].Message < result.Diagnostics[j].Message
		}
		return result.Diagnostics[i].Path < result.Diagnostics[j].Path
	})
	if len(result.Diagnostics) > 0 {
		result.Status = "invalid"
	}
	return encodeInventory(result)
}

func encodeInventory(result inventoryResult) (string, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	output := string(data) + "\n"
	if result.Status != "ok" {
		return output, fmt.Errorf("inventory source %s", result.Status)
	}
	return output, nil
}

func inventoryCause(err error) string {
	if os.IsNotExist(err) {
		return "not found"
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}
