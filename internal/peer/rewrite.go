package peer

import (
	"bytes"
	"encoding/json"
)

// What crosses between a host and the console carries session ids and URLs that only mean something on the host.
// These rewrite them (docs/dev/MULTIHOST.md, "Session ids"). Payloads are handled as generic JSON on purpose: the
// console may run another Gerdr version than the host, and a field it doesn't know must pass through untouched.

// HostRef is put on every host session's summary, so the UI can badge it and the new-session picker knows where it is.
type HostRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RewriteSummary: a host's session summary as the console shows it. nil if it doesn't parse.
func RewriteSummary(h Host, raw json.RawMessage) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil
	}
	for _, k := range []string{"id", "reportTo"} {
		if s, ok := m[k].(string); ok && s != "" {
			m[k] = Join(h.ID, s)
		}
	}
	// The slash-command list is fetched by key (/api/commands/<key>): the prefix routes that fetch to the host
	if s, ok := m["commandsKey"].(string); ok && s != "" {
		m["commandsKey"] = Join(h.ID, s)
	}
	if cl, ok := m["cleared"].([]any); ok {
		for _, c := range cl {
			if cm, ok := c.(map[string]any); ok {
				if s, ok := cm["id"].(string); ok && s != "" {
					cm["id"] = Join(h.ID, s)
				}
			}
		}
	}
	m["host"] = HostRef{ID: h.ID, Name: h.Name}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return RewriteURLs(h.ID, b)
}

var blobURL = []byte(`"/api/blobs/`)

// RewriteURLs points the host's own URLs in a payload (images in the log: /api/blobs/<hash>) at the console's proxy
// for that host, /h/<id>/api/blobs/<hash>.
func RewriteURLs(hostID string, b []byte) []byte {
	if !bytes.Contains(b, blobURL) {
		return b
	}
	return bytes.ReplaceAll(b, blobURL, []byte(`"/h/`+hostID+`/api/blobs/`))
}

// UnrewriteURLs: the reverse, for what the console sends to a host (a draft's image refs).
func UnrewriteURLs(hostID string, b []byte) []byte {
	return bytes.ReplaceAll(b, []byte(`"/h/`+hostID+`/api/blobs/`), blobURL)
}

// idKeys: top-level fields of request bodies that name a session (send's from, open's near, meta's reportTo…).
var idKeys = []string{"from", "near", "reportTo", "continueFrom", "session"}

// RewriteBody: a request body on its way to a host. Ids of that host lose their prefix; ids of anything else (a
// console session, another host) can't mean anything there and are dropped. Not an object = passed as is.
func RewriteBody(hostID string, b []byte) []byte {
	if len(bytes.TrimSpace(b)) == 0 {
		return b
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil || m == nil {
		return b
	}
	for _, k := range idKeys {
		s, ok := m[k].(string)
		if !ok || s == "" {
			continue
		}
		if h, id := Split(s); h == hostID {
			m[k] = id
		} else {
			delete(m, k)
		}
	}
	delete(m, "host")
	out, err := json.Marshal(m)
	if err != nil {
		return b
	}
	return UnrewriteURLs(hostID, out)
}
