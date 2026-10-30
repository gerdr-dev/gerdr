package plugins

import (
	"log/slog"
	"strings"
	"testing"
)

// 自带的插件必须编得过（它们是给人照着抄的样板）。
func TestBuiltinPluginsCompile(t *testing.T) {
	m := New(t.TempDir(), slog.Default())
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	list := m.List()
	ids := map[string]bool{}
	for _, p := range list {
		ids[p.ID] = true
		if p.Error != "" {
			t.Errorf("%s: %s", p.ID, p.Error)
		}
		if !strings.Contains(p.Code, "definePlugin") {
			t.Errorf("%s: unexpected output", p.ID)
		}
	}
	// 下划线开头的默认不加载
	if !ids["duplicate-tab"] || ids["_notify-when-done"] {
		t.Fatalf("ids = %v", ids)
	}
}
