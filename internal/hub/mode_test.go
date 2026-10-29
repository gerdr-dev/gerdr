package hub

import (
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestCheckMode(t *testing.T) {
	ms := &acp.SessionModeState{AvailableModes: []acp.SessionMode{{Id: "default"}, {Id: "bypassPermissions"}}}
	if err := checkMode("", ms); err != nil {
		t.Fatalf("没给模式不该报错：%v", err)
	}
	if err := checkMode("bypassPermissions", ms); err != nil {
		t.Fatalf("合法模式不该报错：%v", err)
	}
	if err := checkMode("full-access", nil); err != nil {
		t.Fatalf("agent 没报模式时不查：%v", err)
	}
	err := checkMode("full-access", ms)
	if err == nil || !strings.Contains(err.Error(), "default、bypassPermissions") {
		t.Fatalf("不认识的模式要报错并列出可选值，得到 %v", err)
	}
}
