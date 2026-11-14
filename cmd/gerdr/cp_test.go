package main

import "testing"

func TestParseSpec(t *testing.T) {
	for in, want := range map[string]cpSpec{
		"mini:~/bin/":     {host: "mini", path: "~/bin/", into: true},
		"mini:~/bin":      {host: "mini", path: "~/bin"},
		"mini:":           {host: "mini", into: true},
		"home-2:/var/x":   {host: "home-2", path: "/var/x"},
		"./a:b":           {path: "./a:b"},
		"C:/Users/x":      {path: "C:/Users/x"}, // a Windows path, not a host
		"c:/Users/x":      {path: "c:/Users/x"}, // host ids are two characters or more
		"dist":            {path: "dist"},
		".":               {path: ".", into: true},
		"out/":            {path: "out/", into: true},
		"Mini:/x":         {path: "Mini:/x"},
		"some/dir:x/y.go": {path: "some/dir:x/y.go"},
	} {
		if got := parseSpec(in); got != want {
			t.Errorf("%q: %+v, want %+v", in, got, want)
		}
	}
}
