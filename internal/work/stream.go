package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Streams: a long connection a panel keeps open to a service of its own (a WebSocket), held by Gerdr's backend — the
// one process here that stays up. A panel's functions run once and end, so without this a panel could only poll: a
// team board pulled its server every minute while the server had a WebSocket saying "something changed" the moment it
// did (2026-11-06). Declared like schedules, one file per connection, the file name is its id:
//
//	streams/team.json   { "connect": "team.stream", "on": "team.tick", "name": "Team server" }
//
// connect is a local function returning where to connect, { url: "wss://…", headers?: {…} }, or null for "not now"
// (not signed in yet): called before every (re)connect, so a new token or address is picked up. on is a local function
// run for what comes, with { message, stream } (message: the JSON value, or the text when it isn't JSON). Messages that
// come while it runs collapse into one more run with the latest: a burst of "changed" is one catch-up, and nothing that
// came after a run started is missed. The connection is redialled with backoff when it drops; nothing is queued for
// when it's down, so `on` should catch up by itself (a "changed" stream is that already) and a schedule stays the
// backstop. Gerdr knows nothing of what flows over it: the panel's two functions do.

// StreamsDir is the Work directory's folder of stream files.
const StreamsDir = "streams"

const (
	streamIdle       = 30 * time.Second // connect said "not now": asked again after this
	streamBackoffMin = 2 * time.Second
	streamBackoffMax = time.Minute
	streamSilent     = 2 * time.Minute // nothing (not even a ping) for this long: the connection is dead
	streamCallLimit  = 30 * time.Second
)

// Stream is one streams/<id>.json.
type Stream struct {
	ID      string `json:"id"`
	Connect string `json:"connect"`
	On      string `json:"on"`
	Name    string `json:"name,omitempty"`
}

// StreamInfo is a stream with how it's doing, for `gerdr panel streams`.
type StreamInfo struct {
	Stream
	State    string     `json:"state"` // connecting | connected | idle (connect said not now) | retrying
	Since    *time.Time `json:"since,omitempty"`
	Error    string     `json:"error,omitempty"`
	Messages int        `json:"messages"`
	Runs     int        `json:"runs"`
	LastRun  *time.Time `json:"lastRun,omitempty"`
	RunError string     `json:"runError,omitempty"`
}

// ParseStreams reads a Work directory's streams/ (and user/streams/); a broken file is an error naming it.
func ParseStreams(dir string) ([]Stream, error) {
	files, err := overlayJSON(dir, StreamsDir)
	if err != nil {
		return nil, err
	}
	var out []Stream
	for _, id := range overlayIDs(files) {
		f := files[id]
		if !scheduleIDRe.MatchString(id) {
			return nil, fmt.Errorf(i18n.S("%s：文件名只能用字母、数字、. - _", "%s: the file name may only use letters, digits, . - _"), f.rel)
		}
		b, err := os.ReadFile(f.abs)
		if err != nil {
			return nil, err
		}
		var s Stream
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf(i18n.S("%s 格式不对：%w", "%s is malformed: %w"), f.rel, err)
		}
		s.ID = id
		for _, fn := range []struct{ name, v string }{{"connect", s.Connect}, {"on", s.On}} {
			file, name, ok := strings.Cut(fn.v, ".")
			if !ok || name == "" || !fileRE.MatchString(file) {
				return nil, fmt.Errorf(i18n.S("%s：%s 要写成 文件.函数（比如 team.stream）", "%s: %s must be file.function (e.g. team.stream)"), f.rel, fn.name)
			}
		}
		out = append(out, s)
	}
	return out, nil
}

type streamConn struct {
	def    Stream
	cancel context.CancelFunc
	info   StreamInfo
}

type streamer struct {
	mu    sync.Mutex
	sigs  map[string]string // project → overlaySig of its streams/
	defs  map[string][]Stream
	conns map[string]*streamConn // project + "\x00" + id
}

// RunStreams keeps every project's streams/ connected until ctx ends, following the folders as they change.
func (r *Runner) RunStreams(ctx context.Context) {
	go func() {
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}
		t := time.NewTicker(scheduleTick)
		defer t.Stop()
		for {
			r.syncStreams(ctx)
			select {
			case <-t.C:
			case <-ctx.Done():
				return
			}
		}
	}()
}

// syncStreams starts the streams declared now and stops the ones gone or changed.
func (r *Runner) syncStreams(ctx context.Context) {
	r.strm.mu.Lock()
	defer r.strm.mu.Unlock()
	if r.strm.conns == nil {
		r.strm.conns, r.strm.sigs, r.strm.defs = map[string]*streamConn{}, map[string]string{}, map[string][]Stream{}
	}
	want := map[string]Stream{}
	projects := map[string]string{}
	for _, project := range r.workProjects() {
		dir, err := r.Dir(project)
		if err != nil {
			continue
		}
		if sig := overlaySig(dir, StreamsDir); sig != r.strm.sigs[project] {
			r.strm.sigs[project] = sig
			defs, err := ParseStreams(dir)
			if err != nil && r.Log != nil {
				r.Log.Warn("work: streams not connected", "project", project, "err", err)
			}
			r.strm.defs[project] = defs
		}
		for _, s := range r.strm.defs[project] {
			k := project + "\x00" + s.ID
			want[k], projects[k] = s, project
		}
	}
	for k, c := range r.strm.conns {
		if s, ok := want[k]; !ok || s != c.def {
			c.cancel()
			delete(r.strm.conns, k)
		}
	}
	for k, s := range want {
		if r.strm.conns[k] != nil {
			continue
		}
		cctx, cancel := context.WithCancel(ctx)
		c := &streamConn{def: s, cancel: cancel, info: StreamInfo{Stream: s, State: "connecting"}}
		r.strm.conns[k] = c
		go r.keepStream(cctx, projects[k], c)
	}
}

func (r *Runner) streamSet(c *streamConn, f func(i *StreamInfo)) {
	r.strm.mu.Lock()
	f(&c.info)
	r.strm.mu.Unlock()
}

// keepStream: connect, read until it drops, again — until ctx ends.
func (r *Runner) keepStream(ctx context.Context, project string, c *streamConn) {
	wake := make(chan struct{}, 1)
	var mu sync.Mutex
	var latest any
	go r.streamRunner(ctx, project, c, wake, &mu, &latest)
	backoff := streamBackoffMin
	sleep := func(d time.Duration) bool {
		select {
		case <-time.After(d):
			return true
		case <-ctx.Done():
			return false
		}
	}
	for ctx.Err() == nil {
		url, hdr, err := r.streamTarget(ctx, project, c.def.Connect)
		if err != nil || url == "" {
			now := time.Now()
			r.streamSet(c, func(i *StreamInfo) {
				i.State, i.Since, i.Error = "idle", &now, ""
				if err != nil {
					i.State, i.Error = "retrying", err.Error()
				}
			})
			wait := streamIdle
			if err != nil {
				wait, backoff = backoff, min(backoff*2, streamBackoffMax)
			}
			if !sleep(wait) {
				return
			}
			continue
		}
		dctx, cancel := context.WithTimeout(ctx, streamCallLimit)
		conn, _, err := websocket.DefaultDialer.DialContext(dctx, url, hdr)
		cancel()
		if err != nil {
			now := time.Now()
			r.streamSet(c, func(i *StreamInfo) { i.State, i.Since, i.Error = "retrying", &now, err.Error() })
			wait := backoff
			backoff = min(backoff*2, streamBackoffMax)
			if !sleep(wait) {
				return
			}
			continue
		}
		backoff = streamBackoffMin
		now := time.Now()
		r.streamSet(c, func(i *StreamInfo) { i.State, i.Since, i.Error = "connected", &now, "" })
		err = r.readStream(ctx, conn, func(msg any) {
			mu.Lock()
			latest = msg
			mu.Unlock()
			r.streamSet(c, func(i *StreamInfo) { i.Messages++ })
			select {
			case wake <- struct{}{}:
			default: // a run is already due: it takes the latest
			}
		})
		if ctx.Err() != nil {
			return
		}
		now = time.Now()
		r.streamSet(c, func(i *StreamInfo) { i.State, i.Since, i.Error = "retrying", &now, errText(err) })
		if !sleep(backoff) {
			return
		}
		backoff = min(backoff*2, streamBackoffMax)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// readStream reads messages until the connection fails or ctx ends. A ping (answered by gorilla's default handler)
// or any message keeps it alive; silence past streamSilent drops it, to be redialled.
func (r *Runner) readStream(ctx context.Context, conn *websocket.Conn, got func(any)) error {
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	alive := func() { _ = conn.SetReadDeadline(time.Now().Add(streamSilent)) }
	alive()
	ping := conn.PingHandler()
	conn.SetPingHandler(func(s string) error { alive(); return ping(s) })
	for {
		_, b, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		alive()
		var v any
		if json.Unmarshal(b, &v) != nil {
			v = string(b)
		}
		got(v)
	}
}

// streamRunner runs `on`, one run at a time, each time something came; what came during a run is one more run.
func (r *Runner) streamRunner(ctx context.Context, project string, c *streamConn, wake <-chan struct{}, mu *sync.Mutex, latest *any) {
	for {
		select {
		case <-wake:
		case <-ctx.Done():
			return
		}
		mu.Lock()
		msg := *latest
		mu.Unlock()
		rn := r.Start(project, c.def.On, map[string]any{"message": msg, "stream": c.def.ID})
		_, err := rn.Wait(context.Background())
		now := time.Now()
		r.streamSet(c, func(i *StreamInfo) {
			i.Runs++
			i.LastRun, i.RunError = &now, errText(err)
		})
		if err != nil && r.Log != nil {
			r.Log.Warn("work: stream's on failed", "project", project, "id", c.def.ID, "err", err)
		}
	}
}

// streamTarget asks the panel where to connect: its connect function's { url, headers }, or "" for not now.
func (r *Runner) streamTarget(ctx context.Context, project, fn string) (string, http.Header, error) {
	cctx, cancel := context.WithTimeout(ctx, streamCallLimit)
	defer cancel()
	res, err := r.Start(project, fn, map[string]any{}).Wait(cctx)
	if err != nil {
		return "", nil, err
	}
	if res == nil {
		return "", nil, nil
	}
	b, _ := json.Marshal(res)
	var t struct {
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	if json.Unmarshal(b, &t) != nil {
		return "", nil, fmt.Errorf(i18n.S("%s 要返回 { url, headers? } 或 null", "%s must return { url, headers? } or null"), fn)
	}
	if t.URL == "" {
		return "", nil, nil
	}
	if !strings.HasPrefix(t.URL, "ws://") && !strings.HasPrefix(t.URL, "wss://") {
		return "", nil, errors.New(i18n.S("url 要是 ws:// 或 wss:// 开头", "url must start with ws:// or wss://"))
	}
	h := http.Header{}
	for k, v := range t.Headers {
		h.Set(k, v)
	}
	return t.URL, h, nil
}

// Streams lists a project's streams with how each is doing.
func (r *Runner) Streams(project string) ([]StreamInfo, error) {
	if project == "" {
		project = DefaultProject
	}
	dir, err := r.Dir(project)
	if err != nil {
		return nil, err
	}
	defs, err := ParseStreams(dir)
	if err != nil {
		return nil, err
	}
	r.strm.mu.Lock()
	defer r.strm.mu.Unlock()
	out := []StreamInfo{}
	for _, s := range defs {
		if c := r.strm.conns[project+"\x00"+s.ID]; c != nil {
			out = append(out, c.info)
		} else {
			out = append(out, StreamInfo{Stream: s, State: "connecting"})
		}
	}
	return out, nil
}
