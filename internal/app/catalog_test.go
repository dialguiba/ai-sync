package app

import (
	"context"
	"strings"
	"testing"
)

func TestCatalogPageEmbedsBootstrapAndToken(t *testing.T) {
	page, err := catalogPage("secret-token", `{"status":"ok"}`)
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	if strings.Contains(html, "__CATALOG_BOOTSTRAP__") || strings.Contains(html, "__CATALOG_TOKEN__") {
		t.Fatal("catalogPage left a placeholder unreplaced")
	}
	if !strings.Contains(html, `content="secret-token"`) {
		t.Fatal("catalogPage did not embed the token")
	}
	if !strings.Contains(html, `id="catalog-bootstrap">{"status":"ok"}`) {
		t.Fatal("catalogPage did not embed the inventory")
	}
}

func TestCatalogPageAcceptsAnEmptyBootstrap(t *testing.T) {
	page, err := catalogPage("token", "null")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), `id="catalog-bootstrap">null`) {
		t.Fatal("catalogPage did not render an empty inventory")
	}
}

func TestCatalogReusableTabCapsTheProbe(t *testing.T) {
	list := func(ctx context.Context, project string) ([]catalogTab, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("the reuse probe has no deadline")
		}
		return nil, context.DeadlineExceeded
	}
	tab, err := catalogReusableTab("/project", "build", list)
	if err == nil || tab.BrowserPageID != "" {
		t.Fatalf("got tab %#v, error %v", tab, err)
	}
}
