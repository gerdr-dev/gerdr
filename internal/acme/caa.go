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
	"os"
	"strings"
	"time"
	"unicode"
)

// CAA writes the record that pins issuance for a name to this machine's ACME account, through the same DNS
// provider credentials the certificate is already signed with — so the settings page can offer one button
// instead of asking the user to copy a record into their DNS console.
//
// lego only knows TXT records, so each provider needs its own small client (caa_*.go), built from the same
// SDKs lego already pulls in and reading the same credentials. A provider without a writer would only lose the
// button (the page still shows the record to copy), but TestCAAEveryProvider asks for one.

// caaWriter reports whether host lacks the record; with apply it also adds it. It must leave other CAA records
// on host alone: they may belong to another machine issuing for the same name.
type caaWriter func(ctx context.Context, host, value string, apply bool) (missing bool, err error)

var caaWriters = map[string]caaWriter{
	"cloudflare": func(ctx context.Context, host, value string, apply bool) (bool, error) {
		return cloudflareCAA(ctx, cfAPI, host, value, apply)
	},
	"alidns":       alidnsCAA,
	"tencentcloud": tencentCAA,
	"route53":      route53CAA,
	"digitalocean": func(ctx context.Context, host, value string, apply bool) (bool, error) {
		return digitaloceanCAA(ctx, doAPI, host, value, apply)
	},
	"huaweicloud": huaweiCAA,
}

// CAAValue is the record's value (tag "issue", flags 0).
func CAAValue(accountURI string) string {
	return "letsencrypt.org; accounturi=" + accountURI + "; validationmethods=dns-01"
}

// caaRecord is the `flags tag "value"` text form most provider APIs take.
func caaRecord(value string) string { return `0 issue "` + value + `"` }

// sameCAA tells whether a record read back from a provider is ours. Providers differ in quoting and spacing
// (and some hand back only the value part), so compare with both stripped.
func sameCAA(got, value string) bool {
	norm := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == '"' || r == '\\' || unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s)
	}
	g := norm(got)
	return g == norm(caaRecord(value)) || g == norm(value)
}

// pickZone finds the zone host lives in among the ones the credentials can see (longest suffix wins, so a
// delegated sub-zone beats its parent) and the record name relative to it ("@" at the apex).
func pickZone(host string, zones []string) (zone, rr string, err error) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, z := range zones {
		z = strings.ToLower(strings.TrimSuffix(z, "."))
		if (host == z || strings.HasSuffix(host, "."+z)) && len(z) > len(zone) {
			zone = z
		}
	}
	if zone == "" {
		return "", "", fmt.Errorf("这份凭据下找不到 %s 所在的域名（zone），确认它托管在这个账号下、凭据能读到它", host)
	}
	if rr = strings.TrimSuffix(strings.TrimSuffix(host, zone), "."); rr == "" {
		rr = "@"
	}
	return zone, rr, nil
}

// CAASupported reports whether SetCAA can check and write the record for this provider.
func CAASupported(dns string) bool { return caaWriters[dns] != nil }

// SetCAA reports whether host lacks the CAA record; with apply it adds it (missing=true then means "added now").
func SetCAA(ctx context.Context, dns, host, accountURI string, apply bool) (missing bool, err error) {
	if accountURI == "" {
		return false, errors.New("还没签过证书，没有 ACME 账户可写进 CAA")
	}
	w := caaWriters[dns]
	if w == nil {
		return false, fmt.Errorf("%s 还不支持自动加 CAA，照上面那条手动加", dns)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return w(ctx, strings.TrimSuffix(host, "."), CAAValue(accountURI), apply)
}

// credEnv reads a credential the way exportEnv does: the GERDR_-prefixed name wins over the bare one;
// several names cover lego's aliases.
func credEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(envPrefix + n); v != "" {
			return v
		}
	}
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

const cfAPI = "https://api.cloudflare.com/client/v4"

func cloudflareCAA(ctx context.Context, api, host, value string, apply bool) (bool, error) {
	// Same credential choices lego's cloudflare provider accepts. A separate zone token (Zone:Read) is
	// what people use when the DNS token is scoped too tightly to list zones.
	auth := func(r *http.Request, zone bool) error {
		tok := credEnv("CLOUDFLARE_DNS_API_TOKEN", "CF_DNS_API_TOKEN")
		if zone {
			if z := credEnv("CLOUDFLARE_ZONE_API_TOKEN", "CF_ZONE_API_TOKEN"); z != "" {
				tok = z
			}
		}
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
			return nil
		}
		email, key := credEnv("CLOUDFLARE_EMAIL", "CF_API_EMAIL"), credEnv("CLOUDFLARE_API_KEY", "CF_API_KEY")
		if email == "" || key == "" {
			return errors.New("没找到 Cloudflare 的 API Token")
		}
		r.Header.Set("X-Auth-Email", email)
		r.Header.Set("X-Auth-Key", key)
		return nil
	}
	call := func(method, path string, body, out any, zone bool) error {
		var rd io.Reader = http.NoBody
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		r, err := http.NewRequestWithContext(ctx, method, api+path, rd)
		if err != nil {
			return err
		}
		if err := auth(r, zone); err != nil {
			return err
		}
		r.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			return fmt.Errorf("连不上 Cloudflare：%w", err)
		}
		defer resp.Body.Close()
		var env struct {
			Success bool `json:"success"`
			Errors  []struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"errors"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			return fmt.Errorf("Cloudflare 返回了看不懂的东西（HTTP %d）", resp.StatusCode)
		}
		if !env.Success {
			var msgs []string
			for _, e := range env.Errors {
				msgs = append(msgs, fmt.Sprintf("%d %s", e.Code, e.Message))
			}
			return fmt.Errorf("Cloudflare 拒绝了（HTTP %d）：%s", resp.StatusCode, strings.Join(msgs, "; "))
		}
		if out != nil {
			return json.Unmarshal(env.Result, out)
		}
		return nil
	}

	// Find the zone by walking up the labels: gerdr.example.co.uk → example.co.uk → co.uk.
	var zoneID string
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	for i := 0; i < len(labels)-1 && zoneID == ""; i++ {
		var zones []struct {
			ID string `json:"id"`
		}
		if err := call("GET", "/zones?name="+url.QueryEscape(strings.Join(labels[i:], ".")), nil, &zones, true); err != nil {
			return false, err
		}
		if len(zones) > 0 {
			zoneID = zones[0].ID
		}
	}
	if zoneID == "" {
		return false, fmt.Errorf("这个 Cloudflare 凭据下找不到 %s 所在的 zone（token 要能读这个 zone）", host)
	}

	type caaData struct {
		Flags int    `json:"flags"`
		Tag   string `json:"tag"`
		Value string `json:"value"`
	}
	var recs []struct {
		Data caaData `json:"data"`
	}
	if err := call("GET", "/zones/"+zoneID+"/dns_records?type=CAA&name="+url.QueryEscape(host), nil, &recs, false); err != nil {
		return false, err
	}
	for _, r := range recs {
		if r.Data.Tag == "issue" && sameCAA(r.Data.Value, value) {
			return false, nil
		}
	}
	if !apply {
		return true, nil
	}
	rec := map[string]any{"type": "CAA", "name": host, "ttl": 1, "data": caaData{Flags: 0, Tag: "issue", Value: value}}
	if err := call("POST", "/zones/"+zoneID+"/dns_records", rec, nil, false); err != nil {
		return false, err
	}
	return true, nil
}
