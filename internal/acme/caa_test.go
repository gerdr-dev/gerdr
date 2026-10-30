package acme

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		missing, err := cloudflareCAA(context.Background(), srv.URL, "gerdr.example.com", val, c.apply)
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
		missing, err := digitaloceanCAA(context.Background(), srv.URL, "gerdr.example.com", val, c.apply)
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
		if !CAASupported(p.Name) {
			t.Errorf("%s has no CAA writer", p.Name)
		}
	}
}
