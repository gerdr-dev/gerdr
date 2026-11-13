// Package waits is waiting for a condition on the state of something outside (docs/dev/EVENTS.md, "Waiting for a
// state"): a source (an add-on's service) reports what a thing is like now — an MR's state, its pipeline, its review
// comments — and a session or a panel waits until a condition on it holds:
//
//	gerdr wait gitlab gitlab.example.com/group/repo!34 --until "state != 'opened' || pipeline == 'failed' || reviews > 2"
//
// Why not events: an event is said once, when it happens; whoever subscribes late, or misses it, never hears it,
// and every fix (dedup, replay, "final") is a patch on that. A condition is checked against the state as it is,
// any number of times with the same answer, so it can't be missed: waiting late on something already true wakes at
// once. The expectation is always the waiter's own (decided with the user, 2026-11-10).
package waits

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"
)

// Cond is a condition: one JavaScript expression over a state. Each of the state's fields is a variable, and `$`
// is the whole state (for a field whose name isn't a valid identifier):
//
//	state != 'opened' || pipeline == 'failed' || reviews > 2
//	notes.some(n => n.author != me && n.resolvable && !n.resolved && n.id > 1233511)
//
// JavaScript rather than a little language of Gerdr's own (it was one for a day): filtering a list — "an unresolved
// comment by someone else on this file" — needs a real expression, and agents write JS without being taught it.
// The expression sees nothing but the state: no require, no files, no network (a bare moejs runtime), and it gets
// 50ms per check.
type Cond struct {
	src  string
	prog *moejs.Script
}

// Timeout is how long one check may take.
const Timeout = 50 * time.Millisecond

var identRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// Parse compiles a condition (a syntax error is caught here, a wrong field name only when it is checked).
func Parse(src string) (*Cond, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil, errors.New("empty condition")
	}
	if len(src) > 4000 {
		return nil, errors.New("condition over 4000 characters")
	}
	// An expression, not statements: wrapped so `a; b` or a return can't sneak in
	prog, err := moejs.CompileScript("until", "'use strict';(function(){ return (\n"+src+"\n); })()")
	if err != nil {
		return nil, fmt.Errorf("condition: %v", cleanErr(err))
	}
	return &Cond{src: src, prog: prog}, nil
}

func (c *Cond) String() string { return c.src }

// Eval: does the condition hold for this state (its value, as JavaScript reads it as true)? A field the state
// doesn't have is an error (a ReferenceError), not false: a typo must not mean "never".
func (c *Cond) Eval(st map[string]any) (hit bool, err error) {
	rt := moejs.NewRuntime(moejs.Options{DisableDynamicCode: true})
	for k, v := range st {
		if identRe.MatchString(k) {
			if err := rt.SetGlobal(k, v); err != nil {
				return false, fmt.Errorf("condition: field %s: %v", k, err)
			}
		}
	}
	if err := rt.SetGlobal("$", st); err != nil {
		return false, fmt.Errorf("condition: %v", err)
	}
	t := time.AfterFunc(Timeout, func() { rt.Interrupt("took over 50ms") })
	defer t.Stop()
	v, err := rt.RunScript(c.prog)
	if err != nil {
		return false, fmt.Errorf("condition: %v", cleanErr(err))
	}
	return engine.ToBoolean(v), nil
}

// cleanErr: the message alone — a thrown error's, without the stack moejs appends (whose lines point into the
// wrapper), or the interrupt's reason.
func cleanErr(err error) string {
	var ex *moejs.Exception
	var ie *moejs.InterruptedError
	switch {
	case errors.As(err, &ie):
		return fmt.Sprint(ie.Value)
	case errors.As(err, &ex) && ex.Name() != "":
		return ex.Name() + ": " + ex.Message()
	}
	msg := err.Error()
	if i := strings.Index(msg, " at "); i > 0 {
		msg = msg[:i]
	}
	return strings.TrimSpace(msg)
}
