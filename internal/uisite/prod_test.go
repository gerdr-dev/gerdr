package uisite

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Production React is the default: no cookie (or a stray value) means production without waiting; only
// "development" opts out, and "production" (the old opt-in) is the explicit, waiting kind.
func TestReactChoice(t *testing.T) {
	for cookie, want := range map[string]string{"": "", "gerdr_react=": "", "gerdr_react=x": "", "gerdr_react=development": "development", "gerdr_react=production": "production"} {
		req := httptest.NewRequest("GET", "/", nil)
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		if got := reactChoice(req); got != want {
			t.Errorf("cookie %q: %q, want %q", cookie, got, want)
		}
	}
}

// Production React's import map: no "dev" anywhere, still one React (every other package external=react,react-dom),
// and no React Refresh (it has no production build; the runtime falls back to re-rendering the page).
func TestProdImportMap(t *testing.T) {
	deps := map[string]string{"react": "19.2.0", "react-dom": "19.2.0", "react-router": "7.9.4", "react-refresh": "0.19.0", "zustand": "5.0.8"}
	im, missing := buildImportMap(deps, []string{"zustand"}, true)
	if len(missing) > 0 {
		t.Fatalf("missing %v", missing)
	}
	for spec, u := range im {
		if strings.Contains(u, "?dev") || strings.Contains(u, "&dev") {
			t.Errorf("%s → %s: development build in the production map", spec, u)
		}
	}
	if _, ok := im["react-refresh/runtime"]; ok {
		t.Error("react-refresh in the production map")
	}
	want := map[string]string{
		"react":             "/_esm/react@19.2.0",
		"react-dom/client":  "/_esm/react-dom@19.2.0/client?external=react",
		"zustand":           "/_esm/zustand@5.0.8?external=react,react-dom",
		"react/jsx-runtime": "/_esm/react@19.2.0/jsx-runtime",
		"react/":            "/_esm/react@19.2.0/",
		"react-dom/":        "/_esm/react-dom@19.2.0&external=react/",
	}
	for spec, u := range want {
		if im[spec] != u {
			t.Errorf("%s → %q, want %q", spec, im[spec], u)
		}
	}
	// The development map is unchanged
	dev, _ := buildImportMap(deps, []string{"zustand"}, false)
	if dev["react"] != "/_esm/react@19.2.0?dev" || dev["zustand"] != "/_esm/zustand@5.0.8?dev&external=react,react-dom" || dev["react-refresh/runtime"] == "" {
		t.Errorf("development map changed: %v", dev)
	}
}
