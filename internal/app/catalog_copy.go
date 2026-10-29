package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type catalogWorktree struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	DisplayName string `json:"displayName"`
	ProjectID   string `json:"projectId"`
	HostID      string `json:"hostId"`
	HasAI       bool   `json:"hasAi"`
}

type catalogCopyFile struct {
	Path       string `json:"path"`
	Status     string `json:"status"`
	SourceText string `json:"sourceText,omitempty"`
	TargetText string `json:"targetText,omitempty"`
	SourceHash string `json:"sourceHash"`
	TargetHash string `json:"targetHash,omitempty"`
	SourceMode uint32 `json:"sourceMode"`
	TargetMode uint32 `json:"targetMode,omitempty"`
}

type catalogCopyPreview struct {
	Files       []catalogCopyFile `json:"files"`
	Fingerprint string            `json:"fingerprint"`
	TargetHasAI bool              `json:"targetHasAi"`
}

func catalogWorktrees(ctx context.Context, project string) ([]catalogWorktree, error) {
	cmd := exec.CommandContext(ctx, orcaCLICommand(), "worktree", "list", "--json")
	cmd.Dir = project
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing Orca worktrees: %w", err)
	}
	var response struct {
		Result struct {
			Worktrees []catalogWorktree `json:"worktrees"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, err
	}
	var worktrees []catalogWorktree
	for _, worktree := range response.Result.Worktrees {
		if worktree.HostID == "local" && filepath.IsAbs(worktree.Path) && (project == "" || filepath.Clean(worktree.Path) != project) {
			if info, err := os.Stat(worktree.Path); err == nil && info.IsDir() {
				if aiInfo, err := os.Lstat(filepath.Join(worktree.Path, ".ai")); err == nil && aiInfo.IsDir() {
					worktree.HasAI = true
				}
				worktrees = append(worktrees, worktree)
			}
		}
	}
	sort.Slice(worktrees, func(i, j int) bool { return worktrees[i].Path < worktrees[j].Path })
	return worktrees, nil
}

func catalogCopyPlan(project, target, kind, name, scope string) (catalogCopyPreview, error) {
	output, err := inventoryProject(project, []string{project})
	if err != nil {
		return catalogCopyPreview{}, err
	}
	var inventory inventoryResult
	if err := json.Unmarshal([]byte(output), &inventory); err != nil {
		return catalogCopyPreview{}, err
	}
	var resource *inventoryResource
	for i := range inventory.Resources {
		candidate := &inventory.Resources[i]
		if candidate.Type == kind && candidate.Name == name {
			resource = candidate
			break
		}
	}
	if resource == nil || (kind != "skill" && kind != "agent") || resource.Status == "invalid" || len(resource.Sources) == 0 {
		return catalogCopyPreview{}, errors.New("skill or agent unavailable for copying")
	}
	var sources []string
	switch scope {
	case "resource":
		sources = resource.Sources
	case "all":
		err := filepath.WalkDir(filepath.Join(project, ".ai"), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("unsupported source file: %s", path)
			}
			relative, err := filepath.Rel(project, path)
			if err != nil {
				return err
			}
			sources = append(sources, filepath.ToSlash(relative))
			return nil
		})
		if err != nil {
			return catalogCopyPreview{}, err
		}
	default:
		return catalogCopyPreview{}, errors.New("unknown copy scope")
	}
	aiInfo, err := os.Lstat(filepath.Join(target, ".ai"))
	if err != nil && !os.IsNotExist(err) {
		return catalogCopyPreview{}, err
	}
	if err == nil && !aiInfo.IsDir() {
		return catalogCopyPreview{}, errors.New("destination .ai is not a directory")
	}
	plan := catalogCopyPreview{Files: make([]catalogCopyFile, 0, len(sources)), TargetHasAI: err == nil}
	for _, relative := range sources {
		if scope == "resource" && (!catalogSource(inventory, relative) || !strings.HasPrefix(relative, ".ai/"+kind+"s/")) {
			return catalogCopyPreview{}, errors.New("invalid source path")
		}
		source := filepath.Join(project, filepath.FromSlash(relative))
		destination := filepath.Join(target, filepath.FromSlash(relative))
		if err := catalogNoSymlinks(project, source); err != nil {
			return catalogCopyPreview{}, err
		}
		if err := catalogNoSymlinks(target, destination); err != nil {
			return catalogCopyPreview{}, err
		}
		sourceInfo, err := os.Lstat(source)
		if err != nil || !sourceInfo.Mode().IsRegular() {
			return catalogCopyPreview{}, fmt.Errorf("source unavailable: %s", relative)
		}
		sourceBytes, err := os.ReadFile(source)
		if err != nil {
			return catalogCopyPreview{}, err
		}
		file := catalogCopyFile{Path: relative, Status: "new", SourceHash: catalogHash(sourceBytes), SourceMode: uint32(sourceInfo.Mode().Perm())}
		if len(sourceBytes) <= 65536 && utf8.Valid(sourceBytes) {
			file.SourceText = string(sourceBytes)
		}
		targetInfo, err := os.Lstat(destination)
		if err != nil && !os.IsNotExist(err) {
			return catalogCopyPreview{}, err
		}
		if err == nil {
			if !targetInfo.Mode().IsRegular() {
				return catalogCopyPreview{}, fmt.Errorf("destination is not a regular file: %s", relative)
			}
			targetBytes, err := os.ReadFile(destination)
			if err != nil {
				return catalogCopyPreview{}, err
			}
			file.TargetHash = catalogHash(targetBytes)
			file.TargetMode = uint32(targetInfo.Mode().Perm())
			if len(targetBytes) <= 65536 && utf8.Valid(targetBytes) {
				file.TargetText = string(targetBytes)
			}
			if file.SourceHash == file.TargetHash && file.SourceMode == file.TargetMode {
				file.Status = "same"
			} else {
				file.Status = "conflict"
			}
		}
		plan.Files = append(plan.Files, file)
	}
	serialized, _ := json.Marshal(struct {
		Files       []catalogCopyFile
		TargetHasAI bool
	}{plan.Files, plan.TargetHasAI})
	plan.Fingerprint = catalogHash(serialized)
	return plan, nil
}

func catalogHash(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }

func catalogNoSymlinks(root, path string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("path leaves project")
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic link in path: %s", current)
		}
	}
	return nil
}

func catalogCopyApply(project, target, kind, name, scope, fingerprint, conflictAction string) (int, error) {
	plan, err := catalogCopyPlan(project, target, kind, name, scope)
	if err != nil {
		return 0, err
	}
	if plan.Fingerprint != fingerprint {
		return 0, errors.New("files changed since preview; review the copy again")
	}
	if conflictAction != "keep" && conflictAction != "replace" {
		return 0, errors.New("choose how to handle conflicts")
	}
	var copied int
	for _, file := range plan.Files {
		if file.Status == "same" || (file.Status == "conflict" && conflictAction == "keep") {
			continue
		}
		source := filepath.Join(project, filepath.FromSlash(file.Path))
		destination := filepath.Join(target, filepath.FromSlash(file.Path))
		if err := catalogNoSymlinks(target, destination); err != nil {
			return copied, err
		}
		contents, err := os.ReadFile(source)
		if err != nil {
			return copied, err
		}
		if catalogHash(contents) != file.SourceHash {
			return copied, errors.New("source changed since preview; review the copy again")
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return copied, err
		}
		if err := os.WriteFile(destination, contents, os.FileMode(file.SourceMode)); err != nil {
			return copied, err
		}
		if err := os.Chmod(destination, os.FileMode(file.SourceMode)); err != nil {
			return copied, err
		}
		copied++
	}
	return copied, nil
}
