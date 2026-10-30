package config

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
)

// Hidden stands in config.json for a secret that lives in secrets.json.
//
// Why split them: config.json is what agents read and edit to change settings (and what the UI, remote
// clients and the Android App receive). A key sitting in it ends up in whatever transcript happened to
// `cat` the file, and from there with the model provider. secrets.json is never served over the API.
// It is not protection against an agent that goes looking (same OS user), only against leaking by accident.
//
// The UI shows Hidden as a filled password field and sends it back untouched when the key wasn't changed,
// so "keep the stored one" needs no extra field.
const Hidden = "<hidden>"

// LLMKeyName is a native provider's API key's name in secrets.json.
func LLMKeyName(providerID string) string { return "llm/" + providerID }

// Secrets is secrets.json (0600): a flat name → value map. Read on every Get so a hand edit counts.
type Secrets struct {
	path string
	mu   sync.Mutex
}

func NewSecrets(path string) *Secrets { return &Secrets{path: path} }

func (s *Secrets) load() map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (s *Secrets) save(m map[string]string) error {
	out, _ := json.MarshalIndent(m, "", "  ")
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Secrets) Get(name string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()[name]
}

// Names lists the stored names starting with prefix, prefix cut off (never the values).
func (s *Secrets) Names(prefix string) []string {
	out := []string{}
	if s == nil {
		return out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.load() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, strings.TrimPrefix(k, prefix))
		}
	}
	sort.Strings(out)
	return out
}

// Set stores a value; "" deletes it.
func (s *Secrets) Set(name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.load()
	if m[name] == value {
		return nil
	}
	if value == "" {
		delete(m, name)
	} else {
		m[name] = value
	}
	return s.save(m)
}

// Split moves plaintext secrets out of a config.json body into secrets.json and puts Hidden in their place.
// Today that is llm.providers[].apiKey. Returns the body unchanged (and false) when there was nothing to move.
func (s *Secrets) Split(b json.RawMessage) (json.RawMessage, bool) {
	var root map[string]any
	if json.Unmarshal(b, &root) != nil {
		return b, false
	}
	changed := false
	for _, p := range llmProviders(root) {
		id, _ := p["id"].(string)
		key, _ := p["apiKey"].(string)
		if id == "" || key == "" || key == Hidden {
			continue
		}
		if s.Set(LLMKeyName(id), key) != nil {
			continue // keep it in config.json rather than lose it
		}
		p["apiKey"] = Hidden
		changed = true
	}
	if !changed {
		return b, false
	}
	out, err := json.Marshal(root)
	if err != nil {
		return b, false
	}
	return out, true
}

// Prune drops the LLM keys config.json no longer points at (provider deleted, or switched to apiKeyEnv).
// Only called on saves from the UI, which always sends the whole config: a hand edit that briefly breaks
// a provider shouldn't cost the user their key.
func (s *Secrets) Prune(b json.RawMessage) {
	var root map[string]any
	if json.Unmarshal(b, &root) != nil {
		return
	}
	keep := map[string]bool{}
	for _, p := range llmProviders(root) {
		if id, _ := p["id"].(string); id != "" && p["apiKey"] == Hidden {
			keep[LLMKeyName(id)] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.load()
	n := len(m)
	for k := range m {
		if strings.HasPrefix(k, "llm/") && !keep[k] {
			delete(m, k)
		}
	}
	if len(m) != n {
		_ = s.save(m)
	}
}

func llmProviders(root map[string]any) []map[string]any {
	llm, _ := root["llm"].(map[string]any)
	list, _ := llm["providers"].([]any)
	var out []map[string]any
	for _, v := range list {
		if p, ok := v.(map[string]any); ok {
			out = append(out, p)
		}
	}
	return out
}
