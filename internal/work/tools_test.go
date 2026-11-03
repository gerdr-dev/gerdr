package work

import (
	"context"
	"testing"
	"time"
)

func TestTools(t *testing.T) {
	r, _ := newProject(t, map[string]string{
		"board.ts": `export const tools = {
  ask: { description: "ask the user", input: { type: "object", properties: { what: { type: "string" } }, required: ["what"] } },
  ghost: { description: "declared, no function" },
};
export function ask(input: any, ctx: any) { return { what: input.what, by: ctx.caller.session } }
export function notATool() { return 1 }`,
		// Files without a tools export are the common case and must be skipped, not crash the listing
		"plain.ts": `export function hello() { return 1 }`,
	})
	tools, err := r.Tools("")
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "ask" || tools[0].Fn != "board.ask" || tools[0].Input["required"] == nil {
		t.Fatalf("tools %+v", tools)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := r.CallTool(ctx, "", "ask", "s123", map[string]any{"what": "sign in"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["what"] != "sign in" || m["by"] != "s123" {
		t.Errorf("result %v", m)
	}
	if _, err := r.CallTool(ctx, "", "ghost", "s1", nil); err == nil {
		t.Error("a tool without its function must not run")
	}
	// A plain run has no caller
	res, _, err = run(t, r, "board.ask", map[string]any{"what": "x"})
	if err != nil || res.(map[string]any)["by"] != "" {
		t.Errorf("plain run: %v %v", res, err)
	}
}
