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

// 建会话时补设默认配置：先 model 再别的，不在可选项里的跳过。
func TestApplyConfigOrder(t *testing.T) {
	keys := []string{"effort", "zeta", "model"}
	sortKeys(keys)
	if keys[0] != "model" || keys[1] != "effort" || keys[2] != "zeta" {
		t.Fatalf("顺序不对：%v", keys)
	}
	opts := acp.SessionConfigSelectOptionsUngrouped{{Value: "opus"}, {Value: "sonnet"}}
	o := &acp.SessionConfigOptionSelect{Id: "model", Options: acp.SessionConfigSelectOptions{Ungrouped: &opts}}
	if !hasValue(o, "opus") || hasValue(o, "gone") {
		t.Fatal("hasValue 不对")
	}
}
