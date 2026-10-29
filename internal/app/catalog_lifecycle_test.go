package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCatalogFindReusableTab(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/catalog-instance" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(catalogInstance{Project: "/project", Fingerprint: "new-build"})
	}))
	defer server.Close()
	tabs := []catalogTab{
		{BrowserPageID: "old", URL: "http://127.0.0.1:1/"},
		{BrowserPageID: "current", URL: server.URL + "/"},
	}
	getTabs := func(context.Context, string) ([]catalogTab, error) { return tabs, nil }
	got, err := catalogFindReusableTab(context.Background(), "/project", "new-build", getTabs)
	if err != nil || got.BrowserPageID != "current" {
		t.Fatalf("got tab %#v, error %v", got, err)
	}
	got, err = catalogFindReusableTab(context.Background(), "/project", "different-build", getTabs)
	if err != nil || got.BrowserPageID != "" {
		t.Fatalf("reused stale build: tab %#v, error %v", got, err)
	}
}

func TestCatalogWaitForTabClose(t *testing.T) {
	checks := 0
	getTabs := func(context.Context, string) ([]catalogTab, error) {
		checks++
		if checks == 1 {
			return []catalogTab{{URL: "http://127.0.0.1:1234/?lang=es"}}, nil
		}
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	catalogWaitForTabClose(ctx, "/project", "http://127.0.0.1:1234/", time.Millisecond, getTabs)
	if checks != 3 || ctx.Err() != nil {
		t.Fatalf("expected one present and two missing checks, got %d, context error %v", checks, ctx.Err())
	}
}

func TestCatalogOrcaResultRejectsFailedResponse(t *testing.T) {
	if err := catalogOrcaResult("switching", []byte(`{"ok":false,"error":{"message":"tab missing"}}`), nil); err == nil {
		t.Fatal("accepted a failed Orca response")
	}
}
