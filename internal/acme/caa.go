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

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Two records on the relay host go through the same DNS provider credentials the certificate is already signed
// with, so the settings page can offer one button instead of asking the user to copy them into a DNS console:
//
//   - A: the host points at the relay VPS (phones connect to the name, the relay forwards it here by SNI). An A
//     record, not a CNAME to the relay's name: a CNAME can't sit beside the CAA below, and CAA lookups follow
//     the alias, so the relay's owner would decide who may issue for this name — the very thing CAA is there for.
//   - CAA: pins issuance for the name to this machine's ACME account.
//
// lego only knows TXT records, so each provider needs its own small client (caa_*.go), built from the same
// SDKs lego already pulls in and reading the same credentials. A provider without a writer would only lose the
// buttons (the page still shows the records to copy), but TestCAAEveryProvider asks for one.

// rec is one record to keep on the host.
type rec struct {
	Type  string // "A" or "CAA"
	Value string // A: the IP; CAA: the issue value (CAAValue)
}

// only: ours must be the only record of its type on the name — another address would send phones elsewhere.
// CAA is added beside the others instead: they may belong to another machine issuing for the same name.
func (r rec) only() bool { return r.Type == "A" }

// same tells whether a value read back from a provider is ours.
func (r rec) same(got string) bool {
	if r.Type == "CAA" {
		return sameCAA(got, r.Value)
	}
	return strings.TrimSpace(got) == r.Value
}

// text is the value in the form provider APIs take.
func (r rec) text() string {
	if r.Type == "CAA" {
		return caaRecord(r.Value)
	}
	return r.Value
}

// held is a record of the wanted type a provider already has on the name.
type held struct{ id, value string }

// judge sorts what a provider holds: ok = ours is among them; others = the ids to remove (only() types). Done when
// ok and no others.
func (r rec) judge(have []held) (ok bool, others []string) {
	for _, h := range have {
		if r.same(h.value) {
			ok = true
		} else if r.only() {
			others = append(others, h.id)
		}
	}
	return ok, others
}

// recordWriter reports whether host lacks the record (or, for only() types, also has others of its type); with
// apply it puts it right. Besides the A record of an only() type, it leaves every other record alone.
type recordWriter func(ctx context.Context, host string, r rec, apply bool) (missing bool, err error)

var recordWriters = map[string]recordWriter{
	"cloudflare": func(ctx context.Context, host string, r rec, apply bool) (bool, error) {
		return cloudflareRecord(ctx, cfAPI, host, r, apply)
	},
	"alidns":       alidnsRecord,
	"tencentcloud": tencentRecord,
	"route53":      route53Record,
	"digitalocean": func(ctx context.Context, host string, r rec, apply bool) (bool, error) {
		return digitaloceanRecord(ctx, doAPI, host, r, apply)
	},
	"huaweicloud": huaweiRecord,
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
		return "", "", fmt.Errorf(i18n.S("这份凭据下找不到 %s 所在的域名（zone），确认它托管在这个账号下、凭据能读到它", "no zone for %s under these credentials; make sure it's hosted in this account and the credentials can read it"), host)
	}
	if rr = strings.TrimSuffix(strings.TrimSuffix(host, zone), "."); rr == "" {
		rr = "@"
	}
	return zone, rr, nil
}

// RecordsSupported reports whether SetCAA / SetA can check and write records through this provider.
func RecordsSupported(dns string) bool { return recordWriters[dns] != nil }

// SetCAA reports whether host lacks the CAA record; with apply it adds it (missing=true then means "added now").
func SetCAA(ctx context.Context, dns, host, accountURI string, apply bool) (missing bool, err error) {
	if accountURI == "" {
		return false, errors.New(i18n.S("还没签过证书，没有 ACME 账户可写进 CAA", "no certificate issued yet, so there's no ACME account to put in the CAA record"))
	}
	return setRecord(ctx, dns, host, rec{"CAA", CAAValue(accountURI)}, apply)
}

// SetA reports whether host lacks an A record pointing at ip alone; with apply it adds ours and removes the other
// A records on the name (missing=true then means "fixed now").
func SetA(ctx context.Context, dns, host, ip string, apply bool) (missing bool, err error) {
	return setRecord(ctx, dns, host, rec{"A", ip}, apply)
}

func setRecord(ctx context.Context, dns, host string, r rec, apply bool) (bool, error) {
	w := recordWriters[dns]
	if w == nil {
		return false, fmt.Errorf(i18n.S("%s 还不支持自动加 %s 记录，照上面那条手动加", "%s can't add the %s record automatically yet; add the record above by hand"), dns, r.Type)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return w(ctx, strings.TrimSuffix(host, "."), r, apply)
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

func cloudflareRecord(ctx context.Context, api, host string, want rec, apply bool) (bool, error) {
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
			return errors.New(i18n.S("没找到 Cloudflare 的 API Token", "no Cloudflare API token found"))
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
			return fmt.Errorf(i18n.S("连不上 Cloudflare：%w", "can't reach Cloudflare: %w"), err)
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
			return fmt.Errorf(i18n.S("Cloudflare 返回了看不懂的东西（HTTP %d）", "unreadable response from Cloudflare (HTTP %d)"), resp.StatusCode)
		}
		if !env.Success {
			var msgs []string
			for _, e := range env.Errors {
				msgs = append(msgs, fmt.Sprintf("%d %s", e.Code, e.Message))
			}
			return fmt.Errorf(i18n.S("Cloudflare 拒绝了（HTTP %d）：%s", "Cloudflare refused (HTTP %d): %s"), resp.StatusCode, strings.Join(msgs, "; "))
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
		return false, fmt.Errorf(i18n.S("这个 Cloudflare 凭据下找不到 %s 所在的 zone（token 要能读这个 zone）", "no zone for %s under this Cloudflare token (it needs read access to the zone)"), host)
	}

	// Cloudflare takes CAA as structured data and A as content. An A record with the orange cloud on (proxied)
	// is not ours even with the right IP: Cloudflare would terminate TLS itself, and the relay only routes by SNI.
	type caaData struct {
		Flags int    `json:"flags"`
		Tag   string `json:"tag"`
		Value string `json:"value"`
	}
	var recs []struct {
		ID      string  `json:"id"`
		Content string  `json:"content"`
		Proxied bool    `json:"proxied"`
		Data    caaData `json:"data"`
	}
	if err := call("GET", "/zones/"+zoneID+"/dns_records?type="+want.Type+"&name="+url.QueryEscape(host), nil, &recs, false); err != nil {
		return false, err
	}
	ok, unproxy := false, ""
	var others []string
	for _, r := range recs {
		switch {
		case want.Type == "CAA":
			if r.Data.Tag == "issue" && want.same(r.Data.Value) {
				ok = true
			}
		case !want.same(r.Content):
			others = append(others, r.ID)
		case r.Proxied:
			unproxy = r.ID
		default:
			ok = true
		}
	}
	if !want.only() {
		others = nil
	}
	if ok && len(others) == 0 {
		return false, nil
	}
	if !apply {
		return true, nil
	}
	switch {
	case ok:
	case unproxy != "":
		if err := call("PATCH", "/zones/"+zoneID+"/dns_records/"+unproxy, map[string]any{"proxied": false}, nil, false); err != nil {
			return false, err
		}
	default:
		body := map[string]any{"type": want.Type, "name": host, "ttl": 1}
		if want.Type == "CAA" {
			body["data"] = caaData{Flags: 0, Tag: "issue", Value: want.Value}
		} else {
			body["content"], body["proxied"] = want.Value, false
		}
		if err := call("POST", "/zones/"+zoneID+"/dns_records", body, nil, false); err != nil {
			return false, err
		}
	}
	// Ours goes in first, the others go after: the name never answers with nothing in between.
	for _, id := range others {
		if err := call("DELETE", "/zones/"+zoneID+"/dns_records/"+id, nil, nil, false); err != nil {
			return false, err
		}
	}
	return true, nil
}
