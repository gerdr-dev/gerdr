package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSecretsSplitAndPrune(t *testing.T) {
	dir := t.TempDir()
	sec := NewSecrets(filepath.Join(dir, "secrets.json"))
	st := NewStore(filepath.Join(dir, "config.json"), `{}`)
	st.SetFilter(sec.Split)

	in := `{"fontSize":15,"llm":{"providers":[{"id":"a","apiKey":"sk-a"},{"id":"b","apiKeyEnv":"B_KEY"}]}}`
	if err := st.Set(json.RawMessage(in)); err != nil {
		t.Fatal(err)
	}
	if got := string(st.Get()); strings.Contains(got, "sk-a") || !strings.Contains(got, Hidden) {
		t.Fatalf("key left in config.json: %s", got)
	}
	if sec.Get(LLMKeyName("a")) != "sk-a" {
		t.Fatal("key not in secrets.json")
	}
	if fi, _ := os.Stat(filepath.Join(dir, "secrets.json")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("secrets.json mode %v", fi.Mode().Perm())
	}

	// Saving with the placeholder keeps the stored key.
	_ = st.Set(json.RawMessage(`{"llm":{"providers":[{"id":"a","apiKey":"<hidden>"}]}}`))
	sec.Prune(st.Get())
	if sec.Get(LLMKeyName("a")) != "sk-a" {
		t.Fatal("placeholder lost the key")
	}

	// A hand edit that writes a key into the file gets moved out too.
	time.Sleep(10 * time.Millisecond)
	_ = os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"llm":{"providers":[{"id":"a","apiKey":"sk-new"}]}}`), 0o600)
	future := time.Now().Add(time.Second)
	_ = os.Chtimes(filepath.Join(dir, "config.json"), future, future)
	if got := string(st.Get()); strings.Contains(got, "sk-new") {
		t.Fatalf("hand-written key left in config.json: %s", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "config.json")); strings.Contains(string(b), "sk-new") {
		t.Fatal("file not rewritten")
	}
	if sec.Get(LLMKeyName("a")) != "sk-new" {
		t.Fatal("hand-written key not stored")
	}

	// Deleting the provider from the UI drops its key.
	_ = st.Set(json.RawMessage(`{"llm":{"providers":[]}}`))
	sec.Prune(st.Get())
	if sec.Get(LLMKeyName("a")) != "" {
		t.Fatal("pruned key still there")
	}
}
