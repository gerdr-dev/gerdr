package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAddonFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Echo-Method", r.Method)
		if r.Header.Get("X-Token") != "secret" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"no token"}`))
			return
		}
		_, _ = w.Write([]byte("got:" + string(b)))
	}))
	defer srv.Close()

	body := "hi"
	res, err := doAddonFetch(context.Background(), addonFetchReq{URL: srv.URL, Method: "post", Headers: map[string]string{"X-Token": "secret"}, Body: &body})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || res.Body != "got:hi" || res.Headers["x-echo-method"] != "POST" {
		t.Fatalf("unexpected %+v", res)
	}

	// A non-2xx answer comes back as data, like fetch(), so the add-on can read the error body
	res, err = doAddonFetch(context.Background(), addonFetchReq{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 401 || res.StatusText != "Unauthorized" || res.Body != `{"error":"no token"}` {
		t.Fatalf("unexpected %+v", res)
	}

	for _, u := range []string{"file:///etc/passwd", "ftp://x", "not a url", ""} {
		if _, err := doAddonFetch(context.Background(), addonFetchReq{URL: u}); err == nil {
			t.Errorf("%q: want an error", u)
		}
	}
}
