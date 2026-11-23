package uisite

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Production React is the default: no cookie (or a stray value) means production; only "development" opts out, and
// "production" (the old opt-in) also resolves to the primary production build.
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
// no React Refresh (it has no production build), and no react/jsx-dev-runtime (served via @jsx-dev-prod.js instead).
func TestProdImportMap(t *testing.T) {
	deps := map[string]string{"react": "19.2.0", "react-dom": "19.2.0", "react-router": "7.9.4", "react-refresh": "0.19.0", "zustand": "5.0.8"}
	im, missing := buildImportMap(deps, []string{"zustand", "react/jsx-dev-runtime"}, true)
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
	if _, ok := im["react/jsx-dev-runtime"]; ok {
		t.Error("react/jsx-dev-runtime should be served by the local shim, not esm.sh")
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
	// The development map includes ?dev, react-refresh and real react/jsx-dev-runtime
	dev, _ := buildImportMap(deps, []string{"zustand"}, false)
	if dev["react"] != "/_esm/react@19.2.0?dev" || dev["zustand"] != "/_esm/zustand@5.0.8?dev&external=react,react-dom" || dev["react-refresh/runtime"] == "" || dev["react/jsx-dev-runtime"] == "" {
		t.Errorf("development map changed: %v", dev)
	}
}

// Default build & vendor bundle are production: upstream esm.sh only sees production requests until a device asks for
// development via cookie or PrepareDev.
func TestDefaultVendorIsProduction(t *testing.T) {
	var mu sync.Mutex
	var requested []string
	esm := NewESM("https://esm.test", t.TempDir())
	esm.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		requested = append(requested, r.URL.RequestURI())
		mu.Unlock()
		var body string
		switch {
		case strings.Contains(r.URL.Path, "jsx-runtime"):
			body = `export const jsx = () => {}; export const jsxs = () => {}; export const Fragment = Symbol();`
		case strings.Contains(r.URL.Path, "jsx-dev-runtime"):
			body = `export const jsxDEV = () => {}; export const Fragment = Symbol();`
		case strings.Contains(r.URL.Path, "react-dom"):
			body = `export const createRoot = () => ({ render() {} });`
		case strings.Contains(r.URL.Path, "react-router"):
			body = `export const createBrowserRouter = () => {}; export const createMemoryRouter = () => {}; export const RouterProvider = () => {}; export const Outlet = () => {}; export const Navigate = () => {}; export const useLocation = () => {}; export const useParams = () => {}; export const useRouteError = () => {};`
		case strings.Contains(r.URL.Path, "react-refresh"):
			body = `export default { injectIntoGlobalHook() {}, register() {}, performReactRefresh() {} };`
		default:
			body = `export default { createElement() {}, useEffect() {} }; export const createElement = () => {};`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/javascript"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}

	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"package.json":        `{"dependencies":{"react":"19.3.1","react-dom":"19.3.1","react-router":"7.18.5"}}`,
		"src/pages/index.tsx": `export default function P() { return <div /> }`,
	})
	r := New(dir, t.TempDir(), nil)
	r.UseESM(esm)

	// Wait for the primary vendor bundle to finish building.
	deadline := time.Now().Add(5 * time.Second)
	var b *build
	for {
		b = r.ensure()
		if !b.waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("primary vendor bundle did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if b.fatal != "" {
		t.Fatalf("fatal: %s", b.fatal)
	}
	if !strings.HasPrefix(b.importMap["react"], "/_esm/~vendor/") {
		t.Fatalf("react not bundled: %q", b.importMap["react"])
	}
	if !strings.Contains(b.importMap["react/jsx-dev-runtime"], jsxShim) {
		t.Fatalf("jsx-dev-runtime should point at %s, got %q", jsxShim, b.importMap["react/jsx-dev-runtime"])
	}
	if _, ok := b.importMap["react-refresh/runtime"]; ok {
		t.Fatal("react-refresh should not be in the default production import map")
	}

	mu.Lock()
	for _, u := range requested {
		if strings.Contains(u, "?dev") || strings.Contains(u, "&dev") || strings.Contains(u, "react-refresh") {
			t.Errorf("default vendor build fetched development package: %s", u)
		}
	}
	mu.Unlock()

	// Default ServePage serves production React.
	rec := httptest.NewRecorder()
	r.ServePage(rec, httptest.NewRequest("GET", "/", nil), "nonce1")
	body := rec.Body.String()
	if !strings.Contains(body, `"react":"production"`) {
		t.Fatalf("default page did not run production React:\n%s", body)
	}
	if strings.Contains(body, "react-refresh/runtime") {
		t.Fatal("default production page should not include react-refresh")
	}

	// Opting into development builds the dev vendor bundle on demand and serves development React.
	devReq := httptest.NewRequest("GET", "/", nil)
	devReq.Header.Set("Cookie", "gerdr_react=development")
	devRec := httptest.NewRecorder()
	r.ServePage(devRec, devReq, "nonce2")
	devBody := devRec.Body.String()
	if !strings.Contains(devBody, `"react":"development"`) {
		t.Fatalf("dev cookie page did not run development React:\n%s", devBody)
	}
	if !strings.Contains(devBody, `"react-refresh/runtime":"/_esm/~vendor/`) {
		t.Fatalf("dev page missing bundled react-refresh:\n%s", devBody)
	}
	mu.Lock()
	sawDev := false
	for _, u := range requested {
		if strings.Contains(u, "react@19.3.1?dev") {
			sawDev = true
			break
		}
	}
	mu.Unlock()
	if !sawDev {
		t.Errorf("development opt-in did not fetch ?dev react; requested: %v", requested)
	}
}
