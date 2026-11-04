package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// g.fetch: an add-on's HTTP request, made by the backend instead of the page.
//
// Why: add-on code runs in the page, whose origin is Gerdr's own, so a plain fetch() to someone else's API needs that
// API to send CORS headers — most don't, and the person writing the add-on often can't change them. The backend has
// no such rule. This grants nothing new: an add-on is already trusted code (it can run a service process), and the
// route sits behind the same auth as every other /api call.
//
//	POST /api/addons/{id}/fetch   {"url","method","headers","body"} → {"status","statusText","headers","body"}
//
// The body travels as text both ways (JSON / text APIs, which is what most add-ons call), unless the request says
// "binary": then the answer's body is base64 with "base64": true — audio from a text-to-speech API (g.tts), an image —
// since bytes that aren't UTF-8 don't survive a JSON string. A non-2xx answer is still a
// 200 here with its status inside, like fetch(), so the add-on can read the error body. A failure to reach the server
// at all is a 502 with the reason.

type addonFetchReq struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    *string           `json:"body"`
	Binary  bool              `json:"binary"`
}

type addonFetchResp struct {
	Status     int               `json:"status"`
	StatusText string            `json:"statusText"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	Base64     bool              `json:"base64,omitempty"`
}

const (
	addonFetchTimeout = 30 * time.Second
	addonFetchMaxBody = 10 << 20
)

// addonFetchClient: proxies from the environment like any other outgoing request; no cookie jar, so add-ons don't
// share sessions with each other.
var addonFetchClient = &http.Client{
	Timeout:   addonFetchTimeout,
	Transport: http.DefaultTransport,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

func doAddonFetch(ctx context.Context, b addonFetchReq) (*addonFetchResp, error) {
	u, err := url.Parse(b.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("url must be http(s): %q", b.URL)
	}
	method := strings.ToUpper(b.Method)
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if b.Body != nil {
		body = strings.NewReader(*b.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	for k, v := range b.Headers {
		req.Header.Set(k, v)
	}
	res, err := addonFetchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, addonFetchMaxBody+1))
	if err != nil {
		return nil, err
	}
	if len(data) > addonFetchMaxBody {
		return nil, fmt.Errorf("response larger than %d MB", addonFetchMaxBody>>20)
	}
	out := &addonFetchResp{
		Status:     res.StatusCode,
		StatusText: strings.TrimSpace(strings.TrimPrefix(res.Status, fmt.Sprint(res.StatusCode))),
		Headers:    map[string]string{},
		Body:       string(data),
	}
	if b.Binary {
		out.Body, out.Base64 = base64.StdEncoding.EncodeToString(data), true
	}
	for k := range res.Header {
		out.Headers[strings.ToLower(k)] = res.Header.Get(k)
	}
	return out, nil
}

func (s *Server) addonFetch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	found := false
	for _, a := range s.Addons.List() {
		if a.ID == id {
			found = true
			break
		}
	}
	if !found {
		httpError(w, 404, "no such add-on: "+id)
		return
	}
	var b addonFetchReq
	if err := decode(r, &b); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	res, err := doAddonFetch(r.Context(), b)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, res)
}
