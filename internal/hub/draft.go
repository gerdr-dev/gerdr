package hub

import "encoding/json"

// Draft is a session's unsent composer content: text, where the caret is, and the attached images. It lives here
// rather than only in each browser so that the phone and the computer see the same composer: typing on one shows up
// on the other as you go.
//
// Memory only: a draft is a few seconds-to-minutes thing, and the browser keeps its own copy anyway. Last writer wins
// on the whole text, no merging; two people typing into the same box at the same moment is not the case this is for.
// The client sorts out the races (its own echo, a stale write overtaking a newer local one): see ui/src/lib/sync.ts.
type Draft struct {
	Text string `json:"text"`
	// Sel is the selection [start, end] in Text: whoever typed or moved the caret last puts it there on every device,
	// so picking up the phone continues where the computer left off.
	Sel []int `json:"sel,omitempty"`
	// Imgs are the attachments as references only ({mimeType, blob} for an image already stored with SaveBlob, {path}
	// for an uploaded video): the bytes never go through the websocket. Opaque here; the UI owns the shape.
	Imgs json.RawMessage `json:"imgs,omitempty"`
	From string          `json:"from"`           // client id of the browser tab that wrote it, so that tab can tell its own echo
	Init bool            `json:"init,omitempty"` // pushed because the tab just started watching the session, not a fresh edit
}

// maxDraft caps what one tab can park in memory per session (the websocket read limit is 1 MiB anyway).
const maxDraft = 256 << 10

// SetDraft records a session's draft and pushes it to every tab watching that session, the writer included (it
// counts its echoes to know when nothing of its own is in flight any more).
func (h *Hub) SetDraft(id string, d Draft) {
	if len(d.Text)+len(d.Imgs) > maxDraft {
		return
	}
	d.Init = false
	if string(d.Imgs) == "null" || string(d.Imgs) == "[]" {
		d.Imgs = nil
	}
	h.mu.Lock()
	if h.sessions[id] == nil {
		h.mu.Unlock()
		return
	}
	if d.Text == "" && d.Imgs == nil {
		delete(h.drafts, id)
	} else {
		h.drafts[id] = d
	}
	h.mu.Unlock()
	m := Msg{T: "draft", ID: id, Data: d}
	for _, s := range h.subscribers() {
		s.mu.Lock()
		w := s.ids[id]
		s.mu.Unlock()
		if w {
			s.send(m)
		}
	}
}

// draftFor is the current draft to hand a tab that just started watching the session (nil when there is none).
func (h *Hub) draftFor(id string) *Msg {
	h.mu.Lock()
	d, ok := h.drafts[id]
	h.mu.Unlock()
	if !ok {
		return nil
	}
	d.Init = true
	return &Msg{T: "draft", ID: id, Data: d}
}
