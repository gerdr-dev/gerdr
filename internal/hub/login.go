package hub

import (
	"errors"

	"github.com/gerdr-dev/gerdr/agents/claude"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Signing an agent in again from the page (claude/login.go), when a turn failed on an expired or missing login
// (Summary.Login). One sign-in per agent at a time: its sessions share one account. Once it succeeds, the sessions
// that failed for it drop the error, so the page's "sign in" bar goes away and the person resends.

func (h *Hub) loginRun(agent string) *claude.LoginRun {
	h.loginMu.Lock()
	defer h.loginMu.Unlock()
	return h.logins[agent]
}

// StartLogin starts signing in this session's agent, or returns the sign-in already running for it.
func (s *Session) StartLogin() (claude.LoginState, error) {
	h, agent := s.h, s.rec.Agent
	if h.Base(agent) != "claude" {
		return claude.LoginState{}, errors.New(i18n.S("这个 agent 不能在 Gerdr 里登录，在终端里登录", "This agent can't be signed in from Gerdr; sign in in a terminal"))
	}
	def, ok := h.agentDef(agent)
	if !ok {
		return claude.LoginState{}, errors.New("unknown agent " + agent)
	}
	h.loginMu.Lock()
	defer h.loginMu.Unlock()
	if l := h.logins[agent]; l != nil && l.State().State == "running" {
		return l.State(), nil
	}
	l, err := claude.StartLogin(claude.Options{Bin: def.Bin, Env: def.envList()}, func(st claude.LoginState) {
		h.log.Info("agent sign-in ended", "agent", agent, "state", st.State, "err", st.Error)
		if st.State == "done" {
			h.loggedIn(agent)
		}
	})
	if err != nil {
		return claude.LoginState{}, err
	}
	if h.logins == nil {
		h.logins = map[string]*claude.LoginRun{}
	}
	h.logins[agent] = l
	return l.State(), nil
}

// LoginState: the latest sign-in of this session's agent; State "" = none started.
func (s *Session) LoginState() claude.LoginState {
	if l := s.h.loginRun(s.rec.Agent); l != nil {
		return l.State()
	}
	return claude.LoginState{}
}

func (s *Session) LoginCode(code string) error {
	l := s.h.loginRun(s.rec.Agent)
	if l == nil {
		return errors.New(i18n.S("还没开始登录", "No sign-in is running"))
	}
	return l.Code(code)
}

func (s *Session) CancelLogin() {
	if l := s.h.loginRun(s.rec.Agent); l != nil {
		l.Cancel()
	}
}

// loggedIn: the agent is signed in again; sessions whose last turn failed on its login are back to idle.
func (h *Hub) loggedIn(agent string) {
	h.mu.Lock()
	var ss []*Session
	for _, s := range h.sessions {
		if s.rec.Agent == agent {
			ss = append(ss, s)
		}
	}
	h.mu.Unlock()
	for _, s := range ss {
		s.mu.Lock()
		failed := s.lastErr != "" && s.lastLogin != ""
		if failed {
			s.lastErr, s.lastLogin = "", ""
		}
		s.mu.Unlock()
		if failed {
			s.setStatus()
			s.h.broadcastSession(s) // setStatus only broadcasts a status change; the Login field changed regardless
		}
	}
}
