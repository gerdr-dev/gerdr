package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// An inbox card clicked or closed in one window reaches every other window / device as "cards_closed".
func TestCardsClosedBroadcast(t *testing.T) {
	console, ch, _ := hostPair(t)
	cs := httptest.NewServer(ch)
	defer cs.Close()
	hdrs := http.Header{"Authorization": {"Bearer " + console.Token}}
	dial := func() *websocket.Conn {
		c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(cs.URL, "http")+"/ws", hdrs)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	a, b := dial(), dial()
	defer a.Close()
	defer b.Close()
	time.Sleep(100 * time.Millisecond) // both subscribed before the message goes out
	_ = a.WriteJSON(map[string]any{"t": "cards", "ids": []string{"inbox:w1"}})
	deadline := time.Now().Add(5 * time.Second)
	_ = b.SetReadDeadline(deadline)
	for time.Now().Before(deadline) {
		_, raw, err := b.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var batch []struct {
			T    string
			Data []string
		}
		_ = json.Unmarshal(raw, &batch)
		for _, m := range batch {
			if m.T == "cards_closed" && len(m.Data) == 1 && m.Data[0] == "inbox:w1" {
				return
			}
		}
	}
	t.Fatal("the other window never heard of the closed card")
}
