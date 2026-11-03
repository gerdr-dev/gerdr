package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/peer"
)

// Carrying a conversation on on another computer (docs/dev/MULTIHOST.md, "Moving a conversation"): POST /api/sessions
// with continueFrom on one computer and host (or none, for this one) naming another. Neither hub can read the other's
// log, so this one, which reaches both, copies it: the source's record and events, and the images they show (blobs
// are named by their content hash, so a copied one keeps its URL), into a new session there (hub.History). The
// source stays as it is, like a handoff between agents.

var blobRef = regexp.MustCompile(`"/api/blobs/([0-9a-f]{64})"`)

// moveTarget: the computer a create is for ("" = here) and the one its continueFrom is on. ok: they differ.
func moveTarget(body []byte) (to, from string, ok bool) {
	var x struct {
		Host         string `json:"host"`
		ContinueFrom string `json:"continueFrom"`
	}
	if json.Unmarshal(body, &x) != nil || x.ContinueFrom == "" {
		return "", "", false
	}
	h, _ := peer.Split(x.ContinueFrom)
	return x.Host, h, h != x.Host
}

func (s *Server) moveConversation(w http.ResponseWriter, r *http.Request, body []byte) {
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	to, _ := m["host"].(string)
	src, _ := m["continueFrom"].(string)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	rec, evs, err := s.readConversation(ctx, src)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if err := s.copyBlobs(ctx, src, to, evs); err != nil {
		httpError(w, 502, fmt.Sprintf(i18n.S("图片没搬过去：%v", "Couldn't copy the images: %v"), err))
		return
	}
	fromHost, _ := peer.Split(src)
	from := s.computerName(fromHost)
	var o struct {
		Cwd  string      `json:"cwd"`
		Open *createOpen `json:"open"`
	}
	_ = json.Unmarshal(body, &o)
	delete(m, "continueFrom")
	delete(m, "host")
	delete(m, "open")
	m["history"] = map[string]any{"record": rec, "events": evs, "from": from}
	if m["agent"] == nil || m["agent"] == "" {
		m["agent"] = rec.Agent // same agent by default: it's a move
	}
	if m["cwd"] == nil || m["cwd"] == "" {
		httpError(w, 400, i18n.S("要给那台电脑上的目录", "Needs a folder on that computer"))
		return
	}
	nb, _ := json.Marshal(m)
	var id, cwd string
	var out json.RawMessage
	if to == "" {
		var co hub.CreateOptions
		if err := json.Unmarshal(nb, &co); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		sess, err := s.Hub.Create(ctx, co)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		sum := sess.Summary()
		id, cwd = sum.ID, sum.Cwd
		out, _ = json.Marshal(sum)
	} else {
		resp, err := s.Peers.Do(ctx, to, "POST", "/api/sessions", bytes.NewReader(nb), http.Header{"Content-Type": {"application/json"}})
		if err != nil {
			httpError(w, 502, i18n.S("连不上那台电脑：", "Can't reach that computer: ")+err.Error())
			return
		}
		defer resp.Body.Close()
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if resp.StatusCode != 200 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(resp.StatusCode)
			_, _ = w.Write(rb)
			return
		}
		h, _ := s.Peers.Host(to)
		out = s.Peers.RewriteSummary(h, rb)
		s.Peers.NoteSession(to, out)
		var x struct{ ID, Cwd string }
		_ = json.Unmarshal(out, &x)
		id, cwd = x.ID, x.Cwd
	}
	if err := s.placeCreated(o.Open, id, o.Cwd, cwd); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	writeJSON(w, out)
}

// readConversation: a session's record and events, as its own computer has them (ids and URLs not rewritten).
func (s *Server) readConversation(ctx context.Context, id string) (hub.Record, []*hub.Event, error) {
	host, rest := peer.Split(id)
	if host == "" {
		sess, err := s.Hub.Session(id)
		if err != nil {
			return hub.Record{}, nil, err
		}
		sum := sess.Summary()
		if sum.Turn != nil {
			return hub.Record{}, nil, errRunning
		}
		return sum.Record, sess.Events(), nil
	}
	if s.Peers == nil {
		return hub.Record{}, nil, peer.ErrNoHost
	}
	var sum hub.Summary
	if err := s.hostGetJSON(ctx, host, "/api/sessions/"+rest, &sum); err != nil {
		return hub.Record{}, nil, err
	}
	if sum.Turn != nil || sum.Status == hub.StRunning {
		return hub.Record{}, nil, errRunning
	}
	var evs []*hub.Event
	if err := s.hostGetJSON(ctx, host, "/api/sessions/"+rest+"/events", &evs); err != nil {
		return hub.Record{}, nil, err
	}
	return sum.Record, evs, nil
}

var errRunning = errors.New(i18n.S("这个会话还在跑：先停下这一轮，再换到别的电脑接着聊", "This session is still running: stop the current turn before moving it to another computer"))

func (s *Server) hostGetJSON(ctx context.Context, host, path string, v any) error {
	resp, err := s.Peers.Do(ctx, host, "GET", path, nil, http.Header{})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return json.Unmarshal(b, v)
}

// copyBlobs puts every image the events refer to on the target computer, read from the source one.
func (s *Server) copyBlobs(ctx context.Context, src, to string, evs []*hub.Event) error {
	b, _ := json.Marshal(evs)
	from, _ := peer.Split(src)
	seen := map[string]bool{}
	for _, m := range blobRef.FindAllSubmatch(b, -1) {
		hash := string(m[1])
		if seen[hash] {
			continue
		}
		seen[hash] = true
		data, err := s.readBlob(ctx, from, hash)
		if err != nil {
			continue // an image already gone at the source: the copy shows it missing too
		}
		if to == "" {
			s.Hub.SaveBlob(base64.StdEncoding.EncodeToString(data))
			continue
		}
		body, _ := json.Marshal(map[string]string{"data": base64.StdEncoding.EncodeToString(data)})
		resp, err := s.Peers.Do(ctx, to, "POST", "/api/blobs", bytes.NewReader(body), http.Header{"Content-Type": {"application/json"}})
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return errors.New(resp.Status)
		}
	}
	return nil
}

func (s *Server) readBlob(ctx context.Context, host, hash string) ([]byte, error) {
	if host == "" {
		p := s.Hub.BlobPath(hash)
		if p == "" {
			return nil, os.ErrNotExist
		}
		return os.ReadFile(p)
	}
	resp, err := s.Peers.Do(ctx, host, "GET", "/api/blobs/"+hash, nil, http.Header{})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New(resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 40<<20))
}

// computerName: how a computer is called in the notice of a moved conversation.
func (s *Server) computerName(host string) string {
	if host == "" {
		if s.Peers != nil {
			return s.Peers.Self()
		}
		n, _ := os.Hostname()
		return strings.TrimSuffix(n, ".local")
	}
	if h, ok := s.Peers.Host(host); ok && h.Name != "" {
		return h.Name
	}
	return host
}
