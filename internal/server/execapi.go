package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/peer"
	"github.com/gerdr-dev/gerdr/internal/term"
)

// POST /api/exec: run one command line on this computer and answer with its output and exit code (hub.RunCommand).
// The console reaches a host's through the ?host= route (`gerdr exec --host`): an agent here runs commands there
// directly instead of asking an agent there to. It gives a paired console nothing it didn't have: it can already open
// a terminal there (termOnHost) and type into it. The request's context is the command's: the caller going away
// (Ctrl-C on the CLI, the console's request cancelled) stops it.
func (s *Server) execRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/exec", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Command string `json:"command"`
			Cwd     string `json:"cwd"`
			Stdin   string `json:"stdin"`
			Timeout int64  `json:"timeout"` // ms
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		res, err := hub.RunCommand(r.Context(), b.Command, b.Cwd, b.Stdin, time.Duration(b.Timeout)*time.Millisecond)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, res)
	})
	// A terminal's recent output as text (`gerdr pane read` of a terminal pane on another computer asks its host here)
	mux.HandleFunc("GET /api/terms/{id}/text", func(w http.ResponseWriter, r *http.Request) {
		t := s.Terms.Get(r.PathValue("id"))
		if t == nil {
			httpError(w, 404, term.ErrNotFound.Error())
			return
		}
		lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
		writeJSON(w, termText(t, lines))
	})
}

func termText(t *term.Term, lines int) map[string]any {
	cwd := t.LiveCwd()
	if cwd == "" {
		cwd = t.Info().Cwd
	}
	return map[string]any{"cwd": cwd, "exited": t.Info().Exited, "text": t.Text(lines)}
}

// paneTerm serves pane.read / pane.send once the layout found the terminal pane: on this computer, or on the host
// its id names ("<host>:<id>", termOnHost).
func (s *Server) paneTerm(ctx context.Context, method string, pane node, p LayoutParams) (any, error) {
	id := str(pane, "term_id")
	host, hid := "", id
	if s.Peers != nil {
		if h, x := peer.Split(id); h != "" {
			host, hid = h, x
		}
	}
	// The layout lock is held meanwhile: a host that doesn't answer mustn't hold every layout change up for long
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	lines := p.Lines
	if lines == 0 {
		lines = 100
	}
	if method == "pane.read" {
		var out map[string]any
		if host == "" {
			t := s.Terms.Get(id)
			if t == nil {
				return nil, term.ErrNotFound
			}
			out = termText(t, lines)
		} else if err := s.hostJSON(ctx, host, "GET", "/api/terms/"+url.PathEscape(hid)+"/text?lines="+strconv.Itoa(lines), nil, &out); err != nil {
			return nil, err
		}
		out["pane_id"] = pane["pane_id"]
		if host != "" {
			out["host"] = host
		}
		return out, nil
	}
	// pane.send: typed in as is, Enter added unless asked not to
	data := p.Text
	if !p.NoEnter {
		data += "\r"
	}
	if host == "" {
		t := s.Terms.Get(id)
		if t == nil {
			return nil, term.ErrNotFound
		}
		if err := t.Write([]byte(data)); err != nil {
			return nil, err
		}
	} else if err := s.hostJSON(ctx, host, "POST", "/api/terms/"+url.PathEscape(hid)+"/input", map[string]any{"data": data}, nil); err != nil {
		return nil, err
	}
	return node{"ok": true, "pane_id": pane["pane_id"]}, nil
}

// hostJSON: one JSON call to a host, its error message passed on.
func (s *Server) hostJSON(ctx context.Context, host, method, path string, body, out any) error {
	var rd io.Reader
	hdr := http.Header{}
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
		hdr.Set("Content-Type", "application/json")
	}
	resp, err := s.Peers.Do(ctx, host, method, path, rd, hdr)
	if err != nil {
		return fmt.Errorf("%s%w", i18n.S("连不上那台电脑：", "Can't reach that computer: "), err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode == 404 && !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		// The mux's own plain "404 page not found": a route that Gerdr doesn't have yet
		return fmt.Errorf(i18n.S("%s 上的 Gerdr 太旧，没有这个功能：让它升级", "the Gerdr on %s is too old for this: update it"), host)
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s: %s", host, e.Error)
		}
		return fmt.Errorf("%s: HTTP %d %s", host, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}
