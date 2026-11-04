package server

import (
	"encoding/json"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/hub"
)

var (
	permReq = hub.PendingReq{SessionID: "s1", Cwd: "/x/proj", ReqID: "r1", Kind: "permission", Request: json.RawMessage(`{
		"toolCall":{"title":"Bash: rm -rf build","kind":"execute","rawInput":{"command":"rm -rf build"}},
		"options":[{"optionId":"ok","name":"Allow","kind":"allow_once"},{"optionId":"no","name":"Reject","kind":"reject_once"}]}`)}
	askReq = hub.PendingReq{SessionID: "s1", ReqID: "r2", Kind: "ask", Request: json.RawMessage(`{"questions":[
		{"id":"db","question":"Which database?","options":[{"label":"pg"},{"label":"mysql"}]},
		{"id":"name","question":"Name?","allowOther":true,"options":[{"label":"a"}]},
		{"id":"feat","question":"Features?","multiSelect":true,"options":[{"label":"x"},{"label":"y"}]}]}`)}
)

// The agent's request taken apart for an add-on: the tool and the options' ids, the questions; the session's name
// falls back to its directory.
func TestToSvcPending(t *testing.T) {
	v := toSvcPending(permReq, true)
	if v.Title != "proj" || !v.Muted || v.Tool == nil || v.Tool.Title != "Bash: rm -rf build" || v.Tool.Kind != "execute" ||
		string(v.Tool.Input) != `{"command":"rm -rf build"}` || len(v.Options) != 2 || v.Options[1] != (svcOption{"no", "Reject", "reject_once"}) {
		t.Fatalf("permission = %+v (tool %+v)", v, v.Tool)
	}
	if q := toSvcPending(askReq, false).Questions; len(q) != 3 || q[0].Id != "db" || !q[1].AllowOther || !q[2].MultiSelect {
		t.Fatalf("questions = %+v", q)
	}
}

// An answer is checked against the request it answers before the session gets it.
func TestAnswerResponse(t *testing.T) {
	all := map[string][]string{"db": {"pg"}, "name": {"my own"}, "feat": {"x", "y"}}
	for _, c := range []struct {
		name string
		req  hub.PendingReq
		a    svcAnswer
		want string // "" = refused
	}{
		{"an option", permReq, svcAnswer{OptionID: "no"}, `{"optionId":"no"}`},
		{"not an option", permReq, svcAnswer{OptionID: "always"}, ""},
		{"a permission can't be skipped", permReq, svcAnswer{Skip: true}, ""},
		{"all answered", askReq, svcAnswer{Answers: all}, `{"answers":{"db":["pg"],"feat":["x","y"],"name":["my own"]}}`},
		{"skipped", askReq, svcAnswer{Skip: true}, `{"cancelled":true,"reason":"skipped"}`},
		{"one missing", askReq, svcAnswer{Answers: map[string][]string{"db": {"pg"}, "name": {"a"}}}, ""},
		{"not an option, no other allowed", askReq, svcAnswer{Answers: map[string][]string{"db": {"sqlite"}, "name": {"a"}, "feat": {"x"}}}, ""},
		{"two for a single choice", askReq, svcAnswer{Answers: map[string][]string{"db": {"pg", "mysql"}, "name": {"a"}, "feat": {"x"}}}, ""},
		{"unknown question", askReq, svcAnswer{Answers: map[string][]string{"db": {"pg"}, "name": {"a"}, "feat": {"x"}, "zz": {"1"}}}, ""},
	} {
		got, err := answerResponse(c.req, c.a)
		if c.want == "" {
			if err == nil {
				t.Errorf("%s: taken (%s), want refused", c.name, got)
			}
			continue
		}
		if err != nil || string(got) != c.want {
			t.Errorf("%s: %s, %v; want %s", c.name, got, err, c.want)
		}
	}
}
