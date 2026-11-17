package chromeext

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// tabLog keeps what a window's page did, from the DevTools events the extension forwards (Runtime / Log / Network /
// Page are enabled when the window opens): console messages and uncaught errors, network requests, and the
// JavaScript dialog that is open now (while one is, the page's JavaScript is paused, so evaluating anything hangs).
type tabLog struct {
	mu      sync.Mutex
	console []ConsoleEntry
	net     map[string]*NetEntry
	order   []string // request ids, oldest first
	dialog  *Dialog
}

const (
	maxConsole = 1000
	maxNet     = 1000
)

type ConsoleEntry struct {
	At    int64  `json:"at"`
	Level string `json:"level"` // log, info, warning, error, debug, exception
	Text  string `json:"text"`
	URL   string `json:"url,omitempty"`
	Line  int    `json:"line,omitempty"`
}

type NetEntry struct {
	ID       string  `json:"id"`
	At       int64   `json:"at"`
	Method   string  `json:"method"`
	URL      string  `json:"url"`
	Type     string  `json:"type"` // Document, XHR, Fetch, Script, Image…
	Status   int     `json:"status,omitempty"`
	MIME     string  `json:"mime,omitempty"`
	Size     float64 `json:"size,omitempty"`
	Failed   string  `json:"failed,omitempty"`
	Finished bool    `json:"finished"`
}

type Dialog struct {
	Type    string `json:"type"` // alert, confirm, prompt, beforeunload
	Message string `json:"message"`
	Default string `json:"default,omitempty"`
}

func newTabLog() *tabLog { return &tabLog{net: map[string]*NetEntry{}} }

func (l *tabLog) record(method string, params json.RawMessage) {
	switch method {
	case "Runtime.consoleAPICalled":
		var p struct {
			Type string `json:"type"`
			Args []struct {
				Type        string          `json:"type"`
				Value       json.RawMessage `json:"value"`
				Description string          `json:"description"`
			} `json:"args"`
			StackTrace *struct {
				CallFrames []struct {
					URL  string `json:"url"`
					Line int    `json:"lineNumber"`
				} `json:"callFrames"`
			} `json:"stackTrace"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		parts := make([]string, 0, len(p.Args))
		for _, a := range p.Args {
			switch {
			case a.Type == "string":
				var s string
				json.Unmarshal(a.Value, &s)
				parts = append(parts, s)
			case len(a.Value) > 0:
				parts = append(parts, string(a.Value))
			default:
				parts = append(parts, a.Description)
			}
		}
		e := ConsoleEntry{At: time.Now().UnixMilli(), Level: p.Type, Text: strings.Join(parts, " ")}
		if p.StackTrace != nil && len(p.StackTrace.CallFrames) > 0 {
			e.URL, e.Line = p.StackTrace.CallFrames[0].URL, p.StackTrace.CallFrames[0].Line+1
		}
		l.addConsole(e)
	case "Runtime.exceptionThrown":
		var p struct {
			Details struct {
				Text      string `json:"text"`
				URL       string `json:"url"`
				Line      int    `json:"lineNumber"`
				Exception *struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		text := p.Details.Text
		if p.Details.Exception != nil && p.Details.Exception.Description != "" {
			text = p.Details.Exception.Description
		}
		l.addConsole(ConsoleEntry{At: time.Now().UnixMilli(), Level: "exception", Text: text, URL: p.Details.URL, Line: p.Details.Line + 1})
	case "Log.entryAdded":
		var p struct {
			Entry struct {
				Level string `json:"level"`
				Text  string `json:"text"`
				URL   string `json:"url"`
				Line  int    `json:"lineNumber"`
			} `json:"entry"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		l.addConsole(ConsoleEntry{At: time.Now().UnixMilli(), Level: p.Entry.Level, Text: p.Entry.Text, URL: p.Entry.URL, Line: p.Entry.Line})
	case "Network.requestWillBeSent":
		var p struct {
			ID      string `json:"requestId"`
			Type    string `json:"type"`
			Request struct {
				Method string `json:"method"`
				URL    string `json:"url"`
			} `json:"request"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		l.mu.Lock()
		if _, ok := l.net[p.ID]; !ok {
			l.order = append(l.order, p.ID)
		}
		// a redirect reuses the id: the entry becomes the new URL
		l.net[p.ID] = &NetEntry{ID: p.ID, At: time.Now().UnixMilli(), Method: p.Request.Method, URL: p.Request.URL, Type: p.Type}
		for len(l.order) > maxNet {
			delete(l.net, l.order[0])
			l.order = l.order[1:]
		}
		l.mu.Unlock()
	case "Network.responseReceived":
		var p struct {
			ID       string `json:"requestId"`
			Type     string `json:"type"`
			Response struct {
				Status   int    `json:"status"`
				MimeType string `json:"mimeType"`
			} `json:"response"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		l.mu.Lock()
		if e := l.net[p.ID]; e != nil {
			e.Status, e.MIME = p.Response.Status, p.Response.MimeType
			if p.Type != "" {
				e.Type = p.Type
			}
		}
		l.mu.Unlock()
	case "Network.loadingFinished":
		var p struct {
			ID   string  `json:"requestId"`
			Size float64 `json:"encodedDataLength"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		l.mu.Lock()
		if e := l.net[p.ID]; e != nil {
			e.Finished, e.Size = true, p.Size
		}
		l.mu.Unlock()
	case "Network.loadingFailed":
		var p struct {
			ID    string `json:"requestId"`
			Error string `json:"errorText"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		l.mu.Lock()
		if e := l.net[p.ID]; e != nil {
			e.Finished, e.Failed = true, p.Error
		}
		l.mu.Unlock()
	case "Page.javascriptDialogOpening":
		var p struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Default string `json:"defaultPrompt"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		l.mu.Lock()
		l.dialog = &Dialog{Type: p.Type, Message: p.Message, Default: p.Default}
		l.mu.Unlock()
	case "Page.javascriptDialogClosed":
		l.mu.Lock()
		l.dialog = nil
		l.mu.Unlock()
	}
}

func (l *tabLog) addConsole(e ConsoleEntry) {
	if len(e.Text) > 4000 {
		e.Text = e.Text[:4000] + "…"
	}
	l.mu.Lock()
	l.console = append(l.console, e)
	if len(l.console) > maxConsole {
		l.console = l.console[len(l.console)-maxConsole:]
	}
	l.mu.Unlock()
}

func (l *tabLog) openDialog() *Dialog {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dialog
}

// consoleText lists console entries (errors only: level error / exception / warning-as-error), filtered by text.
func (l *tabLog) consoleText(errorsOnly bool, filter string, limit int, clear bool) string {
	l.mu.Lock()
	list := append([]ConsoleEntry(nil), l.console...)
	if clear {
		l.console = nil
	}
	l.mu.Unlock()
	var out []string
	for _, e := range list {
		if errorsOnly && e.Level != "error" && e.Level != "exception" {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(e.Text+" "+e.URL), strings.ToLower(filter)) {
			continue
		}
		line := fmt.Sprintf("[%s] %s", e.Level, e.Text)
		if e.URL != "" {
			line += fmt.Sprintf("  (%s:%d)", e.URL, e.Line)
		}
		out = append(out, line)
	}
	if limit > 0 && len(out) > limit {
		out = append([]string{fmt.Sprintf("… %d earlier", len(out)-limit)}, out[len(out)-limit:]...)
	}
	if len(out) == 0 {
		return "(no console messages)"
	}
	return strings.Join(out, "\n")
}

// netText lists requests, filtered by URL substring and type.
func (l *tabLog) netText(filter, typ string, limit int, clear bool) string {
	l.mu.Lock()
	list := make([]NetEntry, 0, len(l.order))
	for _, id := range l.order {
		list = append(list, *l.net[id])
	}
	if clear {
		l.net, l.order = map[string]*NetEntry{}, nil
	}
	l.mu.Unlock()
	var out []string
	for _, e := range list {
		if filter != "" && !strings.Contains(strings.ToLower(e.URL), strings.ToLower(filter)) {
			continue
		}
		if typ != "" && !strings.EqualFold(typ, e.Type) {
			continue
		}
		status := "…"
		switch {
		case e.Failed != "":
			status = "FAILED " + e.Failed
		case e.Status != 0:
			status = fmt.Sprint(e.Status)
		}
		u := e.URL
		if len(u) > 300 {
			u = u[:300] + "…"
		}
		out = append(out, fmt.Sprintf("%s  %s %s  %s  %s %s", e.ID, e.Method, status, e.Type, u, e.MIME))
	}
	if limit > 0 && len(out) > limit {
		out = append([]string{fmt.Sprintf("… %d earlier", len(out)-limit)}, out[len(out)-limit:]...)
	}
	if len(out) == 0 {
		return "(no requests)"
	}
	return strings.Join(out, "\n")
}
