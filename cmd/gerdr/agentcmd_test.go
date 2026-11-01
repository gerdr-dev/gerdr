package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func agentEnv(t *testing.T) string {
	dir := t.TempDir()
	t.Setenv("GERDR_DIR", dir)
	t.Setenv("HOME", t.TempDir()) // no Claude Code / Codex skills or MCP leaking in
	return dir
}

func mustSpec(t *testing.T, s string) *agentSpec {
	sp, err := parseAgentSpec([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

// create writes the skill, the MCP server, the agent (with the defined names added to its lists) and its defaults;
// a later failure leaves none of it behind.
func TestAgentCreate(t *testing.T) {
	dir := agentEnv(t)
	err := createAgent(mustSpec(t, `{"id":"rev","name":"Rev","base":"claude","skills":[],"mcp":[],
		"define":{"skills":[{"name":"cr","content":"---\ndescription: d\n---\n"}],"mcp":[{"name":"gh","url":"https://x"}]},
		"defaults":{"mode":"plan","config":{"model":"opus"}}}`), false)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Agents []struct {
			ID     string   `json:"id"`
			Skills []string `json:"skills"`
			MCP    []string `json:"mcp"`
		} `json:"agents"`
		DefaultModes  map[string]string            `json:"defaultModes"`
		DefaultConfig map[string]map[string]string `json:"defaultConfig"`
	}
	st, _ := openConfig()
	_ = json.Unmarshal(st.Get(), &cfg)
	if len(cfg.Agents) != 1 || cfg.Agents[0].Skills[0] != "cr" || cfg.Agents[0].MCP[0] != "gh" ||
		cfg.DefaultModes["rev"] != "plan" || cfg.DefaultConfig["rev"]["model"] != "opus" {
		t.Fatalf("config: %+v", cfg)
	}
	if s := openExt().Settings(); len(s.MCP) != 1 || !s.MCP[0].Enabled || s.MCP[0].Type != "http" {
		t.Fatalf("ext: %+v", s)
	}

	// a different skill under an existing name is refused before anything is written
	err = createAgent(mustSpec(t, `{"id":"two","base":"codex","define":{"skills":[{"name":"new","content":"---\ndescription: d\n---\n"},
		{"name":"cr","content":"---\ndescription: other\n---\n"}]}}`), false)
	if err == nil {
		t.Fatal("conflicting skill accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "new")); err == nil {
		t.Fatal("refused create left a skill behind")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "skills", "cr", "SKILL.md")); string(b) != "---\ndescription: d\n---\n" {
		t.Fatalf("existing skill changed: %q", b)
	}

	// rm --prune deletes what only it used
	if err := removeAgent("rev", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", "cr")); err == nil {
		t.Fatal("prune kept the skill")
	}
	if s := openExt().Settings(); len(s.MCP) != 0 {
		t.Fatalf("prune kept the MCP server: %+v", s.MCP)
	}
}

func TestConfigAdd(t *testing.T) {
	agentEnv(t)
	if err := appendConfig("agents", map[string]any{"id": "a", "base": "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := appendConfig("agents", map[string]any{"id": "a", "base": "codex"}); err == nil {
		t.Fatal("duplicate id accepted")
	}
	if err := appendConfig("agents", map[string]any{"id": "b"}); err == nil {
		t.Fatal("agent without base or command accepted")
	}
	if err := appendConfig("theme", "dark"); err == nil {
		t.Fatal("appending to a string setting accepted")
	}
}
