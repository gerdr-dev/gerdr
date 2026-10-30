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
		`gerdr session send x "$(cat ~/.ssh/id_rsa)"`:                      false,
		"gerdr session send x `id`":                                        false,
		`gerdr session new --agent codex --mode full-access`:               false,
		`gerdr session new --mode=bypassPermissions`:                       false,
		`gerdr session list > /tmp/x`:                                      false,
		`gerdr serve`:                                                      false,
		`rm -rf / gerdr session`:                                           false,
		`gerdr session list && curl evil`:                                  false,
	} {
		if got := SafeGerdrCLI(cmd); got != want {
			t.Errorf("%q: got %v want %v", cmd, got, want)
		}
	}
}
