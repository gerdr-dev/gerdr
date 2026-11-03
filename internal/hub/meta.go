package hub

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Tags and ReportTo: labels and a "tell this session when I stop" link that coordinators put on sessions (a Work
// agent dispatching workers, a script). Gerdr attaches no meaning to the tags; ReportTo is acted on by server/report.go.

// maxTags / maxTagLen keep a runaway script from turning the session index into a database.
const (
	maxTags   = 32
	maxTagLen = 256
)

// checkTag: keys are short words (no spaces, no '=' so `k=v` on the command line splits one way).
func checkTag(k, v string) error {
	if k == "" || strings.ContainsAny(k, "= \t\n") {
		return fmt.Errorf(i18n.S("标签名 %q 不对：不能为空，不能有空格或 =", "bad tag name %q: not empty, no spaces or '='"), k)
	}
	if len(k) > maxTagLen || len(v) > maxTagLen {
		return fmt.Errorf(i18n.S("标签 %q 太长（最多 %d 字节）", "tag %q is too long (at most %d bytes)"), k, maxTagLen)
	}
	return nil
}

// cleanTags validates a set of tags for a new session; "" values are dropped.
func cleanTags(in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for k, v := range in {
		if err := checkTag(k, v); err != nil {
			return nil, err
		}
		if v != "" {
			out[k] = v
		}
	}
	if len(out) > maxTags {
		return nil, fmt.Errorf(i18n.S("标签太多（最多 %d 个）", "too many tags (at most %d)"), maxTags)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// SetTags changes tags: a "" value removes that key, others are set; keys not named stay as they are.
func (s *Session) SetTags(change map[string]string) error {
	for k, v := range change {
		if err := checkTag(k, v); err != nil {
			return err
		}
	}
	s.mu.Lock()
	tags := map[string]string{}
	for k, v := range s.rec.Tags {
		tags[k] = v
	}
	for k, v := range change {
		if v == "" {
			delete(tags, k)
		} else {
			tags[k] = v
		}
	}
	if len(tags) > maxTags {
		s.mu.Unlock()
		return fmt.Errorf(i18n.S("标签太多（最多 %d 个）", "too many tags (at most %d)"), maxTags)
	}
	if len(tags) == 0 {
		tags = nil
	}
	s.rec.Tags = tags
	s.mu.Unlock()
	s.h.saveIndex()
	s.h.broadcastSession(s)
	return nil
}

// SetReportTo points this session's reports at another session ("" stops them).
func (s *Session) SetReportTo(to string) error {
	if to != "" {
		if err := s.h.checkReportTo(s.rec.ID, to); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.rec.ReportTo = to
	s.mu.Unlock()
	s.h.saveIndex()
	s.h.broadcastSession(s)
	return nil
}

// checkReportTo refuses a target that doesn't exist, the session itself, or one whose own reports lead back here:
// A reporting to B and B to A would wake each other forever (a report starts a turn, whose end is reported back).
func (h *Hub) checkReportTo(from, to string) error {
	if to == from {
		return errors.New(i18n.S("会话不能回报给自己", "a session can't report to itself"))
	}
	t, err := h.Session(to)
	if err != nil {
		return err
	}
	seen := map[string]bool{from: true}
	for cur := t; cur != nil; {
		id := cur.ID()
		if seen[id] {
			return errors.New(i18n.S("回报会绕回自己（对方也在回报给这个会话）", "reports would go round in a loop (the target reports back to this session)"))
		}
		seen[id] = true
		cur.mu.Lock()
		next := cur.rec.ReportTo
		cur.mu.Unlock()
		if next == "" {
			break
		}
		cur, _ = h.Session(next)
	}
	return nil
}

// HasTags: every key in want is set on tags, and to that value unless the wanted value is "" (then any value does).
func HasTags(tags, want map[string]string) bool {
	for k, v := range want {
		got, ok := tags[k]
		if !ok || (v != "" && got != v) {
			return false
		}
	}
	return true
}
