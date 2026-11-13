package work

import (
	"errors"
	"fmt"
	"math"
)

// A panel's badge on its rail button: a red dot or a number saying "something new here" (a board's tasks moved, new
// messages). Unlike the inbox (inbox.go) it asks nothing of the user and notifies nobody: it only marks the button,
// and goes once the user looks at the panel. Sources are the panel's own functions (ctx.badge) and anyone with the
// CLI (`gerdr panel badge`), so a schedule or trigger that notices a change can say so without a page open.

// Badges is where panels' badges go (the server's). n: >0 a number, 0 a plain dot, <0 none. add: n is added to what
// the badge shows now (a dot counts as 0) instead of replacing it.
type Badges interface {
	SetBadge(panel string, n int, add bool) error
}

// BadgeMax: shown as "99+" past it; also keeps a runaway add() from overflowing.
const BadgeMax = 999

func (r *runner) badgeObject() map[string]any {
	call := func(name string, n int, add bool) {
		if r.r.Badges == nil {
			r.throw(errors.New("ctx.badge: not available here (needs Gerdr's server)"))
		}
		if err := r.r.Badges.SetBadge(r.project, n, add); err != nil {
			r.throw(fmt.Errorf("ctx.badge.%s: %w", name, err))
		}
	}
	return map[string]any{
		// ctx.badge.set(n | true | null): a number, a dot, or none (false / 0 / null / undefined clear it too)
		"set": func(arg any) {
			switch v := plain(toPlain(arg)).(type) {
			case nil:
				call("set", -1, false)
			case bool:
				if v {
					call("set", 0, false)
				} else {
					call("set", -1, false)
				}
			default:
				f, ok := toFloat(v)
				if !ok || math.IsNaN(f) {
					r.throw(errors.New("ctx.badge.set takes a number, true (a dot) or null (none)"))
				}
				n := int(f)
				if n <= 0 {
					n = -1
				}
				call("set", min(n, BadgeMax), false)
			}
		},
		// ctx.badge.add(n = 1): count n more things on top of what it shows (e.g. one per change seen)
		"add": func(arg any) {
			n := 1
			if v := plain(toPlain(arg)); v != nil {
				f, ok := toFloat(v)
				if !ok || math.IsNaN(f) || f < 0 {
					r.throw(errors.New("ctx.badge.add takes a number ≥ 0"))
				}
				n = int(f)
			}
			call("add", min(n, BadgeMax), true)
		},
		// ctx.badge.clear(): none
		"clear": func() { call("clear", -1, false) },
	}
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	}
	return 0, false
}
