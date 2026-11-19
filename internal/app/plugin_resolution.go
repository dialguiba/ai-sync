package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const pluginLockFile = "plugins.lock.yaml"

type pluginPackageLock struct {
	Sources map[string]lockedPluginPackage `yaml:"sources"`
}
type lockedPluginPackage struct {
	Git  string `yaml:"git"`
	Ref  string `yaml:"ref"`
	Hash string `yaml:"sha256"`
}

func readPluginLock(project string) (pluginPackageLock, error) {
	lock := pluginPackageLock{Sources: map[string]lockedPluginPackage{}}
	data, err := os.ReadFile(filepath.Join(project, ".ai", pluginLockFile))
	if os.IsNotExist(err) {
		return lock, nil
	}
	if err != nil {
		return lock, err
	}
	if err = yaml.Unmarshal(data, &lock); err != nil {
		return lock, fmt.Errorf("parse .ai/%s: %w", pluginLockFile, err)
	}
	if lock.Sources == nil {
		lock.Sources = map[string]lockedPluginPackage{}
	}
	for name, pin := range lock.Sources {
		if pin.Git == "" || !gitCommit.MatchString(pin.Ref) || len(pin.Hash) != 64 {
			return lock, fmt.Errorf("invalid plugin lock entry %q", name)
		}
	}
	return lock, nil
}
func pluginPackagePath(pin lockedPluginPackage) string {
	sum := sha256.Sum256([]byte(pin.Git))
	return filepath.ToSlash(filepath.Join(".ai", "plugins", hex.EncodeToString(sum[:])[:16], pin.Ref))
}
func verifyPluginPackage(project string, pin lockedPluginPackage) error {
	root := filepath.Join(project, filepath.FromSlash(pluginPackagePath(pin)))
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	absProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		return err
	}
	if !withinPath(absProject, real) {
		return errors.New("locked plugin package escapes project")
	}
	files, err := readPackageFiles(root)
	if err != nil {
		return err
	}
	if hashVendoredSkill(files) != pin.Hash {
		return fmt.Errorf("locked plugin package %s was modified; restore it before syncing", pin.Ref)
	}
	return nil
}
func marketplaceGit(source marketplaceSource) (string, error) {
	if resolveMarketplaceSource(source) != "github" {
		return "", nil
	}
	parts := strings.Split(source.Repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(source.Repo, " \\:@?#\n\r\t") || parts[0] == ".." || parts[1] == ".." {
		return "", fmt.Errorf("invalid GitHub marketplace repo %q; expected owner/repo", source.Repo)
	}
	return "https://github.com/" + source.Repo + ".git", nil
}

// loadResolvedPlugins reads only local files. Declarations remain untouched and
// the lock provides their exact local package origins.
func loadResolvedPlugins(project string) (pluginConfig, error) {
	project, err := filepath.Abs(project)
	if err != nil {
		return pluginConfig{}, err
	}
	config, err := loadPlugins(filepath.Join(project, ".ai/plugins.yaml"))
	if err != nil {
		return config, err
	}
	lock, err := readPluginLock(project)
	if err != nil {
		return config, err
	}
	for name, marketplace := range config.Marketplaces {
		active := false
		for identity, enabled := range config.Plugins {
			_, market, _ := strings.Cut(identity, "@")
			if enabled && market == name {
				active = true
				break
			}
		}
		if !active {
			continue
		}
		git, err := marketplaceGit(marketplace)
		if err != nil {
			return config, err
		}
		pin, ok := lock.Sources["marketplace:"+name]
		if !ok || git == "" || pin.Git != git {
			continue
		}
		if err = verifyPluginPackage(project, pin); err != nil {
			return config, fmt.Errorf("marketplace %q: %w", name, err)
		}
		config.Marketplaces[name] = marketplaceSource{Path: filepath.Join(project, filepath.FromSlash(pluginPackagePath(pin)), "source")}
	}
	for name, declaration := range config.Portable {
		if !declaration.Enabled {
			continue
		}
		for target, source := range declaration.Targets {
			resolved, err := resolveLockedPluginTarget(project, source)
			if err != nil {
				return config, err
			}
			declaration.Targets[target] = resolved
		}
		config.Portable[name] = declaration
	}
	return config, nil
}

func syncPluginPackages(workdir string, args []string) (string, error) {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return "Usage: ai-sync plugins sync <project-dir> [--update] [--dry-run]\nResolve pinned project packages without installing or running them.\n", nil
	}
	if len(args) == 0 {
		return "", errors.New("usage: ai-sync plugins sync <project-dir> [--update] [--dry-run]")
	}
	project := args[0]
	if !filepath.IsAbs(project) {
		project = filepath.Join(workdir, project)
	}
	project, err := filepath.Abs(project)
	if err != nil {
		return "", err
	}
	update, dry := false, false
	for _, arg := range args[1:] {
		switch arg {
		case "--update":
			update = true
		case "--dry-run":
			dry = true
		default:
			return "", fmt.Errorf("unknown plugins sync option %q", arg)
		}
	}
	config, err := loadPlugins(filepath.Join(project, ".ai/plugins.yaml"))
	if err != nil {
		return "", err
	}
	lock, err := readPluginLock(project)
	if err != nil {
		return "", err
	}
	type request struct{ key, git, ref string }
	requests := map[string]request{}
	for identity, enabled := range config.Plugins {
		if !enabled {
			continue
		}
		_, name, _ := strings.Cut(identity, "@")
		git, err := marketplaceGit(config.Marketplaces[name])
		if err != nil {
			return "", err
		}
		if git != "" {
			requests["marketplace:"+name] = request{key: "marketplace:" + name, git: git}
		}
	}
	for _, declaration := range config.Portable {
		if !declaration.Enabled {
			continue
		}
		for _, source := range declaration.Targets {
			if source.Git != "" {
				key := "git:" + source.Git + "#" + source.Ref
				requests[key] = request{key: key, git: source.Git, ref: source.Ref}
			}
		}
	}
	var lines []string
	for _, key := range sortedStringMapKeys(requests) {
		request := requests[key]
		pin, ok := lock.Sources[key]
		if !ok {
			for _, alias := range sortedStringMapKeys(lock.Sources) {
				candidate := lock.Sources[alias]
				if candidate.Git == request.git && (request.ref == "" || candidate.Ref == request.ref) {
					pin, ok = candidate, true
					break
				}
			}
		}
		if ok && pin.Git == request.git && (request.ref == "" || pin.Ref == request.ref) && (!update || request.ref != "") {
			if err = verifyPluginPackage(project, pin); err == nil {
				pin, err = prepareMissingMarketplaceMembers(project, pin, config, dry)
				if err != nil {
					return "", err
				}
				if !dry {
					if err = persistPluginPin(project, &lock, key, pin); err != nil {
						return "", err
					}
				}
				if dry {
					lines = append(lines, "would use pinned package "+key+" @ "+pin.Ref)
				} else {
					lines = append(lines, "ok "+key+" @ "+pin.Ref)
				}
				continue
			} else if !os.IsNotExist(err) {
				return strings.Join(lines, "\n"), err
			}
		}
		if dry {
			lines = append(lines, "would resolve "+key)
			continue
		}
		ref := request.ref
		if ref == "" {
			if ok && pin.Git == request.git && !update {
				ref = pin.Ref
			} else {
				ref, err = remoteHead(request.git)
				if err != nil {
					return "", err
				}
			}
		}
		pin, err = materializePluginPackage(project, request.git, ref, config)
		if err != nil {
			return strings.Join(lines, "\n"), err
		}
		if err = persistPluginPin(project, &lock, key, pin); err != nil {
			return "", err
		}
		lines = append(lines, "resolved "+key+" @ "+ref)
	}

	if len(lines) == 0 {
		return "no remote plugins to resolve\n", nil
	}
	if update && !dry {
		lines = append(lines, "Run ai-sync to regenerate project resources; run ai-sync plugins to reinstall updated Claude packages.")
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func materializePluginPackage(project, git, ref string, config pluginConfig) (lockedPluginPackage, error) {
	pin := lockedPluginPackage{Git: git, Ref: ref}
	source, cleanup, err := fetchSourceTree(git, ref)
	if err != nil {
		return pin, err
	}
	defer cleanup()
	files, err := readPackageTree(source, true)
	if err != nil {
		return pin, err
	}
	destination := filepath.Join(project, filepath.FromSlash(pluginPackagePath(pin)))
	parent := filepath.Dir(destination)
	if err = ensurePluginDirectory(project, parent); err != nil {
		return pin, err
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return pin, err
	}
	realProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		return pin, err
	}
	if !withinPath(realProject, realParent) {
		return pin, errors.New("plugin cache escapes project")
	}
	stage, err := os.MkdirTemp(parent, ".resolve-")
	if err != nil {
		return pin, err
	}
	defer os.RemoveAll(stage)
	if err = writeVendoredSkill(filepath.Join(stage, "source"), files); err != nil {
		return pin, err
	}
	if err := prepareMarketplaceMembers(stage, pin, config); err != nil {
		return pin, err
	}
	if err := validatePreparedPluginPackages(project, stage); err != nil {
		return pin, err
	}
	all, err := readPackageFiles(stage)
	if err != nil {
		return pin, err
	}
	pin.Hash = hashVendoredSkill(all)
	if _, err = os.Stat(destination); err == nil {
		existing, err := readPackageFiles(destination)
		if err != nil {
			return pin, err
		}
		if hashVendoredSkill(existing) != pin.Hash {
			return pin, errors.New("existing plugin revision differs from resolved package; restore or remove it explicitly")
		}
	} else if !os.IsNotExist(err) {
		return pin, err
	} else if err = os.Rename(stage, destination); err != nil {
		return pin, err
	}
	return pin, nil
}
func mustRelative(root, path string) string { rel, _ := filepath.Rel(root, path); return rel }

func resolveLockedPluginTarget(project string, source pluginTarget) (pluginTarget, error) {
	if source.Git == "" {
		return source, nil
	}
	lock, err := readPluginLock(project)
	if err != nil {
		return source, err
	}
	pin, ok := lock.Sources["git:"+source.Git+"#"+source.Ref]
	if !ok || pin.Git != source.Git || pin.Ref != source.Ref {
		return source, nil
	}
	if err := verifyPluginPackage(project, pin); err != nil {
		return source, err
	}
	source.Path = filepath.ToSlash(filepath.Join(pluginPackagePath(pin), "source", source.Subdir))
	return source, nil
}

func prepareMarketplaceMembers(stage string, pin lockedPluginPackage, config pluginConfig) error {
	// Prepare every enabled marketplace member of this checkout before publishing.
	for _, identity := range sortedStringMapKeys(config.Plugins) {
		if !config.Plugins[identity] {
			continue
		}
		name, market, _ := strings.Cut(identity, "@")
		url, err := marketplaceGit(config.Marketplaces[market])
		if err != nil {
			return err
		}
		if url != pin.Git {
			continue
		}
		if !pluginName.MatchString(name) || len(name) > 64 {
			return fmt.Errorf("invalid plugin package name %q", name)
		}
		root, err := localPluginDir(filepath.Join(stage, "source"), name)
		if err != nil {
			return err
		}
		if _, err = containedPluginPath(filepath.Join(stage, "source"), mustRelative(filepath.Join(stage, "source"), root)); err != nil {
			return err
		}
		components, err := readPluginComponents(root)
		if err != nil {
			return fmt.Errorf("plugin %s: %w", identity, err)
		}
		if err = preparePluginPackage(filepath.Join(stage, "prepared", name), root, name, components); err != nil {
			return err
		}
	}
	return nil
}

func prepareMissingMarketplaceMembers(project string, pin lockedPluginPackage, config pluginConfig, dry bool) (lockedPluginPackage, error) {
	root := filepath.Join(project, pluginPackagePath(pin))
	missing := false
	for identity, enabled := range config.Plugins {
		if !enabled {
			continue
		}
		name, market, _ := strings.Cut(identity, "@")
		git, err := marketplaceGit(config.Marketplaces[market])
		if err != nil {
			return pin, err
		}
		if git != pin.Git {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "prepared", name, "plugin.json")); os.IsNotExist(err) {
			missing = true
		} else if err != nil {
			return pin, err
		}
	}
	if !missing || dry {
		return pin, nil
	}
	files, err := readPackageFiles(root)
	if err != nil {
		return pin, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(root), ".prepare-")
	if err != nil {
		return pin, err
	}
	defer os.RemoveAll(stage)
	if err = writeVendoredSkill(stage, files); err != nil {
		return pin, err
	}
	if err = prepareMarketplaceMembers(stage, pin, config); err != nil {
		return pin, err
	}
	if err = validatePreparedPluginPackages(project, stage); err != nil {
		return pin, err
	}
	files, err = readPackageFiles(stage)
	if err != nil {
		return pin, err
	}
	pin.Hash = hashVendoredSkill(files)
	backup := stage + "-previous"
	if err = os.Rename(root, backup); err != nil {
		return pin, err
	}
	if err = os.Rename(stage, root); err != nil {
		os.Rename(backup, root)
		return pin, err
	}
	if err = os.RemoveAll(backup); err != nil {
		return pin, err
	}
	return pin, nil
}

func ensurePluginDirectory(project, path string) error {
	realProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		return err
	}
	current := path
	for {
		real, err := filepath.EvalSymlinks(current)
		if err == nil {
			if !withinPath(realProject, real) {
				return errors.New("plugin storage escapes project")
			}
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return errors.New("plugin storage has no existing parent")
		}
		current = parent
	}
	return os.MkdirAll(path, 0755)
}

func writePluginLock(project string, data []byte) error {
	if previous, err := os.ReadFile(filepath.Join(project, ".ai", pluginLockFile)); err == nil && bytes.Equal(previous, data) {
		return nil
	}
	root := filepath.Join(project, ".ai")
	if err := ensurePluginDirectory(project, root); err != nil {
		return err
	}
	file, err := os.CreateTemp(root, ".plugins-lock-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0644); err == nil {
		_, err = file.Write(data)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(root, pluginLockFile))
}

// Each successful resolution is durable before another repository can fail.
// Shared aliases all refer to the same retained package bytes.
func persistPluginPin(project string, lock *pluginPackageLock, key string, pin lockedPluginPackage) error {
	for alias, other := range lock.Sources {
		if other.Git == pin.Git && other.Ref == pin.Ref {
			lock.Sources[alias] = pin
		}
	}
	lock.Sources[key] = pin
	data, err := yaml.Marshal(lock)
	if err != nil {
		return err
	}
	return writePluginLock(project, data)
}
