package server

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// g.socket: the backend dials with the add-on's headers, and frames pass both ways as they are (binary stays binary)
func TestAddonSocket(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Key") != "secret" {
			http.Error(w, "no key", 401)
			return
		}
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			mt, b, err := c.ReadMessage()
			if err != nil {
				return
			}
			_ = c.WriteMessage(mt, append([]byte("echo:"), b...))
		}
	}))
	defer up.Close()
	upURL := "ws" + strings.TrimPrefix(up.URL, "http")

	s := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/addons/{id}/socket/{token}", s.addonSocketConnect)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/addons/voice/socket/"
	put := func(tok string, h map[string]string) {
		addonSockets.Lock()
		addonSockets.m[tok] = addonSocketReq{URL: upURL, Headers: h, addon: "voice", at: time.Now()}
		addonSockets.Unlock()
	}

	put("t1", map[string]string{"X-Key": "secret"})
	c, _, err := websocket.DefaultDialer.Dial(base+"t1", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.WriteMessage(websocket.BinaryMessage, []byte{0, 1, 0xff})
	if mt, b, err := c.ReadMessage(); err != nil || mt != websocket.BinaryMessage || string(b) != "echo:\x00\x01\xff" {
		t.Fatalf("binary: %v %v %q", mt, err, b)
	}
	_ = c.WriteMessage(websocket.TextMessage, []byte("hi"))
	if mt, b, err := c.ReadMessage(); err != nil || mt != websocket.TextMessage || string(b) != "echo:hi" {
		t.Fatalf("text: %v %v %q", mt, err, b)
	}
	c.Close()

	// A token works once
	if _, resp, err := websocket.DefaultDialer.Dial(base+"t1", nil); err == nil || resp == nil || resp.StatusCode != 404 {
		t.Fatalf("reused token: %v", err)
	}
	// Another add-on's token isn't this one's
	put("t2", map[string]string{"X-Key": "secret"})
	other := strings.Replace(base, "/voice/", "/tts/", 1)
	if _, resp, err := websocket.DefaultDialer.Dial(other+"t2", nil); err == nil || resp == nil || resp.StatusCode != 404 {
		t.Fatalf("other add-on: %v", err)
	}
	// The service's refusal comes back as its status, not an upgrade that closes at once
	put("t3", nil)
	if _, resp, err := websocket.DefaultDialer.Dial(base+"t3", nil); err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatalf("refused: %v %v", err, resp)
	}
}

// "bodyBase64": bytes go out unchanged (a recording to a speech-to-text API)
func TestAddonFetchBodyBase64(t *testing.T) {
	sent := []byte{0x52, 0x49, 0x46, 0x46, 0x00, 0xff}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if string(b) != string(sent) {
			w.WriteHeader(400)
		}
	}))
	defer srv.Close()
	body := base64.StdEncoding.EncodeToString(sent)
	res, err := doAddonFetch(context.Background(), addonFetchReq{URL: srv.URL, Method: "POST", Body: &body, BodyBase64: true})
	if err != nil || res.Status != 200 {
		t.Fatalf("%v %+v", err, res)
	}
	bad := "not base64!"
	if _, err := doAddonFetch(context.Background(), addonFetchReq{URL: srv.URL, Method: "POST", Body: &bad, BodyBase64: true}); err == nil {
		t.Fatal("want an error for a body that isn't base64")
	}
}
