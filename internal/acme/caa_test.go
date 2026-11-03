package acme

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// A fake Cloudflare: the zone sits one label up, and the record is only added once.
func TestCloudflareCAA(t *testing.T) {
	t.Setenv("GERDR_CLOUDFLARE_DNS_API_TOKEN", "tok")
	var recs []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth header %q", r.Header.Get("Authorization"))
		}
		var result any = []any{}
		switch {
		case r.URL.Path == "/zones" && r.URL.Query().Get("name") == "example.com":
			result = []any{map[string]any{"id": "z1"}}
		case r.URL.Path == "/zones":
		case r.URL.Path == "/zones/z1/dns_records" && r.Method == "GET":
			result = recs
		case r.URL.Path == "/zones/z1/dns_records" && r.Method == "POST":
			var rec map[string]any
			_ = json.NewDecoder(r.Body).Decode(&rec)
			recs = append(recs, rec)
			result = rec
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
	}))
	defer srv.Close()

	val := CAAValue("https://acme/acct/1")
	// check only, add, then both see it present
	for i, c := range []struct{ apply, missing bool }{{false, true}, {true, true}, {false, false}, {true, false}} {
		missing, err := cloudflareRecord(context.Background(), srv.URL, "gerdr.example.com", rec{"CAA", val}, c.apply)
		if err != nil || missing != c.missing {
			t.Fatalf("call %d: missing=%v err=%v, want %v", i, missing, err, c.missing)
		}
	}
	if len(recs) != 1 || recs[0]["name"] != "gerdr.example.com" || recs[0]["type"] != "CAA" {
		t.Fatalf("records: %v", recs)
	}
}

func TestPickZone(t *testing.T) {
	zones := []string{"example.com", "sub.example.com.", "other.org"}
	for _, c := range []struct{ host, zone, rr string }{
		{"gerdr.example.com", "example.com", "gerdr"},
		{"a.b.example.com", "example.com", "a.b"},
		{"gerdr.sub.example.com", "sub.example.com", "gerdr"}, // delegated sub-zone beats its parent
		{"Example.COM.", "example.com", "@"},
	} {
		zone, rr, err := pickZone(c.host, zones)
		if err != nil || zone != c.zone || rr != c.rr {
			t.Errorf("%s: got %q %q %v, want %q %q", c.host, zone, rr, err, c.zone, c.rr)
		}
	}
	if _, _, err := pickZone("gerdr.notexample.com", zones); err == nil {
		t.Error("a name outside every zone must not match by bare suffix")
	}
}

func TestSameCAA(t *testing.T) {
	v := CAAValue("https://acme/acct/1")
	for _, got := range []string{
		`0 issue "letsencrypt.org; accounturi=https://acme/acct/1; validationmethods=dns-01"`,
		`0 issue "letsencrypt.org;accounturi=https://acme/acct/1;validationmethods=dns-01"`,
		`0 issue \"letsencrypt.org; accounturi=https://acme/acct/1; validationmethods=dns-01\"`,
		v,
	} {
		if !sameCAA(got, v) {
			t.Errorf("should match: %s", got)
		}
	}
	for _, got := range []string{`0 issue "letsencrypt.org"`, CAAValue("https://acme/acct/2"), `0 issuewild "` + v + `"`} {
		if sameCAA(got, v) {
			t.Errorf("should not match: %s", got)
		}
	}
}

// A fake DigitalOcean API: record names go in relative to the domain, and the record is only added once.
func TestDigitalOceanCAA(t *testing.T) {
	t.Setenv("GERDR_DO_AUTH_TOKEN", "tok")
	t.Setenv("GERDR_DO_API_URL", "")
	var recs []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth header %q", r.Header.Get("Authorization"))
		}
		var out any
		switch {
		case r.URL.Path == "/v2/domains":
			out = map[string]any{"domains": []any{map[string]any{"name": "example.com"}}}
		case r.URL.Path == "/v2/domains/example.com/records" && r.Method == "GET":
			if r.URL.Query().Get("name") != "gerdr.example.com" || r.URL.Query().Get("type") != "CAA" {
				t.Errorf("list query %s", r.URL.RawQuery)
			}
			out = map[string]any{"domain_records": recs}
		case r.URL.Path == "/v2/domains/example.com/records" && r.Method == "POST":
			var rec map[string]any
			_ = json.NewDecoder(r.Body).Decode(&rec)
			recs = append(recs, rec)
			w.WriteHeader(201)
			out = map[string]any{"domain_record": rec}
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	val := CAAValue("https://acme/acct/1")
	for i, c := range []struct{ apply, missing bool }{{false, true}, {true, true}, {false, false}, {true, false}} {
		missing, err := digitaloceanRecord(context.Background(), srv.URL, "gerdr.example.com", rec{"CAA", val}, c.apply)
		if err != nil || missing != c.missing {
			t.Fatalf("call %d: missing=%v err=%v, want %v", i, missing, err, c.missing)
		}
	}
	if len(recs) != 1 || recs[0]["name"] != "gerdr" || recs[0]["tag"] != "issue" || recs[0]["data"] != val {
		t.Fatalf("records: %v", recs)
	}
}

// Every provider in the settings dropdown gets the button.
func TestCAAEveryProvider(t *testing.T) {
	for _, p := range providers {
		if !RecordsSupported(p.Name) {
			t.Errorf("%s has no record writer", p.Name)
		}
	}
}

// A fake Cloudflare zone for the A record: ours replaces a wrong address, and a right one with the proxy on (which
// would end TLS at Cloudflare instead of passing it to the relay) gets the proxy turned off rather than duplicated.
func TestCloudflareA(t *testing.T) {
	t.Setenv("GERDR_CLOUDFLARE_DNS_API_TOKEN", "tok")
	for _, c := range []struct {
		name  string
		start []map[string]any
	}{
		{"empty", nil},
		{"wrong ip", []map[string]any{{"id": "old", "type": "A", "content": "9.9.9.9", "proxied": false}}},
		{"proxied", []map[string]any{{"id": "p", "type": "A", "content": "1.2.3.4", "proxied": true}}},
		{"ours and a stray", []map[string]any{{"id": "a", "type": "A", "content": "1.2.3.4", "proxied": false}, {"id": "b", "type": "A", "content": "9.9.9.9", "proxied": false}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			recs := append([]map[string]any(nil), c.start...)
			n := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var result any = []any{}
				id := strings.TrimPrefix(r.URL.Path, "/zones/z1/dns_records/")
				switch {
				case r.URL.Path == "/zones" && r.URL.Query().Get("name") == "example.com":
					result = []any{map[string]any{"id": "z1"}}
				case r.URL.Path == "/zones":
				case r.URL.Path == "/zones/z1/dns_records" && r.Method == "GET":
					if r.URL.Query().Get("type") != "A" {
						t.Errorf("list query %s", r.URL.RawQuery)
					}
					result = recs
				case r.URL.Path == "/zones/z1/dns_records" && r.Method == "POST":
					var rec map[string]any
					_ = json.NewDecoder(r.Body).Decode(&rec)
					n++
					rec["id"] = fmt.Sprint("new", n)
					recs = append(recs, rec)
				case r.Method == "PATCH":
					var p map[string]any
					_ = json.NewDecoder(r.Body).Decode(&p)
					for _, rec := range recs {
						if rec["id"] == id {
							rec["proxied"] = p["proxied"]
						}
					}
				case r.Method == "DELETE":
					recs = slices.DeleteFunc(recs, func(rec map[string]any) bool { return rec["id"] == id })
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
			}))
			defer srv.Close()

			want := rec{"A", "1.2.3.4"}
			for i, s := range []struct{ apply, missing bool }{{false, true}, {true, true}, {false, false}, {true, false}} {
				missing, err := cloudflareRecord(context.Background(), srv.URL, "gerdr.example.com", want, s.apply)
				if err != nil || missing != s.missing {
					t.Fatalf("call %d: missing=%v err=%v, want %v", i, missing, err, s.missing)
				}
			}
			if len(recs) != 1 || recs[0]["content"] != "1.2.3.4" || recs[0]["proxied"] != false {
				t.Fatalf("records: %v", recs)
			}
		})
	}
}

// DigitalOcean: a wrong address is replaced, a CAA beside it isn't touched (the list is per type).
func TestDigitalOceanA(t *testing.T) {
	t.Setenv("GERDR_DO_AUTH_TOKEN", "tok")
	t.Setenv("GERDR_DO_API_URL", "")
	recs := []map[string]any{{"id": 7, "type": "A", "name": "gerdr", "data": "9.9.9.9"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var out any
		switch {
		case r.URL.Path == "/v2/domains":
			out = map[string]any{"domains": []any{map[string]any{"name": "example.com"}}}
		case r.URL.Path == "/v2/domains/example.com/records" && r.Method == "GET":
			out = map[string]any{"domain_records": recs}
		case r.URL.Path == "/v2/domains/example.com/records" && r.Method == "POST":
			var rec map[string]any
			_ = json.NewDecoder(r.Body).Decode(&rec)
			rec["id"] = 8
			recs = append(recs, rec)
			w.WriteHeader(201)
			out = map[string]any{"domain_record": rec}
		case r.URL.Path == "/v2/domains/example.com/records/7" && r.Method == "DELETE":
			recs = recs[1:]
			w.WriteHeader(204)
			return
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	want := rec{"A", "1.2.3.4"}
	for i, s := range []struct{ apply, missing bool }{{false, true}, {true, true}, {false, false}} {
		missing, err := digitaloceanRecord(context.Background(), srv.URL, "gerdr.example.com", want, s.apply)
		if err != nil || missing != s.missing {
			t.Fatalf("call %d: missing=%v err=%v, want %v", i, missing, err, s.missing)
		}
	}
	if len(recs) != 1 || recs[0]["data"] != "1.2.3.4" || recs[0]["name"] != "gerdr" {
		t.Fatalf("records: %v", recs)
	}
}
