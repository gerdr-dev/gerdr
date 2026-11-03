package acme

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// LookupA asks public DNS-over-HTTPS resolvers for name's IPv4 addresses, not the system resolver: a machine
// running a proxy in fake-IP mode (Clash, Surge…; this is common where gerdr is used) gets 198.18.x.x answers
// from it, which would then be written into DNS as "the relay's IP" and compared against as "where the name
// points now". DoH goes out as plain HTTPS, so the proxy forwards it untouched. Several resolvers, one of them
// reachable from mainland China. An IP literal comes back as itself.
func LookupA(ctx context.Context, name string) ([]string, error) {
	if ip, err := netip.ParseAddr(name); err == nil {
		return []string{ip.String()}, nil
	}
	var errs []error
	for _, base := range dohResolvers {
		ips, err := dohA(ctx, base, name)
		if err == nil {
			return ips, nil
		}
		errs = append(errs, err)
	}
	return nil, fmt.Errorf(i18n.S("查不到 %s 的 IP：%w", "can't look up %s: %w"), name, errors.Join(errs...))
}

var dohResolvers = []string{"https://cloudflare-dns.com/dns-query", "https://dns.google/resolve", "https://dns.alidns.com/resolve"}

func dohA(ctx context.Context, base, name string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "GET", base+"?"+url.Values{"name": {name}, "type": {"1"}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Accept", "application/dns-json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Status int
		Answer []struct {
			Type int
			Data string `json:"data"`
		}
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", base, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: %w", base, err)
	}
	// 0 NOERROR, 3 NXDOMAIN: both are answers (the latter just has no address); anything else is the resolver failing.
	if out.Status != 0 && out.Status != 3 {
		return nil, fmt.Errorf("%s: DNS status %d", base, out.Status)
	}
	ips := []string{}
	for _, a := range out.Answer {
		if a.Type == 1 { // A; a CNAME chain shows up as type 5 entries before it
			ips = append(ips, a.Data)
		}
	}
	return ips, nil
}
