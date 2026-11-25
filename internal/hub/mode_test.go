package hub

import (
	"context"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/gerdr-dev/gerdr/agents/native"
)

func TestNewSessionDefaultEffort(t *testing.T) {
	cfg := native.Config{Providers: []native.Provider{{
		ID: "test", BaseURL: "http://localhost", APIKey: "test",
		Models: []native.Model{
			{ID: "with-medium", Efforts: []string{"low", "medium", "high"}, DefaultEffort: "high"},
			{ID: "without-medium", Efforts: []string{"low", "high"}, DefaultEffort: "high"},
		},
	}}}
	h, err := New(Options{
		Dir: t.TempDir(),
		LLM: func() native.Config { return cfg },
		ExtraAgents: []AgentDef{
			{ID: "worker", Base: "native"},
			{ID: "reviewer", Base: "native"},
		},
		Defaults: func(agent string) NewDefaults {
			if agent == "reviewer" {
				return NewDefaults{Config: map[string]string{"effort": "low"}}
			}
			return NewDefaults{}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	for _, tt := range []struct {
		name, agent string
		config      map[string]string
		want        string
	}{
		{name: "built-in", agent: "native", want: "medium"},
		{name: "role", agent: "worker", want: "medium"},
		{name: "configured", agent: "reviewer", want: "low"},
		{name: "explicit", agent: "reviewer", config: map[string]string{"effort": "high"}, want: "high"},
		{name: "unsupported", agent: "native", config: map[string]string{"model": "test/without-medium"}, want: "high"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, err := h.Create(context.Background(), CreateOptions{Agent: tt.agent, Cwd: t.TempDir(), Config: tt.config})
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Summary().Config["effort"]; got != tt.want {
				t.Fatalf("effort = %q, want %q", got, tt.want)
			}
		})
	}
}

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

// A model / effort named by code must be one the agent offers: the error lists what it does offer.
func TestCheckConfig(t *testing.T) {
	vals := acp.SessionConfigSelectOptionsUngrouped{{Value: "sonnet"}, {Value: "haiku"}}
	opts := []acp.SessionConfigOption{{Select: &acp.SessionConfigOptionSelect{Id: "model", Options: acp.SessionConfigSelectOptions{Ungrouped: &vals}}}}
	if err := checkConfig(nil, opts); err != nil {
		t.Fatalf("nothing given: %v", err)
	}
	if err := checkConfig(map[string]string{"model": "haiku"}, opts); err != nil {
		t.Fatalf("an offered value: %v", err)
	}
	if err := checkConfig(map[string]string{"model": "opus"}, opts); err == nil || !strings.Contains(err.Error(), "sonnet, haiku") {
		t.Fatalf("an unknown model should be refused with the choices, got %v", err)
	}
	if err := checkConfig(map[string]string{"effort": "high"}, opts); err == nil {
		t.Fatal("an option the agent lacks should be refused")
	}
}

// optionChoices gives coordinators the picker's names next to the ids: "fable" alone doesn't say which version it is.
func TestOptionChoices(t *testing.T) {
	desc := "For your toughest challenges"
	vals := acp.SessionConfigSelectOptionsUngrouped{{Value: "fable", Name: "Fable 5.2", Description: &desc}, {Value: "haiku", Name: "Haiku 5.5"}}
	opts := []acp.SessionConfigOption{{Select: &acp.SessionConfigOptionSelect{Id: "model", Options: acp.SessionConfigSelectOptions{Ungrouped: &vals}}}}
	got := optionChoices(opts, "model")
	if len(got) != 2 || got[0]["id"] != "fable" || got[0]["name"] != "Fable 5.2" || got[0]["description"] != desc {
		t.Fatalf("got %v", got)
	}
	if _, ok := got[1]["description"]; ok {
		t.Fatalf("no description should mean no key, got %v", got[1])
	}
	if optionChoices(opts, "effort") != nil {
		t.Fatal("an option the agent lacks should give nil")
	}
}
