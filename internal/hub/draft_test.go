package hub

import "testing"

// A draft reaches only the tabs watching that session, writer included; a tab that starts watching later gets the
// current one marked init; an empty draft is forgotten.
func TestDraftSync(t *testing.T) {
	h, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s := newSession(h, Record{ID: "s1"})
	h.mu.Lock()
	h.sessions["s1"] = s
	h.mu.Unlock()
	drain := func(sub *Sub) (out []Msg) {
		for {
			select {
			case m := <-sub.C():
				if m.T == "draft" {
					out = append(out, m)
				}
			default:
				return
			}
		}
	}

	a, b, other := h.Subscribe(), h.Subscribe(), h.Subscribe()
	h.Watch(a, []string{"s1"}, nil)
	h.Watch(b, []string{"s1"}, nil)
	drain(a)
	drain(b)

	h.SetDraft("s1", Draft{Text: "hello", From: "tabA"})
	h.SetDraft("nope", Draft{Text: "x", From: "tabA"}) // unknown session: ignored
	for _, sub := range []*Sub{a, b} {
		got := drain(sub)
		if len(got) != 1 || got[0].Data.(Draft).Text != "hello" || got[0].Data.(Draft).From != "tabA" || got[0].Data.(Draft).Init {
			t.Fatalf("watcher got %+v", got)
		}
	}
	if got := drain(other); len(got) != 0 {
		t.Fatalf("non-watcher got %+v", got)
	}

	h.Watch(other, []string{"s1"}, nil)
	if got := drain(other); len(got) != 1 || got[0].Data.(Draft).Text != "hello" || !got[0].Data.(Draft).Init {
		t.Fatalf("late watcher got %+v", got)
	}

	// Images alone keep the draft alive; nothing at all clears it
	h.SetDraft("s1", Draft{From: "tabB", Imgs: []byte(`[{"blob":"/api/blobs/x"}]`)})
	d := h.Subscribe()
	h.Watch(d, []string{"s1"}, nil)
	if got := drain(d); len(got) != 1 || string(got[0].Data.(Draft).Imgs) == "" {
		t.Fatalf("image-only draft not kept: %+v", got)
	}
	h.SetDraft("s1", Draft{From: "tabB", Imgs: []byte("[]")})
	c := h.Subscribe()
	h.Watch(c, []string{"s1"}, nil)
	if got := drain(c); len(got) != 0 {
		t.Fatalf("cleared draft still pushed: %+v", got)
	}
}
