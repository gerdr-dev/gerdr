package codex

import "testing"

func TestCellOutput(t *testing.T) {
	cases := []struct {
		raw    string
		want   string
		failed bool
	}{
		// Shapes copied from real rollouts (codex 0.166).
		{`[{"type":"input_text","text":"Script completed\nWall time 0.0 seconds\nOutput:\n"},{"type":"input_text","text":"[\"a\"]"},{"type":"input_text","text":"[\"b\"]"}]`, "[\"a\"]\n[\"b\"]", false},
		{`"Script running with cell ID 6\nWall time 1.0 seconds\nOutput:\n"`, "Script running with cell ID 6", false},
		{`[{"type":"input_text","text":"Script failed\nWall time 0.0 seconds\nOutput:\n"},{"type":"input_text","text":"Script error:\nboom"}]`, "Script failed\nScript error:\nboom", true},
		{`[{"type":"input_text","text":"Script terminated\nWall time 0.0 seconds\nOutput:\n"}]`, "Script terminated", true},
		{`[{"type":"input_text","text":"Script completed\nWall time 0.0 seconds\nOutput:\n"}]`, "", false},
	}
	for _, c := range cases {
		got, failed := cellOutput([]byte(c.raw))
		if got != c.want || failed != c.failed {
			t.Errorf("cellOutput(%s) = %q, %v; want %q, %v", c.raw, got, failed, c.want, c.failed)
		}
	}
}

func TestParseRollLine(t *testing.T) {
	keep := []string{
		`{"timestamp":"2026-10-30T02:49:46.526Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c1","name":"exec","input":"text(1)"}}`,
		`{"timestamp":"2026-10-30T02:49:46.526Z","type":"response_item","payload":{"type":"function_call","call_id":"c2","name":"wait","arguments":"{\"cell_id\":\"6\"}"}}`,
		`{"timestamp":"2026-10-30T02:49:48.728Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1","output":"x"}}`,
	}
	drop := []string{
		// apply_patch is a custom tool too, but the app-server shows it as a fileChange item.
		`{"timestamp":"2026-10-30T02:49:46.526Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c3","name":"apply_patch","input":"…"}}`,
		// sleep / spawn_agent have their own items.
		`{"timestamp":"2026-10-30T02:49:46.526Z","type":"response_item","payload":{"type":"function_call","call_id":"c4","name":"sleep","namespace":"clock","arguments":"{}"}}`,
		`{"timestamp":"2026-10-30T02:49:46.526Z","type":"event_msg","payload":{"type":"item_completed"}}`,
	}
	for _, l := range keep {
		if _, ok := parseRollLine([]byte(l)); !ok {
			t.Errorf("dropped %s", l)
		}
	}
	for _, l := range drop {
		if _, ok := parseRollLine([]byte(l)); ok {
			t.Errorf("kept %s", l)
		}
	}
}

func TestFirstCodeLine(t *testing.T) {
	if got := firstCodeLine("// @exec: {\"yield_time_ms\": 500}\n\n  await x();\nmore"); got != "await x();" {
		t.Errorf("got %q", got)
	}
}
