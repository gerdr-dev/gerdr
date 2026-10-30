package acme

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const doAPI = "https://api.digitalocean.com"

// digitaloceanCAA: DigitalOcean takes CAA as separate flags / tag / data fields, record names relative to the domain.
func digitaloceanCAA(ctx context.Context, api, host, value string, apply bool) (bool, error) {
	tok := credEnv("DO_AUTH_TOKEN")
	if tok == "" {
		return false, errors.New("没找到 DigitalOcean 的 API Token")
	}
	if u := credEnv("DO_API_URL"); u != "" {
		api = u
	}
	call := func(method, path string, body, out any) error {
		var rd io.Reader = http.NoBody
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		r, err := http.NewRequestWithContext(ctx, method, api+path, rd)
		if err != nil {
			return err
		}
		r.Header.Set("Authorization", "Bearer "+tok)
		r.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			return fmt.Errorf("连不上 DigitalOcean：%w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			var e struct{ Message string }
			_ = json.NewDecoder(resp.Body).Decode(&e)
			return fmt.Errorf("DigitalOcean 拒绝了（HTTP %d）：%s", resp.StatusCode, e.Message)
		}
		if out != nil {
			return json.NewDecoder(resp.Body).Decode(out)
		}
		return nil
	}

	var zones []string
	for page := 1; ; page++ {
		var r struct {
			Domains []struct{ Name string } `json:"domains"`
			Links   struct {
				Pages struct{ Next string } `json:"pages"`
			} `json:"links"`
		}
		if err := call("GET", fmt.Sprintf("/v2/domains?per_page=200&page=%d", page), nil, &r); err != nil {
			return false, err
		}
		for _, d := range r.Domains {
			zones = append(zones, d.Name)
		}
		if r.Links.Pages.Next == "" {
			break
		}
	}
	zone, rr, err := pickZone(host, zones)
	if err != nil {
		return false, err
	}

	var r struct {
		Records []struct {
			Tag  string `json:"tag"`
			Data string `json:"data"`
		} `json:"domain_records"`
	}
	q := url.Values{"type": {"CAA"}, "name": {host}, "per_page": {"200"}}
	if err := call("GET", "/v2/domains/"+url.PathEscape(zone)+"/records?"+q.Encode(), nil, &r); err != nil {
		return false, err
	}
	for _, rec := range r.Records {
		if rec.Tag == "issue" && sameCAA(rec.Data, value) {
			return false, nil
		}
	}
	if !apply {
		return true, nil
	}
	rec := map[string]any{"type": "CAA", "name": rr, "flags": 0, "tag": "issue", "data": value, "ttl": 1800}
	if err := call("POST", "/v2/domains/"+url.PathEscape(zone)+"/records", rec, nil); err != nil {
		return false, err
	}
	return true, nil
}
