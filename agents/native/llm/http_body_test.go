package llm

import "testing"

func TestHTTPErrorShowsBodyMessage(t *testing.T) {
	cases := map[string]string{
		`{"error":{"code":"insufficient_quota","message":"AI 积分不足，请到 creght 充值","type":"insufficient_quota"}}`: "model API 402: AI 积分不足，请到 creght 充值",
		`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`:                                 "model API 402: Overloaded",
		`{"error":"bad key"}`:   "model API 402: bad key",
		`{"message":"nope"}`:    "model API 402: nope",
		`<html>502</html>`:      "model API 402: <html>502</html>",
		`{"error":{"code":1}}`:  `model API 402: {"error":{"code":1}}`,
	}
	for body, want := range cases {
		if got := (&HTTPError{Status: 402, Body: body}).Error(); got != want {
			t.Errorf("%s:\n got %q\nwant %q", body, got, want)
		}
	}
}
