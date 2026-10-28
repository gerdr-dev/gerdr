package server

import "testing"

func TestFuzzyScore(t *testing.T) {
	if fuzzyScore("internal/server/server.go", "zzz") >= 0 {
		t.Fatal("不该匹配")
	}
	// 文件名命中排在只有目录命中的前面
	if fuzzyScore("ui/src/components/chat/Composer.tsx", "comp") <= fuzzyScore("ui/src/components/x.ts", "comp") {
		t.Fatal("文件名命中应当更靠前")
	}
	if fuzzyScore("cmd/gerdr/main.go", "cgm") < 0 {
		t.Fatal("按顺序出现的字符应当算匹配")
	}
}
