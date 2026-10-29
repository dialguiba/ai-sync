package app

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed catalog.html
var catalogFiles embed.FS

func serveCatalog(workdir string, args []string) (string, error) {
	flags := flag.NewFlagSet("ai-sync catalog", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	openOrca := flags.Bool("orca", false, "open the catalog in an Orca tab")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return "Usage: ai-sync catalog [--orca] <project-dir>\n", nil
		}
		return "", err
	}
	if flags.NArg() != 1 || strings.TrimSpace(flags.Arg(0)) == "" {
		return "", errors.New("catalog requires one project directory: ai-sync catalog [--orca] <project-dir>")
	}
	project := flags.Arg(0)
	if !filepath.IsAbs(project) {
		project = filepath.Join(workdir, project)
	}
	project, err := filepath.Abs(project)
	if err != nil {
		return "", err
	}
	var fingerprint string
	if *openOrca {
		fingerprint, err = catalogExecutableFingerprint()
		if err != nil {
			return "", err
		}
		// Reusing an open tab is best effort. Orca takes seconds to list tabs when
		// no tab is open, which is exactly the case with nothing to reuse, so the
		// wait is capped and the catalog opens a new tab instead.
		tab, probeErr := catalogReusableTab(project, fingerprint, catalogListTabs)
		if probeErr == nil && tab.BrowserPageID != "" {
			if err := catalogSwitchTab(project, tab); err == nil {
				fmt.Printf("AI catalog for %s: %s\n", project, tab.URL)
				return "", nil
			}
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(tokenBytes)
	// The inventory is read on the first page request, not before opening the
	// tab, so a slow project never delays the view.
	var pageOnce sync.Once
	var page []byte
	var pageErr error
	renderPage := func() ([]byte, error) {
		pageOnce.Do(func() {
			bootstrap := "null"
			if inventory, _ := inventoryProject(workdir, []string{project}); inventory != "" {
				bootstrap = strings.TrimSpace(inventory)
			}
			page, pageErr = catalogPage(token, bootstrap)
		})
		return page, pageErr
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/catalog-instance", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(catalogInstance{Project: project, Fingerprint: fingerprint})
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		rendered, err := renderPage()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(rendered)
	})
	mux.HandleFunc("GET /api/inventory", func(w http.ResponseWriter, r *http.Request) {
		source, resolveErr := catalogProjectPath(r.Context(), project, r.URL.Query().Get("projectId"))
		if resolveErr != nil {
			http.Error(w, resolveErr.Error(), http.StatusBadRequest)
			return
		}
		output, err := inventoryProject(workdir, []string{source})
		if output == "" {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte(output))
	})
	mux.HandleFunc("GET /api/projects", func(w http.ResponseWriter, r *http.Request) {
		projects, err := catalogProjects(r.Context(), project)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(projects)
	})
	mux.HandleFunc("POST /api/open", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Catalog-Token") != token || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "request rejected", http.StatusForbidden)
			return
		}
		var request struct {
			Path      string `json:"path"`
			ProjectID string `json:"projectId"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		source, err := catalogProjectPath(r.Context(), project, request.ProjectID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		output, _ := inventoryProject(workdir, []string{source})
		var inventory inventoryResult
		if json.Unmarshal([]byte(output), &inventory) != nil || !catalogSource(inventory, request.Path) {
			http.Error(w, "unknown source file", http.StatusBadRequest)
			return
		}
		file := filepath.Join(source, filepath.FromSlash(request.Path))
		if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
			http.Error(w, "source file unavailable", http.StatusBadRequest)
			return
		}
		cmd := exec.CommandContext(r.Context(), orcaCLICommand(), "file", "open", file, "--worktree", "path:"+source, "--json")
		cmd.Dir = source
		if output, err := cmd.CombinedOutput(); err != nil {
			http.Error(w, strings.TrimSpace(string(output)), http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/worktrees", func(w http.ResponseWriter, r *http.Request) {
		source, err := catalogProjectPath(r.Context(), project, r.URL.Query().Get("projectId"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		worktrees, err := catalogWorktrees(r.Context(), source)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(worktrees)
	})
	copyHandler := func(apply bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Catalog-Token") != token || r.Header.Get("Content-Type") != "application/json" {
				http.Error(w, "request rejected", http.StatusForbidden)
				return
			}
			var request struct {
				ProjectID      string `json:"projectId"`
				Type           string `json:"type"`
				Name           string `json:"name"`
				Scope          string `json:"scope"`
				WorktreeID     string `json:"worktreeId"`
				Fingerprint    string `json:"fingerprint"`
				ConflictAction string `json:"conflictAction"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&request); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			source, err := catalogProjectPath(r.Context(), project, request.ProjectID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			worktrees, err := catalogWorktrees(r.Context(), source)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			var target string
			for _, worktree := range worktrees {
				if worktree.ID == request.WorktreeID {
					target = worktree.Path
					break
				}
			}
			if target == "" {
				http.Error(w, "unknown destination worktree", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			if apply {
				copied, err := catalogCopyApply(source, target, request.Type, request.Name, request.Scope, request.Fingerprint, request.ConflictAction)
				if err != nil {
					http.Error(w, err.Error(), http.StatusConflict)
					return
				}
				json.NewEncoder(w).Encode(map[string]int{"copied": copied})
			} else {
				preview, err := catalogCopyPlan(source, target, request.Type, request.Name, request.Scope)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				json.NewEncoder(w).Encode(preview)
			}
		}
	}
	mux.HandleFunc("POST /api/copy/preview", copyHandler(false))
	mux.HandleFunc("POST /api/copy/apply", copyHandler(true))
	url := "http://" + listener.Addr().String()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != listener.Addr().String() {
			http.Error(w, "unknown host", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	if *openOrca {
		cmd := exec.Command(orcaCLICommand(), "tab", "create", "--url", url, "--worktree", "current", "--json")
		cmd.Dir = project
		output, commandErr := cmd.CombinedOutput()
		if err := catalogOrcaResult("opening Orca tab", output, commandErr); err != nil {
			return "", err
		}
		watchContext, stopWatching := context.WithCancel(context.Background())
		defer stopWatching()
		go func() {
			catalogWaitForTabClose(watchContext, project, url+"/", 30*time.Second, catalogListTabs)
			if watchContext.Err() == nil {
				shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				server.Shutdown(shutdownContext)
			}
		}()
	}
	fmt.Printf("AI catalog for %s: %s\n", project, url)
	serverErr := <-serverDone
	if errors.Is(serverErr, http.ErrServerClosed) {
		return "", nil
	}
	return "", serverErr
}

// catalogPage renders the embedded UI. bootstrap holds the inventory JSON of
// the current project, so the first view paints without waiting for a request.
func catalogPage(token, bootstrap string) ([]byte, error) {
	page, err := catalogFiles.ReadFile("catalog.html")
	if err != nil {
		return nil, err
	}
	rendered := strings.ReplaceAll(string(page), "__CATALOG_BOOTSTRAP__", bootstrap)
	rendered = strings.ReplaceAll(rendered, "__CATALOG_TOKEN__", token)
	return []byte(rendered), nil
}

func orcaCLICommand() string {
	if runtime.GOOS == "linux" {
		return "orca-ide"
	}
	return "orca"
}

func catalogSource(inventory inventoryResult, path string) bool {
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") ||
		filepath.ToSlash(filepath.Clean(path)) != path || strings.HasPrefix(path, "../") {
		return false
	}
	for _, resource := range inventory.Resources {
		for _, source := range resource.Sources {
			if source == path {
				return true
			}
		}
	}
	for _, source := range inventory.AdditionalFiles {
		if source == path {
			return true
		}
	}
	return false
}
