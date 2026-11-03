package server

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Reports (`gerdr session new --report-to`, ctx.sessions.start({ reportTo })): a session with Record.ReportTo tells
// that session each time it stops — its turn ended, it's waiting for a person, or it failed — as a message, so a
// coordinator (the Work agent) can hand work out and go back to talking with the user instead of blocking on
// `--wait` or polling.
//
// Reports wait while the target is busy (running, or waiting for a person: a message then would answer its question)
// and go as one message once it's free, so a burst of finished workers costs the coordinator one turn, not one each.
// Turns a person drove by typing in the worker's chat aren't reported (Summary.ByPerson): they saw the answer.
// Pending reports live in memory only: a restart in between drops them (the coordinator can still `session list`).

// reportDelay gathers the reports that arrive together (several workers finishing within a second or two) into one.
const reportDelay = 2 * time.Second

// reportReplyMax bounds how much of a worker's reply goes into a report: the coordinator reads the rest if it needs to.
const reportReplyMax = 1500

type report struct {
	from string // the session that stopped
	kind string // done | waiting | error
}

type reporter struct {
	s *Server
	// state: the target's status, false when it's gone; deliver sends it the message. Hub-backed (newReporter);
	// tests swap them for fakes.
	state   func(id string) (string, bool)
	deliver func(target, text, from string) error
	delay   time.Duration
	mu      sync.Mutex
	last   map[string]string   // session → last status seen
	queue  map[string][]report // target → reports not delivered yet
	timers map[string]*time.Timer
}

// RunReports watches session status changes for reports until ctx ends.
func (s *Server) RunReports(ctx context.Context) {
	if s.Hub == nil {
		return
	}
	go newReporter(s).loop(ctx)
}

func newReporter(s *Server) *reporter {
	rp := &reporter{s: s, delay: reportDelay, last: map[string]string{}, queue: map[string][]report{}, timers: map[string]*time.Timer{}}
	rp.state = func(id string) (string, bool) {
		sess, err := s.Hub.Session(id)
		if err != nil {
			return "", false
		}
		return sess.Summary().Status, true
	}
	rp.deliver = func(target, text, from string) error {
		sess, err := s.Hub.Session(target)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, err = sess.SendFrom(ctx, []acp.ContentBlock{acp.TextBlock(text)}, "", from, false)
		return err
	}
	return rp
}

func (rp *reporter) loop(ctx context.Context) {
	for ctx.Err() == nil {
		sub := rp.s.Hub.Subscribe()
		// (Re)seed without reporting: a change missed while the subscription was cut can't be dated, and reporting
		// every session's current state would wake every coordinator for nothing
		rp.mu.Lock()
		for _, x := range rp.s.Hub.List() {
			rp.last[x.ID] = x.Status
		}
		rp.mu.Unlock()
		func() {
			for {
				select {
				case <-ctx.Done():
					return
				case m, ok := <-sub.C():
					if !ok {
						return // dropped for being slow (Sub.send): subscribe again
					}
					switch m.T {
					case "session":
						if m.Session != nil {
							rp.onSession(*m.Session)
						}
					case "session_removed":
						rp.mu.Lock()
						delete(rp.last, m.ID)
						rp.mu.Unlock()
					}
				}
			}
		}()
		rp.s.Hub.Unsubscribe(sub)
		if ctx.Err() == nil {
			time.Sleep(time.Second)
		}
	}
}

// reportKind: what a status change means to a coordinator, "" for nothing worth telling.
func reportKind(old, cur string) string {
	if old == cur || old == "" {
		return "" // unchanged, or the first time we see it (just created, or gerdr just started)
	}
	switch cur {
	case hub.StWaiting:
		return "waiting"
	case hub.StError:
		return "error"
	case hub.StIdle:
		if old == hub.StRunning || old == hub.StWaiting || old == hub.StStarting {
			return "done"
		}
	}
	return ""
}

func (rp *reporter) onSession(sum hub.Summary) {
	rp.mu.Lock()
	old := rp.last[sum.ID]
	rp.last[sum.ID] = sum.Status
	// A turn a person drove (they talked to the worker in its chat) isn't reported: they read the answer there, and
	// the coordinator would only repeat it back to them (reported: a whole extra turn echoing a reply already seen).
	if kind := reportKind(old, sum.Status); kind != "" && sum.ReportTo != "" && !sum.ByPerson {
		rp.queue[sum.ReportTo] = append(rp.queue[sum.ReportTo], report{from: sum.ID, kind: kind})
		rp.scheduleLocked(sum.ReportTo)
	}
	// A busy target just became free: deliver what waited for it
	if old != sum.Status && (sum.Status == hub.StIdle || sum.Status == hub.StError) && len(rp.queue[sum.ID]) > 0 {
		rp.scheduleLocked(sum.ID)
	}
	rp.mu.Unlock()
}

func (rp *reporter) scheduleLocked(target string) {
	if rp.timers[target] != nil {
		return
	}
	rp.timers[target] = time.AfterFunc(rp.delay, func() { rp.flush(target) })
}

func (rp *reporter) flush(target string) {
	rp.mu.Lock()
	delete(rp.timers, target)
	reps := rp.queue[target]
	rp.mu.Unlock()
	if len(reps) == 0 {
		return
	}
	st, ok := rp.state(target)
	if !ok {
		rp.mu.Lock()
		delete(rp.queue, target) // the target was deleted: nobody to tell
		rp.mu.Unlock()
		return
	}
	switch st {
	case hub.StRunning, hub.StWaiting, hub.StStarting:
		return // kept: onSession schedules again when it's free
	}
	rp.mu.Lock()
	reps = rp.queue[target] // what came in meanwhile goes too
	delete(rp.queue, target)
	rp.mu.Unlock()
	text, from := rp.compose(reps)
	if text == "" {
		return
	}
	if err := rp.deliver(target, text, from); err != nil && rp.s.Log != nil {
		rp.s.Log.Warn("report: send", "to", target, "err", err)
	}
}

// compose writes one message for a batch of reports: per session only its latest state (it may have finished, then
// been sent more and finished again before the target was free), oldest first.
func (rp *reporter) compose(reps []report) (text, from string) {
	latest := map[string]int{}
	for i, r := range reps {
		latest[r.from] = i
	}
	var order []int
	for _, i := range latest {
		order = append(order, i)
	}
	sort.Ints(order)
	var b strings.Builder
	b.WriteString(i18n.S("[Gerdr 回报] 你派出去的会话有动静了：", "[Gerdr report] Sessions you handed work to have news:"))
	n := 0
	for _, i := range order {
		r := reps[i]
		sess, err := rp.s.Hub.Session(r.from)
		if err != nil {
			continue
		}
		if from == "" {
			from = r.from
		}
		n++
		writeReport(&b, sess.Summary(), r.kind, lastReply(sess.Events()))
	}
	if n == 0 {
		return "", ""
	}
	return b.String(), from
}

func writeReport(b *strings.Builder, sum hub.Summary, kind, reply string) {
	name := sum.Title
	if name == "" {
		name = sum.ID
	}
	var state string
	switch kind {
	case "done":
		state = i18n.S("这一轮做完了", "finished its turn")
	case "waiting":
		state = i18n.S("在等人处理", "is waiting for a person")
	case "error":
		state = i18n.S("出错了", "failed")
	}
	fmt.Fprintf(b, "\n\n### %s (%s) — %s\n", name, sum.ID, state)
	if len(sum.Tags) > 0 {
		keys := make([]string, 0, len(sum.Tags))
		for k := range sum.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + "=" + sum.Tags[k]
		}
		fmt.Fprintf(b, i18n.S("标签：%s\n", "Tags: %s\n"), strings.Join(parts, " "))
	}
	switch kind {
	case "waiting":
		fmt.Fprintf(b, i18n.S("它在等：%s\n只有人能处理这一步：告诉用户去 Gerdr 界面上看，别替人决定。\n", "It's waiting on: %s\nOnly a person can handle this: tell the user to look in Gerdr, don't decide for them.\n"), sum.Waiting)
	case "error":
		if sum.Error != "" {
			fmt.Fprintf(b, i18n.S("错误：%s\n", "Error: %s\n"), sum.Error)
		}
	}
	if reply = strings.TrimSpace(reply); reply != "" {
		if r := []rune(reply); len(r) > reportReplyMax {
			reply = string(r[:reportReplyMax]) + i18n.S("…（后面省略了）", "… (cut)")
		}
		b.WriteString(reply)
		b.WriteString("\n")
	}
	fmt.Fprintf(b, i18n.S("（全文：gerdr session read %s）", "(full text: gerdr session read %s)"), sum.ID)
}
