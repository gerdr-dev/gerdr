package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// A panel's part of Gerdr's inbox: the things on it that need the user (a board's task asking something, a step only
// they can do). Gerdr's own inbox only knew sessions' approvals and questions, so a board's "等你" never reached
// it, nor the phone's notifications. A panel says its whole current list each time (ctx.inbox.set): an item that is
// no longer in it is done, so a panel never has to remember to take one back.

// InboxItem: one thing a panel says needs the user.
type InboxItem struct {
	Panel string `json:"panel"` // the panel's key, set by Gerdr
	ID    string `json:"id"`    // stable per thing: the same id across calls is the same item (no second notification)
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	// The conversation it's about: opening the item shows it (in the panel, when the panel started it)
	Session string `json:"session,omitempty"`
	At      int64  `json:"at,omitempty"` // since when, unix ms; Gerdr sets it the first time it sees the item
}

// Inbox is where panels' items go (the server's).
type Inbox interface {
	SetInbox(panel string, items []InboxItem) error
}

const inboxMax = 50 // per panel: a runaway loop can't flood the inbox

func (r *runner) inboxObject() map[string]any {
	return map[string]any{
		// ctx.inbox.set([{ id, title, body?, session? }]): this panel's items now; [] clears them
		"set": func(arg any) {
			if r.r.Inbox == nil {
				r.throw(errors.New("ctx.inbox: not available here (needs Gerdr's server)"))
			}
			b, err := json.Marshal(plain(toPlain(arg)))
			if err != nil {
				r.throw(err)
			}
			var items []InboxItem
			if err := json.Unmarshal(b, &items); err != nil {
				r.throw(fmt.Errorf("ctx.inbox.set takes a list of { id, title, body?, session? }: %w", err))
			}
			seen := map[string]bool{}
			out := items[:0]
			for _, it := range items {
				it.ID, it.Title = strings.TrimSpace(it.ID), strings.TrimSpace(it.Title)
				if it.ID == "" || it.Title == "" {
					r.throw(errors.New("ctx.inbox.set: every item needs an id and a title"))
				}
				if seen[it.ID] {
					continue
				}
				seen[it.ID] = true
				it.Panel = r.project
				out = append(out, it)
			}
			if len(out) > inboxMax {
				out = out[:inboxMax]
			}
			if err := r.r.Inbox.SetInbox(r.project, out); err != nil {
				r.throw(fmt.Errorf("ctx.inbox.set: %w", err))
			}
		},
	}
}
