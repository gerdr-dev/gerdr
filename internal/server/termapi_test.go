//go:build !windows

package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gerdr-dev/gerdr/internal/term"
)

// 输入 → 输出走通；断开重连时先收到重放（之前的输出还在）；shell 退出时收到退出码。
func TestTermWS(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	tm := term.New(os.Environ, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer tm.Close()
	s := &Server{Token: "tok", Terms: tm}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/api/terms", strings.NewReader(`{"cwd":"/"}`))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("X-Gerdr", "1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var info term.Info
	_ = json.NewDecoder(res.Body).Decode(&info)
	res.Body.Close()
	if info.ID == "" {
		t.Fatal("没建出来")
	}

	dial := func() *websocket.Conn {
		h := http.Header{"Authorization": {"Bearer tok"}}
		c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/api/terms/"+info.ID+"/ws", h)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	// 一直读到出现 want；返回读到的全部输出和最后一条控制消息
	readUntil := func(c *websocket.Conn, want string) (string, string) {
		var out strings.Builder
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			typ, b, err := c.ReadMessage()
			if err != nil {
				t.Fatalf("等 %q 没等到：%v，读到 %q", want, err, out.String())
			}
			if typ == websocket.TextMessage {
				if strings.Contains(string(b), want) {
					return out.String(), string(b)
				}
				continue
			}
			out.Write(b)
			if strings.Contains(out.String(), want) {
				return out.String(), ""
			}
		}
	}

	c := dial()
	_ = c.WriteMessage(websocket.TextMessage, []byte(`{"t":"resize","cols":100,"rows":30}`))
	_ = c.WriteMessage(websocket.BinaryMessage, []byte("echo ws-$((6*7))\n"))
	readUntil(c, "ws-42")
	c.Close()

	c = dial()
	defer c.Close()
	readUntil(c, "ws-42") // 重放
	_ = c.WriteMessage(websocket.BinaryMessage, []byte("exit 5\n"))
	_, ctl := readUntil(c, `"exit"`)
	if !strings.Contains(ctl, `"code":5`) {
		t.Fatalf("退出消息不对：%s", ctl)
	}
}
