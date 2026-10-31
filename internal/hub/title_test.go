package hub

import (
	"encoding/json"
	"strings"
	"testing"
)

func userEv(text string) *Event {
	b, _ := json.Marshal(map[string]string{"type": "text", "text": text})
	return &Event{Kind: EvUser, Content: []json.RawMessage{b}}
}

// clear 之后从头数；斜杠命令不算
func TestPromptsSinceClear(t *testing.T) {
	s := &Session{loaded: true, events: []*Event{
		userEv("修一下登录页"), userEv("再看看注册"), userEv("/clear"),
		{Kind: EvNotice, Level: "clear"},
		userEv("/model haiku"), userEv("给侧栏加个搜索框"), userEv("  "),
	}}
	got := s.Prompts()
	if len(got) != 1 || got[0] != "给侧栏加个搜索框" {
		t.Fatalf("prompts = %q", got)
	}
}

// 超长时丢前面的、留后面的
func TestBuildTitlePrompt(t *testing.T) {
	long := strings.Repeat("前", 700)
	p := buildTitlePrompt([]string{"最早那句", long, long, long, long, long, "最后一句"})
	if strings.Contains(p, "最早那句") || !strings.Contains(p, "最后一句") {
		t.Fatalf("prompt 没按「留后面的」裁: %d 字", len([]rune(p)))
	}
	if !strings.HasSuffix(p, "</msgs>") {
		t.Fatal("没收尾")
	}
}

func TestCleanTitle(t *testing.T) {
	for in, want := range map[string]string{
		"「修登录页」。":               "修登录页",
		"ok\n我来帮你":              "",
		strings.Repeat("长", 41): "",
	} {
		if got := cleanTitle(in); got != want {
			t.Errorf("cleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
