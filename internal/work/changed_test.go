package work

import (
	"strings"
	"sync"
	"testing"
)

// Every way a function changes its panel's files is reported, with the panel's key and the path relative to its
// directory: pages refresh on it instead of polling. Reading reports nothing.
func TestChangedReported(t *testing.T) {
	r, _ := newProject(t, map[string]string{"t.ts": `
export function touch(input, ctx) {
  ctx.fs.read('local/t.ts')
  ctx.fs.write('user/data/a.json', '{}')
  ctx.fs.mkdir('user/data/dir')
  ctx.fs.copy('user/data/a.json', 'user/data/b.json')
  ctx.fs.move('user/data/b.json', 'user/data/c.json')
  ctx.fs.trash('user/data/c.json')
  return 1
}`})
	var mu sync.Mutex
	var got []string
	r.Changed = func(key, path string) {
		mu.Lock()
		got = append(got, key+":"+path)
		mu.Unlock()
	}
	mustRun(t, r, "t.touch", nil)
	want := "default:user/data/a.json default:user/data/dir default:user/data/b.json default:user/data/b.json default:user/data/c.json default:user/data/c.json"
	if strings.Join(got, " ") != want {
		t.Fatalf("got  %v\nwant %v", strings.Join(got, " "), want)
	}
}
