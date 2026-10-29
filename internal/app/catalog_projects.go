package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

type catalogProject struct {
	ID           string `json:"id"`
	DisplayName  string `json:"displayName"`
	WorktreeID   string `json:"worktreeId,omitempty"`
	WorktreeName string `json:"worktreeName,omitempty"`
	Path         string `json:"-"`
	HasAI        bool   `json:"hasAi"`
	Current      bool   `json:"current"`
}

func catalogProjects(ctx context.Context, current string) ([]catalogProject, error) {
	cmd := exec.CommandContext(ctx, orcaCLICommand(), "project", "list", "--json")
	cmd.Dir = current
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing Orca projects: %w", err)
	}
	var response struct {
		Result struct {
			Projects []catalogProject `json:"projects"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, err
	}
	worktrees, err := catalogWorktrees(ctx, "")
	if err != nil {
		return nil, err
	}
	byID := make(map[string]catalogProject, len(response.Result.Projects))
	for _, project := range response.Result.Projects {
		byID[project.ID] = project
	}
	for _, worktree := range worktrees {
		project, ok := byID[worktree.ProjectID]
		if !ok {
			continue
		}
		candidate := catalogProject{
			ID: project.ID, DisplayName: project.DisplayName,
			WorktreeID: worktree.ID, WorktreeName: worktree.DisplayName,
			Path: worktree.Path, HasAI: worktree.HasAI,
			Current: filepath.Clean(worktree.Path) == filepath.Clean(current),
		}
		// Prefer the focused worktree, then a configured checkout, then a main checkout.
		if project.WorktreeID == "" || candidate.Current ||
			(!project.Current && candidate.HasAI && !project.HasAI) ||
			(!project.Current && candidate.HasAI == project.HasAI && candidate.WorktreeName == "main" && project.WorktreeName != "main") {
			byID[worktree.ProjectID] = candidate
		}
	}
	projects := make([]catalogProject, 0, len(response.Result.Projects))
	foundCurrent := false
	for _, project := range response.Result.Projects {
		selected := byID[project.ID]
		foundCurrent = foundCurrent || selected.Current
		projects = append(projects, selected)
	}
	if !foundCurrent {
		projects = append(projects, catalogProject{
			DisplayName: filepath.Base(current), WorktreeID: "current",
			WorktreeName: filepath.Base(current), Path: current, Current: true,
		})
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].DisplayName < projects[j].DisplayName })
	return projects, nil
}

func catalogProjectPath(ctx context.Context, current, id string) (string, error) {
	if id == "" {
		return current, nil
	}
	projects, err := catalogProjects(ctx, current)
	if err != nil {
		return "", err
	}
	for _, project := range projects {
		if project.ID == id && project.Path != "" {
			info, err := os.Stat(project.Path)
			if err == nil && info.IsDir() {
				return project.Path, nil
			}
		}
	}
	return "", fmt.Errorf("project has no available local worktree")
}
