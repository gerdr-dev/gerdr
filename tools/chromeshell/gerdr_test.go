package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWindowLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/code" || r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("X-Gerdr") != "1" {
			http.Error(w, "no", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"code":"c0de"}`))
	}))
	defer srv.Close()
	got := windowLink(srv.URL + "/auth?token=tok&next=%2F")
	if strings.Contains(got, "tok") || !strings.Contains(got, "code=c0de") || !strings.Contains(got, "next=%2F") {
		t.Fatalf("with codes: %s", got)
	}
	// A gerdr without codes: the token, as before.
	old := srv.URL + "/auth?token=other"
	if got := windowLink(old); got != old {
		t.Fatalf("without codes: %s", got)
	}
}
