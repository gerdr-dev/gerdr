package addons

// Routes: an add-on's service serving agents' model API traffic itself, for an address it names.
//
// Asked for (2026-11) by a relay with a private protocol: its add-on keeps a local proxy that uploads only what is
// new in each request (a big conversation is megabytes resent every turn, slow from far away), and the protocol is
// the relay's own, so it has no place in Gerdr. What Gerdr does is the general half: while a service with
// "routes": true reports {"type":"routes","routes":[{"from":"https://relay.example","to":"http://127.0.0.1:7861"}]},
// an agent whose API address starts with from is started with to in its place (the rest of the address kept). The
// service knows where to forward, it named from. Only agents pointed at from are touched: an agent signed in to
// Anthropic itself, or on another relay, never goes through it.
//
// Lifetime is the run's: a restart, a crash, switching the add-on off or a "routes" line without the route takes it
// away, and sessions whose process was pointed at it restart before their next turn (each agent compares what
// Rewrite answers for it with what its process was started with) and talk to the API directly again. So a stopped service costs one restarted process, not a broken agent.
//
// to must be plain http on the loopback: an add-on reached over the network would be a different thing (agents'
// keys sent across it) that nobody asked for.

import (
	"net"
	"net/url"
	"slices"
	"sort"
	"strings"
)

// Route is one address a service serves for agents.
type Route struct {
	From string `json:"from"` // the API address agents are configured with (http(s), no query)
	To   string `json:"to"`   // where the service listens (http://127.0.0.1:<port> or localhost / [::1])
	// Agents: only these agents (ids as `gerdr session agents` lists them: claude, codex, a role's id) are routed;
	// empty = every agent on From. Lets the person pick in the add-on's settings (g.agents() lists them).
	Agents []string `json:"agents,omitempty"`
}

// cleanRoutes keeps the valid routes (trailing slashes dropped) and returns the refused ones for the log.
func cleanRoutes(in []Route) (ok []Route, bad []Route) {
	for _, r := range in {
		from, to := strings.TrimRight(strings.TrimSpace(r.From), "/"), strings.TrimRight(strings.TrimSpace(r.To), "/")
		if !validFrom(from) || !loopbackHTTP(to) {
			bad = append(bad, r)
			continue
		}
		var agents []string
		for _, a := range r.Agents {
			if a = strings.TrimSpace(a); a != "" {
				agents = append(agents, a)
			}
		}
		ok = append(ok, Route{From: from, To: to, Agents: agents})
	}
	return ok, bad
}

func validFrom(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.RawQuery == "" && u.Fragment == ""
}

func loopbackHTTP(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "http" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	h := u.Hostname()
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// routes lists what running services serve now, longest from first (the most specific wins).
func (s *Services) routes() []Route {
	s.mu.Lock()
	var out []Route
	for _, p := range s.procs {
		if p.st.State == "running" {
			out = append(out, p.st.Routes...)
		}
	}
	s.mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].From) != len(out[j].From) {
			return len(out[i].From) > len(out[j].From)
		}
		return out[i].From+out[i].To < out[j].From+out[j].To
	})
	return out
}

// Rewrite is the address agent, configured with base, should use instead, or "" to keep base. base matches a route
// when it is its from or continues it with a path (scheme and host compared case-insensitively), and the route names
// agent or no agent at all.
func (s *Services) Rewrite(agent, base string) string {
	if s == nil {
		return ""
	}
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	if b == "" {
		return ""
	}
	for _, r := range s.routes() {
		if len(r.Agents) > 0 && !slices.Contains(r.Agents, agent) {
			continue
		}
		if rest, ok := cutBase(b, r.From); ok {
			return r.To + rest
		}
	}
	return ""
}

// cutBase reports whether base is from or from followed by a path, and returns that path.
func cutBase(base, from string) (string, bool) {
	if len(base) < len(from) || !strings.EqualFold(base[:len(from)], from) {
		return "", false
	}
	rest := base[len(from):]
	if rest != "" && rest[0] != '/' {
		return "", false // https://relay.example.com is not https://relay.example
	}
	return rest, true
}
