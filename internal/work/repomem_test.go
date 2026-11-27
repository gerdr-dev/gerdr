package work

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/repomem"
)

func TestCtxRepoMemory(t *testing.T) {
	r, _ := newProject(t, map[string]string{"t.ts": `
export async function go(input, ctx) {
  const a = ctx.repoMemory.add("/src/billing", "ships straight to main")
  const b = ctx.repoMemory.add("/src/billing", "ships straight to main")
  const all = ctx.repoMemory.list()
  ctx.repoMemory.write("/src/billing", "- release approved by Zhang")
  const read = ctx.repoMemory.read("/src/billing")
  return [a.added, b.added, all.length, all[0].repo, read.text].join("|")
}`})
	res, _, err := run(t, r, "t.go", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(res); got != "true|false|1|/src/billing|- release approved by Zhang" {
		t.Fatalf("got %s", got)
	}
	if n := repomem.Read(r.Root, "/src/billing"); !strings.Contains(n.Text, "Zhang") {
		t.Fatalf("not on disk: %+v", n)
	}
}
