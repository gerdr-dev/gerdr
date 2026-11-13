package work

import (
	"fmt"
	"strings"
	"testing"
)

type fakeBadges []string

func (f *fakeBadges) SetBadge(panel string, n int, add bool) error {
	*f = append(*f, fmt.Sprintf("%s:%d:%v", panel, n, add))
	return nil
}

func TestCtxBadge(t *testing.T) {
	r, _ := newProject(t, map[string]string{"t.ts": `
export async function go(input, ctx) {
  ctx.badge.set(3)
  ctx.badge.set(true)
  ctx.badge.set(null)
  ctx.badge.set(0)
  ctx.badge.set(100000)
  ctx.badge.add()
  ctx.badge.add(2)
  ctx.badge.clear()
  let bad = ''
  try { ctx.badge.set('x') } catch (e) { bad = e.message }
  return bad
}`})
	var got fakeBadges
	r.Badges = &got
	res, _, err := run(t, r, "t.go", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(res), "ctx.badge.set takes") {
		t.Fatalf("bad input not refused: %v", res)
	}
	p := got[0][:strings.Index(got[0], ":")]
	want := []string{"3:false", "0:false", "-1:false", "-1:false", fmt.Sprintf("%d:false", BadgeMax), "1:true", "2:true", "-1:false"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i, w := range want {
		if got[i] != p+":"+w {
			t.Fatalf("call %d: got %s, want %s", i, got[i], w)
		}
	}
}
