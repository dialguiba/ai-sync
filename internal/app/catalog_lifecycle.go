package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

type catalogInstance struct {
	Project     string `json:"project"`
	Fingerprint string `json:"fingerprint"`
}

type catalogTab struct {
	BrowserPageID string `json:"browserPageId"`
	URL           string `json:"url"`
}

type catalogTabLister func(context.Context, string) ([]catalogTab, error)

// catalogReuseProbeTimeout caps how long the catalog waits for Orca's tab list
// before opening a new tab. Orca takes about ten seconds to list tabs when none
// is open, which is exactly the case where there is nothing to reuse, and about
// two seconds when some are open.
const catalogReuseProbeTimeout = 3 * time.Second

func catalogExecutableFingerprint() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func catalogListTabs(ctx context.Context, project string) ([]catalogTab, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, orcaCLICommand(), "tab", "list", "--worktree", "path:"+project, "--json")
	cmd.Dir = project
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing Orca tabs: %w", err)
	}
	var response struct {
		OK     bool `json:"ok"`
		Result struct {
			Tabs []catalogTab `json:"tabs"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return nil, err
	}
	if !response.OK {
		return nil, errors.New("Orca did not return its tabs")
	}
	return response.Result.Tabs, nil
}

// catalogReusableTab looks for an open catalog tab served by this build, with a
// bounded wait so Orca's slow tab listing cannot delay opening a new tab.
func catalogReusableTab(project, fingerprint string, list catalogTabLister) (catalogTab, error) {
	probe, stopProbe := context.WithTimeout(context.Background(), catalogReuseProbeTimeout)
	defer stopProbe()
	return catalogFindReusableTab(probe, project, fingerprint, list)
}

func catalogFindReusableTab(ctx context.Context, project, fingerprint string, list catalogTabLister) (catalogTab, error) {
	tabs, err := list(ctx, project)
	if err != nil {
		return catalogTab{}, err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	for _, tab := range tabs {
		parsed, err := url.Parse(tab.URL)
		if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || parsed.Path != "/" || tab.BrowserPageID == "" {
			continue
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+parsed.Host+"/api/catalog-instance", nil)
		if err != nil {
			continue
		}
		response, err := client.Do(request)
		if err != nil {
			continue
		}
		var instance catalogInstance
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&instance)
		response.Body.Close()
		if response.StatusCode == http.StatusOK && decodeErr == nil && instance.Project == project && instance.Fingerprint == fingerprint {
			return tab, nil
		}
	}
	return catalogTab{}, nil
}

func catalogSwitchTab(project string, tab catalogTab) error {
	cmd := exec.Command(orcaCLICommand(), "tab", "switch", "--page", tab.BrowserPageID, "--worktree", "path:"+project, "--focus", "--json")
	cmd.Dir = project
	output, err := cmd.CombinedOutput()
	return catalogOrcaResult("switching to Orca tab", output, err)
}

func catalogOrcaResult(action string, output []byte, commandErr error) error {
	if commandErr != nil {
		return fmt.Errorf("%s: %w: %s", action, commandErr, strings.TrimSpace(string(output)))
	}
	var response struct {
		OK    bool `json:"ok"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return fmt.Errorf("%s: invalid Orca response: %w", action, err)
	}
	if !response.OK {
		return fmt.Errorf("%s: %s", action, response.Error.Message)
	}
	return nil
}

func catalogTabMatchesURL(tabURL, catalogURL string) bool {
	tab, tabErr := url.Parse(tabURL)
	catalog, catalogErr := url.Parse(catalogURL)
	return tabErr == nil && catalogErr == nil && tab.Scheme == catalog.Scheme && tab.Host == catalog.Host && tab.Path == "/"
}

func catalogWaitForTabClose(ctx context.Context, project, catalogURL string, interval time.Duration, list catalogTabLister) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	missing := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tabs, err := list(ctx, project)
			found := false
			if err == nil {
				for _, tab := range tabs {
					if catalogTabMatchesURL(tab.URL, catalogURL) {
						found = true
						break
					}
				}
			}
			if found {
				missing = 0
			} else {
				missing++
				if missing >= 2 {
					return
				}
			}
		}
	}
}
