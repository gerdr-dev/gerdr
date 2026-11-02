package server

import (
	"bytes"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/config"
)

// Marking one session read or unread changes only that one: two devices each sending their change both stick.
func TestPostUnread(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unread.json")
	s := &Server{Unread: config.NewStore(path, `{}`)}
	post := func(body string) int {
		w := httptest.NewRecorder()
		s.postUnread(w, httptest.NewRequest("POST", "/api/unread", bytes.NewReader([]byte(body))))
		return w.Code
	}
	if c := post(`{"id":"a","unread":true}`); c != 200 {
		t.Fatalf("code %d", c)
	}
	post(`{"id":"b","unread":true}`)
	post(`{"id":"a","unread":false}`)
	got := s.unreadSet()
	if len(got) != 1 || !got["b"] {
		t.Fatalf("want {b}, got %v", got)
	}
	// Survives a restart
	if again := (&Server{Unread: config.NewStore(path, `{}`)}).unreadSet(); len(again) != 1 || !again["b"] {
		t.Fatalf("reloaded %v", again)
	}
	if c := post(`{"unread":true}`); c != 400 {
		t.Fatalf("no id: code %d", c)
	}
}
