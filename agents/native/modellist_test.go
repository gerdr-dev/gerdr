package native

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// Both list shapes as subhub sends them: reasoning levels and output modalities next to the usual fields.
func TestListModelInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1beta/models"):
			fmt.Fprint(w, `{"models":[{"name":"models/gemini-4-neon","supportedGenerationMethods":["generateContent"],"reasoning":{"efforts":["low","medium","high","xhigh"],"default":"medium"},"architecture":{"output_modalities":["text"]}},
				{"name":"models/gemini-x-image","architecture":{"output_modalities":["image"]}},{"name":"models/embed","supportedGenerationMethods":["embedContent"]}]}`)
		case strings.HasSuffix(r.URL.Path, "/v1/models"):
			fmt.Fprint(w, `{"data":[{"id":"gpt-6.1-sol","reasoning":{"efforts":["low","high"],"default":"high"}},{"id":"plain"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	g, err := ListModelInfo(context.Background(), Provider{ID: "g", API: "gemini", BaseURL: srv.URL + "/v1beta"})
	if err != nil || len(g) != 2 {
		t.Fatalf("gemini list %+v %v (embed left out)", g, err)
	}
	if g[0].ID != "gemini-4-neon" || fmt.Sprint(g[0].Efforts) != "[low medium high xhigh]" || g[0].DefaultEffort != "medium" || fmt.Sprint(g[1].Output) != "[image]" {
		t.Errorf("gemini %+v", g)
	}
	o, err := ListModelInfo(context.Background(), Provider{ID: "o", BaseURL: srv.URL + "/v1"})
	if err != nil || len(o) != 2 || fmt.Sprint(o[0].Efforts) != "[low high]" || o[1].Efforts != nil {
		t.Errorf("openai list %+v %v", o, err)
	}
}

func TestFromList(t *testing.T) {
	cfg := Config{Providers: []Provider{{ID: "g", API: "gemini", BaseURL: "https://x", Models: []Model{
		{ID: "gemini-4-neon"}, {ID: "gemini-4-argon", Efforts: []string{"high"}}, {ID: "mystery"}, {ID: "pic"},
	}}}}
	cfg.listed = map[string]map[string]ModelInfo{"g": {
		"gemini-4-neon":  {ID: "gemini-4-neon", Efforts: []string{"low", "medium", "high", "xhigh"}, DefaultEffort: "medium"},
		"gemini-4-argon": {ID: "gemini-4-argon", Efforts: []string{"low", "medium"}, DefaultEffort: "medium"},
		"mystery":        {ID: "mystery", Efforts: []string{"low", "high"}},
		"pic":            {ID: "pic", Output: []string{"text", "image"}},
	}}
	ms := map[string]resolved{}
	for _, m := range cfg.allModels() {
		ms[m.ID] = m
	}
	if n := ms["gemini-4-neon"]; fmt.Sprint(n.Efforts) != "[low medium high xhigh]" || n.DefaultEffort != "medium" {
		t.Errorf("neon %v / %q: the list should beat the id guess", n.Efforts, n.DefaultEffort)
	}
	if a := ms["gemini-4-argon"]; fmt.Sprint(a.Efforts) != "[high]" {
		t.Errorf("argon %v: the config should beat the list", a.Efforts)
	}
	if m := ms["mystery"]; m.Thinking != "gemini" || fmt.Sprint(m.Efforts) != "[low high]" {
		t.Errorf("mystery %+v: an id the guess doesn't know still gets the list's levels, sent as thinkingConfig", m.Model)
	}
	if ms["pic"].Kind != "image" {
		t.Error("output_modalities with image should make it an image model")
	}
}

// The first read starts the fetch and doesn't wait; a later read has the list.
func TestWithListedFetchesInBackground(t *testing.T) {
	release := make(chan struct{})
	calls := 0
	old := listFetch
	listFetch = func(ctx context.Context, p Provider) ([]ModelInfo, error) {
		calls++
		<-release
		return []ModelInfo{{ID: "m1", Efforts: []string{"low", "xhigh"}}}, nil
	}
	defer func() { listFetch = old }()
	cfg := Config{Providers: []Provider{{ID: "bg-test", BaseURL: "https://bg.example", Models: []Model{{ID: "m1"}}}}}
	if got := cfg.WithListed().listed["bg-test"]; got != nil {
		t.Fatalf("first read waited for the list: %v", got)
	}
	cfg.WithListed() // a second read while fetching starts no second fetch
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ms := cfg.WithListed().models(); len(ms) == 1 && slices.Equal(ms[0].Efforts, []string{"low", "xhigh"}) {
			if calls != 1 {
				t.Errorf("%d fetches, want 1", calls)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the fetched list never showed up")
}
