package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	sourcesConfigFile = "sources.yaml"
	sourcesLockFile   = "sources.lock.yaml"
)

var (
	commitHashPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	skillNamePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	siblingSkillReference = regexp.MustCompile(`\.\./([A-Za-z0-9][A-Za-z0-9._-]*)/`)
)

type skillSource struct {
	Git    string   `yaml:"git"`
	Ref    string   `yaml:"ref"`
	Subdir string   `yaml:"subdir,omitempty"`
	Skills []string `yaml:"skills"`
}

type skillSourcesConfig struct {
	Sources map[string]skillSource `yaml:"sources"`
}

// Hashes record what ai-sync vendored, so a later sync can tell an upstream
// change from a local edit before it overwrites anything.
type skillSourcesLock struct {
	Sources map[string]map[string]string `yaml:"sources"`
}

type vendoredFile struct {
	Content    []byte
	Executable bool
}

type vendoredSkill map[string]vendoredFile

type sourceSyncStep struct {
	source string
	name   string
	hash   string
	files  vendoredSkill
	write  bool
}

type sourceUpdateState struct {
	Name    string   `json:"name"`
	Git     string   `json:"git"`
	Ref     string   `json:"ref"`
	Head    string   `json:"head,omitempty"`
	State   string   `json:"state"`
	Changed []string `json:"changed_skills,omitempty"`
	Error   string   `json:"error,omitempty"`
}

type sourcesArgs struct {
	positional []string
	force      bool
	git        string
	ref        string
	subdir     string
}

func sourcesUsage() string {
	return "Usage: ai-sync sources sync <project-dir> [--force]\n" +
		"       ai-sync sources add <project-dir> <source> <skill>... [--git URL] [--ref COMMIT] [--subdir DIR] [--force]\n" +
		"       ai-sync sources available <project-dir> <source>\n" +
		"       ai-sync sources updates <project-dir>\n" +
		"       ai-sync sources update <project-dir> <source> [--ref COMMIT] [--force]\n"
}

func manageSources(workdir string, args []string) (string, error) {
	if len(args) < 2 || args[1] == "--help" || args[1] == "-h" {
		return sourcesUsage(), nil
	}
	parsed, err := parseSourcesArgs(args[1:])
	if err != nil {
		return "", err
	}
	if len(parsed.positional) == 0 {
		return "", errors.New("sources needs a project directory")
	}
	project := parsed.positional[0]
	if !filepath.IsAbs(project) {
		project = filepath.Join(workdir, project)
	}
	project, err = filepath.Abs(project)
	if err != nil {
		return "", err
	}
	rest := parsed.positional[1:]

	switch args[0] {
	case "sync":
		if len(rest) != 0 {
			return "", errors.New("sources sync takes only a project directory")
		}
		return syncSources(project, parsed.force)
	case "add":
		if len(rest) < 2 {
			return "", errors.New("usage: ai-sync sources add <project-dir> <source> <skill>...")
		}
		return addSourceSkills(project, rest[0], rest[1:], parsed)
	case "available":
		if len(rest) != 1 {
			return "", errors.New("usage: ai-sync sources available <project-dir> <source>")
		}
		return availableSourceSkills(project, rest[0])
	case "updates":
		if len(rest) != 0 {
			return "", errors.New("usage: ai-sync sources updates <project-dir>")
		}
		return sourceUpdates(project)
	case "update":
		if len(rest) != 1 {
			return "", errors.New("usage: ai-sync sources update <project-dir> <source> [--ref COMMIT]")
		}
		return updateSource(project, rest[0], parsed)
	default:
		return "", fmt.Errorf("unknown sources operation %q", args[0])
	}
}

func parseSourcesArgs(args []string) (sourcesArgs, error) {
	var parsed sourcesArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--force" {
			parsed.force = true
			continue
		}
		if arg == "--git" || arg == "--ref" || arg == "--subdir" {
			if i+1 >= len(args) {
				return parsed, fmt.Errorf("%s needs a value", arg)
			}
			i++
			switch arg {
			case "--git":
				parsed.git = args[i]
			case "--ref":
				parsed.ref = args[i]
			case "--subdir":
				parsed.subdir = args[i]
			}
			continue
		}
		if strings.HasPrefix(arg, "--") {
			return parsed, fmt.Errorf("unknown option %q", arg)
		}
		parsed.positional = append(parsed.positional, arg)
	}
	return parsed, nil
}

func loadSkillSources(project string) (skillSourcesConfig, error) {
	config := skillSourcesConfig{Sources: map[string]skillSource{}}
	contents, err := os.ReadFile(filepath.Join(project, ".ai", sourcesConfigFile))
	if os.IsNotExist(err) {
		return config, nil
	}
	if err != nil {
		return config, fmt.Errorf("read .ai/%s: %w", sourcesConfigFile, err)
	}
	if err := yaml.Unmarshal(contents, &config); err != nil {
		return config, fmt.Errorf("parse .ai/%s: %w", sourcesConfigFile, err)
	}
	if config.Sources == nil {
		config.Sources = map[string]skillSource{}
	}
	for name, source := range config.Sources {
		if err := validateSkillSource(name, source); err != nil {
			return config, fmt.Errorf(".ai/%s source %q: %w", sourcesConfigFile, name, err)
		}
	}
	return config, nil
}

func validateSkillSource(name string, source skillSource) error {
	if !skillNamePattern.MatchString(name) {
		return errors.New("name must be a single path segment")
	}
	if source.Git == "" {
		return errors.New("git is required")
	}
	if !commitHashPattern.MatchString(source.Ref) {
		return errors.New("ref must be a 40-character commit hash, so the vendored skills are reproducible")
	}
	if source.Subdir != "" && !safeRelativePath(source.Subdir) {
		return errors.New("subdir must be a relative path inside the repository")
	}
	for _, skill := range source.Skills {
		if !skillNamePattern.MatchString(skill) {
			return fmt.Errorf("skill %q must be a single path segment", skill)
		}
	}
	return nil
}

func saveSkillSources(project string, config skillSourcesConfig) error {
	contents, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	return writeSourcesFile(project, sourcesConfigFile, contents)
}

func loadSkillSourcesLock(project string) (skillSourcesLock, error) {
	lock := skillSourcesLock{Sources: map[string]map[string]string{}}
	contents, err := os.ReadFile(filepath.Join(project, ".ai", sourcesLockFile))
	if os.IsNotExist(err) {
		return lock, nil
	}
	if err != nil {
		return lock, fmt.Errorf("read .ai/%s: %w", sourcesLockFile, err)
	}
	if err := yaml.Unmarshal(contents, &lock); err != nil {
		return lock, fmt.Errorf("parse .ai/%s: %w", sourcesLockFile, err)
	}
	if lock.Sources == nil {
		lock.Sources = map[string]map[string]string{}
	}
	return lock, nil
}

func saveSkillSourcesLock(project string, lock skillSourcesLock) error {
	contents, err := yaml.Marshal(lock)
	if err != nil {
		return err
	}
	return writeSourcesFile(project, sourcesLockFile, append([]byte("# Written by ai-sync sources. Do not edit.\n"), contents...))
}

func writeSourcesFile(project, name string, contents []byte) error {
	path := filepath.Join(project, ".ai", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, contents, 0o644)
}

func syncSources(project string, force bool) (string, error) {
	config, err := loadSkillSources(project)
	if err != nil {
		return "", err
	}
	if len(config.Sources) == 0 {
		return "no sources declared in .ai/" + sourcesConfigFile + "\n", nil
	}
	lock, err := loadSkillSourcesLock(project)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, name := range sortedStringMapKeys(config.Sources) {
		sourceLines, err := vendorSource(project, name, config.Sources[name], &lock, force)
		if err != nil {
			return "", err
		}
		lines = append(lines, sourceLines...)
	}
	pruneLock(&lock, config)
	if err := saveSkillSourcesLock(project, lock); err != nil {
		return "", err
	}
	return joinLines(lines, "no changes"), nil
}

func addSourceSkills(project, name string, skills []string, args sourcesArgs) (string, error) {
	config, err := loadSkillSources(project)
	if err != nil {
		return "", err
	}
	source, declared := config.Sources[name]
	if declared {
		if args.git != "" || args.ref != "" || args.subdir != "" {
			return "", fmt.Errorf("source %q is already declared; use `ai-sync sources update` to change its pin", name)
		}
	} else {
		if args.git == "" {
			return "", fmt.Errorf("source %q is not declared; pass --git <url> to create it", name)
		}
		ref := args.ref
		if ref == "" {
			if ref, err = remoteHead(args.git); err != nil {
				return "", err
			}
		}
		source = skillSource{Git: args.git, Ref: ref, Subdir: args.subdir}
	}
	for _, skill := range skills {
		if !containsString(source.Skills, skill) {
			source.Skills = append(source.Skills, skill)
		}
	}
	if err := validateSkillSource(name, source); err != nil {
		return "", err
	}
	lock, err := loadSkillSourcesLock(project)
	if err != nil {
		return "", err
	}
	lines, err := vendorSource(project, name, source, &lock, args.force)
	if err != nil {
		return "", err
	}
	config.Sources[name] = source
	if err := saveSkillSources(project, config); err != nil {
		return "", err
	}
	if err := saveSkillSourcesLock(project, lock); err != nil {
		return "", err
	}
	lines = append(lines, missingSiblingWarnings(project, skills)...)
	return joinLines(lines, "no changes"), nil
}

func availableSourceSkills(project, name string) (string, error) {
	config, err := loadSkillSources(project)
	if err != nil {
		return "", err
	}
	source, ok := config.Sources[name]
	if !ok {
		return "", fmt.Errorf("source %q is not declared in .ai/%s", name, sourcesConfigFile)
	}
	root, cleanup, err := fetchSourceTree(source.Git, source.Ref)
	if err != nil {
		return "", err
	}
	defer cleanup()
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(source.Subdir), "skills"))
	if err != nil {
		return "", fmt.Errorf("source %q has no skills/ directory at %s", name, shortRef(source.Ref))
	}
	skills := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillFile := filepath.Join(root, filepath.FromSlash(source.Subdir), "skills", entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillFile); err == nil {
			skills = append(skills, entry.Name())
		}
	}
	sort.Strings(skills)
	return marshalIndented(map[string]any{"source": name, "ref": source.Ref, "skills": skills})
}

func sourceUpdates(project string) (string, error) {
	config, err := loadSkillSources(project)
	if err != nil {
		return "", err
	}
	lock, err := loadSkillSourcesLock(project)
	if err != nil {
		return "", err
	}
	states := []sourceUpdateState{}
	for _, name := range sortedStringMapKeys(config.Sources) {
		states = append(states, sourceUpdateStatus(name, config.Sources[name], lock))
	}
	return marshalIndented(map[string]any{"project": project, "sources": states})
}

func sourceUpdateStatus(name string, source skillSource, lock skillSourcesLock) sourceUpdateState {
	state := sourceUpdateState{Name: name, Git: source.Git, Ref: source.Ref}
	head, err := remoteHead(source.Git)
	if err != nil {
		state.State, state.Error = "unknown", err.Error()
		return state
	}
	state.Head = head
	if head == source.Ref {
		state.State = "current"
		return state
	}
	root, cleanup, err := fetchSourceTree(source.Git, head)
	if err != nil {
		state.State, state.Error = "unknown", err.Error()
		return state
	}
	defer cleanup()
	for _, skill := range source.Skills {
		upstream, err := readVendoredSkill(filepath.Join(root, filepath.FromSlash(source.Subdir), "skills", skill))
		vendored := lock.Sources[name][skill]
		if err != nil || hashVendoredSkill(upstream) != vendored {
			state.Changed = append(state.Changed, skill)
		}
	}
	state.State = "unchanged"
	if len(state.Changed) > 0 {
		state.State = "update_available"
	}
	return state
}

func updateSource(project, name string, args sourcesArgs) (string, error) {
	config, err := loadSkillSources(project)
	if err != nil {
		return "", err
	}
	source, ok := config.Sources[name]
	if !ok {
		return "", fmt.Errorf("source %q is not declared in .ai/%s", name, sourcesConfigFile)
	}
	previous := source.Ref
	source.Ref = args.ref
	if source.Ref == "" {
		if source.Ref, err = remoteHead(source.Git); err != nil {
			return "", err
		}
	}
	if err := validateSkillSource(name, source); err != nil {
		return "", err
	}
	lock, err := loadSkillSourcesLock(project)
	if err != nil {
		return "", err
	}
	lines, err := vendorSource(project, name, source, &lock, args.force)
	if err != nil {
		return "", err
	}
	config.Sources[name] = source
	if err := saveSkillSources(project, config); err != nil {
		return "", err
	}
	if err := saveSkillSourcesLock(project, lock); err != nil {
		return "", err
	}
	lines = append([]string{fmt.Sprintf("pinned %s from %s to %s", name, shortRef(previous), shortRef(source.Ref))}, lines...)
	return joinLines(lines, "no changes"), nil
}

// vendorSource checks every skill of one source before it writes any, so a
// refused skill never leaves the others half updated.
func vendorSource(project, name string, source skillSource, lock *skillSourcesLock, force bool) ([]string, error) {
	if len(source.Skills) == 0 {
		return nil, nil
	}
	root, cleanup, err := fetchSourceTree(source.Git, source.Ref)
	if err != nil {
		return nil, fmt.Errorf("source %q: %w", name, err)
	}
	defer cleanup()

	var steps []sourceSyncStep
	var refused []string
	for _, skill := range source.Skills {
		files, err := readVendoredSkill(filepath.Join(root, filepath.FromSlash(source.Subdir), "skills", skill))
		if err != nil {
			return nil, fmt.Errorf("source %q: skill %q not found at %s: %w", name, skill, shortRef(source.Ref), err)
		}
		step := sourceSyncStep{source: name, name: skill, files: files, hash: hashVendoredSkill(files), write: true}
		destination := filepath.Join(project, ".ai", "skills", skill)
		if local, err := readVendoredSkill(destination); err == nil {
			localHash := hashVendoredSkill(local)
			recorded, managed := lock.Sources[name][skill]
			switch {
			case localHash == step.hash:
				step.write = false
			case !force && !managed:
				refused = append(refused, fmt.Sprintf("skill %q exists in .ai/skills and is not managed by sources (use --force to replace it)", skill))
			case !force && localHash != recorded:
				refused = append(refused, fmt.Sprintf("skill %q was edited locally (use --force to discard the edits)", skill))
			}
		}
		steps = append(steps, step)
	}
	if len(refused) > 0 {
		return nil, fmt.Errorf("source %q: %s", name, strings.Join(refused, "; "))
	}

	var lines []string
	if lock.Sources[name] == nil {
		lock.Sources[name] = map[string]string{}
	}
	for _, step := range steps {
		lock.Sources[name][step.name] = step.hash
		if !step.write {
			lines = append(lines, fmt.Sprintf("skill %s unchanged", step.name))
			continue
		}
		if err := writeVendoredSkill(filepath.Join(project, ".ai", "skills", step.name), step.files); err != nil {
			return nil, err
		}
		lines = append(lines, fmt.Sprintf("wrote skill %s (%s @ %s)", step.name, name, shortRef(source.Ref)))
	}
	return lines, nil
}

func pruneLock(lock *skillSourcesLock, config skillSourcesConfig) {
	for name, skills := range lock.Sources {
		source, declared := config.Sources[name]
		if !declared {
			delete(lock.Sources, name)
			continue
		}
		for skill := range skills {
			if !containsString(source.Skills, skill) {
				delete(skills, skill)
			}
		}
	}
}

func missingSiblingWarnings(project string, skills []string) []string {
	var warnings []string
	for _, name := range skills {
		contents, err := os.ReadFile(filepath.Join(project, ".ai", "skills", name, "SKILL.md"))
		if err != nil {
			continue
		}
		reported := map[string]bool{}
		for _, match := range siblingSkillReference.FindAllStringSubmatch(string(contents), -1) {
			sibling := match[1]
			if sibling == name || reported[sibling] || sibling == ".." || sibling == "." {
				continue
			}
			reported[sibling] = true
			if _, err := os.Stat(filepath.Join(project, ".ai", "skills", sibling)); err != nil {
				warnings = append(warnings, fmt.Sprintf("warning: skill %s links to ../%s/, which is not in .ai/skills", name, sibling))
			}
		}
	}
	return warnings
}

func readVendoredSkill(root string) (vendoredSkill, error) {
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err != nil {
		return nil, err
	}
	files := vendoredSkill{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = vendoredFile{Content: contents, Executable: info.Mode().Perm()&0o111 != 0}
		return nil
	})
	return files, err
}

func writeVendoredSkill(destination string, files vendoredSkill) error {
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	for rel, file := range files {
		path := filepath.Join(destination, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if file.Executable {
			mode = 0o755
		}
		if err := os.WriteFile(path, file.Content, mode); err != nil {
			return err
		}
	}
	return nil
}

func hashVendoredSkill(files vendoredSkill) string {
	names := make([]string, 0, len(files))
	for rel := range files {
		names = append(names, rel)
	}
	sort.Strings(names)
	total := sha256.New()
	for _, rel := range names {
		sum := sha256.Sum256(files[rel].Content)
		fmt.Fprintf(total, "%s\x00%t\x00%s\n", rel, files[rel].Executable, hex.EncodeToString(sum[:]))
	}
	return hex.EncodeToString(total.Sum(nil))
}

func runGit(dir string, args ...string) (string, error) {
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func remoteHead(url string) (string, error) {
	output, err := runGit("", "ls-remote", url, "HEAD")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(output)
	if len(fields) == 0 || !commitHashPattern.MatchString(fields[0]) {
		return "", fmt.Errorf("%s has no HEAD to pin", url)
	}
	return fields[0], nil
}

// A shallow fetch of one commit is cheap, but a server may refuse to serve a
// commit that no branch points at, so it falls back to the branches and tags.
func fetchSourceTree(url, ref string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "ai-sync-source-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if _, err := runGit(dir, "init", "-q"); err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := runGit(dir, "fetch", "-q", "--depth", "1", url, ref); err != nil {
		if _, err := runGit(dir, "fetch", "-q", url, "+refs/heads/*:refs/remotes/source/*", "+refs/tags/*:refs/tags/*"); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	if _, err := runGit(dir, "checkout", "-q", "--detach", ref); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("commit %s is not in %s", shortRef(ref), url)
	}
	return dir, cleanup, nil
}

func shortRef(ref string) string {
	if len(ref) > 7 {
		return ref[:7]
	}
	return ref
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func joinLines(lines []string, empty string) string {
	if len(lines) == 0 {
		return empty + "\n"
	}
	return strings.Join(lines, "\n") + "\n"
}

func marshalIndented(value any) (string, error) {
	contents, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return string(contents) + "\n", nil
}
