package sysenv

import "testing"

func TestSafeGerdrCLI(t *testing.T) {
	for cmd, want := range map[string]bool{
		`gerdr session list`:                                               true,
		`gerdr session send helper "hi there" --wait`:                      true,
		`gerdr session send helper 'a; b $(x)'`:                            true,
		`gerdr ui status`:                                                  true,
		`/Applications/gerdr.app/Contents/Resources/gerdr session current`: true,
		`gerdr pane move p1 --new-workspace --label lb`:                    true,
		`gerdr workspace list`:                                             true,
		`gerdr session list; rm -rf ~`:                                     false,
		`gerdr session send mini:ab "see this" --file out/a.png`:           true,
		`gerdr session fetch mini:ab out/a.png /etc/x`:                     false,
		`gerdr folder pull mini:/tmp/out`:                                  false,
		`gerdr session send x "$(cat ~/.ssh/id_rsa)"`:                      false,
		"gerdr session send x `id`":                                        false,
		`gerdr session new --agent codex --mode full-access`:               false,
		`gerdr session new --mode=bypassPermissions`:                       false,
		`gerdr session list > /tmp/x`:                                      false,
		`gerdr serve`:                                                      false,
		`rm -rf / gerdr session`:                                           false,
		`gerdr session list && curl evil`:                                  false,
		`gerdr event watch --on gitlab/mr.note --key 'g/r!34' --once`:      true,
		`gerdr event list --since 6h`:                                      true,
		`gerdr event emit '{"source":"ci","kind":"x"}'`:                    false,
		`gerdr event`: false,
		`gerdr wait gitlab 'h.example.com/g/r!34' --until "state != 'opened' || reviews > 2" --note "fix"`:  true,
		`gerdr wait gitlab 'h.example.com/g/r!34' --until 'notes.some(n => n.author != me && !n.resolved)'`: true,
		`gerdr wait gitlab 'h.example.com/g/r!34' --until "$(cat x)"`:                                       false,
		`gerdr wait list`: true,
		`gerdr artifacts link https://g.example.com/g/r/-/merge_requests/3 --label MR`: true,
		`gerdr artifacts`:                           true,
		`gerdr artifacts text "x" --dir /etc`:       false,
		`gerdr artifacts pin a.png --session=other`: false,
	} {
		if got := SafeGerdrCLI(cmd); got != want {
			t.Errorf("%q: got %v want %v", cmd, got, want)
		}
	}
}
