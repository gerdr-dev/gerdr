package native

import (
	"context"
	"strings"
	"sync"
	"time"
)

// What providers' model lists say about their models (ModelInfo: thinking levels and the default one, output
// modalities), used where the config doesn't say: a model's own efforts / defaultEffort / kind in config.json come
// first, then the list, then the guess from the id. Lets a gateway that knows (subhub: the levels each upstream
// account really has) drive the effort picker, instead of Gerdr guessing per id and getting it wrong — gemini-4-neon
// was guessed low / medium / high while it takes xhigh too.
//
// Fetched in the background, never on the path of a turn: the first config read after start (or after an hour)
// starts the fetch and goes on without it; the reads after that have it. A failed fetch is tried again after a few
// minutes; lists without these fields change nothing. Options.ModelLists turns it on (gerdr does); off by default
// so embedders and tests don't get GETs to /models they didn't ask for.

const (
	listTTL   = time.Hour
	listRetry = 5 * time.Minute
)

type listEntry struct {
	models   map[string]ModelInfo
	at       time.Time // last successful fetch
	failedAt time.Time
	fetching bool
}

var modelLists = struct {
	sync.Mutex
	m map[string]*listEntry
}{m: map[string]*listEntry{}}

// listFetch is ListModelInfo; a var for tests.
var listFetch = ListModelInfo

// listKey: one list per provider endpoint (and key: two accounts on one gateway can see different levels).
func listKey(p Provider) string {
	return p.ID + "\x00" + p.API + "\x00" + strings.TrimRight(p.BaseURL, "/") + "\x00" + resolvedKey(p)
}

func resolvedKey(p Provider) string { return resolved{Provider: p}.endpoint().APIKey }

// WithListed is the config with what the providers' model lists say filled in (as far as it is known now), and a
// background fetch started for the lists that are missing or old.
func (c Config) WithListed() Config {
	info := map[string]map[string]ModelInfo{}
	for _, p := range c.Providers {
		if p.ID == "" || p.BaseURL == "" {
			continue
		}
		if ms := listedNow(p); len(ms) > 0 {
			info[p.ID] = ms
		}
	}
	c.listed = info
	return c
}

// listedNow returns the provider's list as known now, starting a fetch when there is none or it is old.
func listedNow(p Provider) map[string]ModelInfo {
	k := listKey(p)
	modelLists.Lock()
	defer modelLists.Unlock()
	e := modelLists.m[k]
	if e == nil {
		e = &listEntry{}
		modelLists.m[k] = e
	}
	if !e.fetching && time.Since(e.at) > listTTL && time.Since(e.failedAt) > listRetry {
		e.fetching = true
		go refreshList(k, p)
	}
	return e.models
}

func refreshList(k string, p Provider) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ms, err := listFetch(ctx, p)
	modelLists.Lock()
	defer modelLists.Unlock()
	e := modelLists.m[k]
	e.fetching = false
	if err != nil {
		e.failedAt = time.Now()
		return
	}
	m := map[string]ModelInfo{}
	for _, mi := range ms {
		m[mi.ID] = mi
	}
	e.models, e.at = m, time.Now()
}

// fromList fills what the config left empty on m from the provider's list. Runs before the id guess
// (withDefaults), which only fills what is still empty.
func (c Config) fromList(p Provider, m Model) Model {
	mi, ok := c.listed[p.ID][m.ID]
	if !ok {
		return m
	}
	if len(m.Efforts) == 0 && len(mi.Efforts) > 0 {
		m.Efforts = mi.Efforts
		if m.DefaultEffort == "" {
			m.DefaultEffort = mi.DefaultEffort
		}
		if m.Thinking == "" && p.API == "gemini" {
			m.Thinking = "gemini" // levels go out as thinkingConfig; the id guess would leave a model it doesn't know without
		}
	}
	if m.Kind == "" {
		for _, o := range mi.Output {
			if strings.EqualFold(o, "image") {
				m.Kind = "image"
			}
		}
	}
	return m
}
