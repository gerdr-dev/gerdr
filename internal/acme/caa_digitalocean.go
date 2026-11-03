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

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

const doAPI = "https://api.digitalocean.com"

// digitaloceanRecord: DigitalOcean takes CAA as separate flags / tag / data fields, record names relative to the domain.
func digitaloceanRecord(ctx context.Context, api, host string, want rec, apply bool) (bool, error) {
	tok := credEnv("DO_AUTH_TOKEN")
	if tok == "" {
		return false, errors.New(i18n.S("没找到 DigitalOcean 的 API Token", "no DigitalOcean API token found"))
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
			return fmt.Errorf(i18n.S("连不上 DigitalOcean：%w", "can't reach DigitalOcean: %w"), err)
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			var e struct{ Message string }
			_ = json.NewDecoder(resp.Body).Decode(&e)
			return fmt.Errorf(i18n.S("DigitalOcean 拒绝了（HTTP %d）：%s", "DigitalOcean refused (HTTP %d): %s"), resp.StatusCode, e.Message)
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
			ID   int64  `json:"id"`
			Tag  string `json:"tag"`
			Data string `json:"data"`
		} `json:"domain_records"`
	}
	q := url.Values{"type": {want.Type}, "name": {host}, "per_page": {"200"}}
	base := "/v2/domains/" + url.PathEscape(zone) + "/records"
	if err := call("GET", base+"?"+q.Encode(), nil, &r); err != nil {
		return false, err
	}
	var have []held
	for _, rec := range r.Records {
		if want.Type != "CAA" || rec.Tag == "issue" {
			have = append(have, held{fmt.Sprint(rec.ID), rec.Data})
		}
	}
	ok, others := want.judge(have)
	if ok && len(others) == 0 {
		return false, nil
	}
	if !apply {
		return true, nil
	}
	if !ok {
		body := map[string]any{"type": want.Type, "name": rr, "data": want.Value, "ttl": 1800}
		if want.Type == "CAA" {
			body["flags"], body["tag"] = 0, "issue"
		}
		if err := call("POST", base, body, nil); err != nil {
			return false, err
		}
	}
	for _, id := range others {
		if err := call("DELETE", base+"/"+id, nil, nil); err != nil {
			return false, err
		}
	}
	return true, nil
}
