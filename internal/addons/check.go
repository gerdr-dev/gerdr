package addons

// Check: what is wrong with an add-on's addon.json, for whoever writes one (mostly an agent).
//
// Loading is lenient on purpose: encoding/json drops fields it doesn't know, an unknown setting type is drawn as a
// text box, a select without options is an empty dropdown, an i18n entry for a key that isn't there is never used. So a
// typo ("option", "defualt", "type": "password") doesn't fail; the field just doesn't show, or shows wrong, and
// nothing says why. Check says it, as a list with a path to each spot, so `gerdr addon check` (and install / update /
// list, which include its problems) can tell an agent what to fix before the person ever opens the form.
//
// Unknown fields are found by walking the JSON against the Manifest struct's json tags (knownFields), so a field
// added to the structs is known here with no list to keep in step.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Problem is one thing wrong in an addon.json.
type Problem struct {
	Path    string `json:"path"` // where: "settings[3] (asrProvider).options", "i18n.en.settings.foo"; "" for the file
	Problem string `json:"problem"`
}

// SettingTypes: every type the settings form draws (ui/src/components/AddonSettings.tsx); anything else falls back to a
// text box there.
var SettingTypes = []string{"number", "string", "text", "boolean", "select", "color", "icon", "secret", "link", "action"}

var hexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// CheckDir checks dir/addon.json. The error is for a folder with no addon.json or one that can't be read; what's wrong
// inside it comes back as problems.
func CheckDir(dir string) ([]Problem, error) {
	b, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s has no %s", dir, ManifestFile)
		}
		return nil, err
	}
	return CheckManifest(b, dir), nil
}

// CheckManifest checks an addon.json's bytes; dir (may be "") is its folder, for the files it names.
func CheckManifest(b []byte, dir string) []Problem {
	var out []Problem
	add := func(path, format string, a ...any) { out = append(out, Problem{path, fmt.Sprintf(format, a...)}) }

	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		var te *json.UnmarshalTypeError
		if !errors.As(err, &te) {
			add("", "not valid JSON: %v", err) // nothing else can be read
			return out
		}
		// The rest is still filled in: report it and go on
		add(te.Field, "is a JSON %s, should be %s", te.Value, jsonKind(te.Type))
	}
	unknownFields(b, reflect.TypeOf(m), "", &out)

	file := func(path, rel string) {
		if rel == "" || dir == "" {
			return
		}
		p, err := within(dir, rel)
		if err != nil {
			add(path, "%q is outside the add-on's folder", rel)
		} else if _, err := os.Stat(p); err != nil {
			add(path, "no file %q in the add-on's folder", rel)
		}
	}
	file("icon", m.Icon)
	for i, s := range m.Screenshots {
		file(fmt.Sprintf("screenshots[%d]", i), s)
	}
	file("main", m.Main)
	if m.Service != nil && strings.TrimSpace(m.Service.Command) == "" {
		add("service.command", "is empty: the service can't start")
	}
	for i, e := range m.Emits {
		if e.Kind == "" {
			add(fmt.Sprintf("emits[%d]", i), "has no kind: it is ignored")
		}
	}

	byKey := map[string]SettingSpec{}
	for i, s := range m.Settings {
		at := fmt.Sprintf("settings[%d]", i)
		if s.Key != "" {
			at += " (" + s.Key + ")"
		}
		checkSetting(s, at, add)
		if s.Key == "" {
			continue
		}
		if _, dup := byKey[s.Key]; dup {
			add(at, "key %q is used by an earlier setting too: they share one value", s.Key)
		}
		byKey[s.Key] = s
	}

	locales := make([]string, 0, len(m.I18n))
	for l := range m.I18n {
		locales = append(locales, l)
	}
	sort.Strings(locales)
	for _, l := range locales {
		keys := make([]string, 0, len(m.I18n[l].Settings))
		for k := range m.I18n[l].Settings {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			t := m.I18n[l].Settings[k]
			at := "i18n." + l + ".settings." + k
			s, ok := byKey[k]
			if !ok {
				add(at, "there is no setting %q%s", k, suggest(k, mapKeys(byKey)))
				continue
			}
			if t.Unit != "" && s.Type != "number" {
				add(at+".unit", "only a number shows a unit")
			}
			if t.Label != "" && !s.Valueless() {
				add(at+".label", "only a link / action has a button label")
			}
			if len(t.Options) > 0 && s.Type != "select" {
				add(at+".options", "only a select has options")
			} else if len(s.Options) > 0 {
				var vals []string
				for _, o := range s.Options {
					vals = append(vals, fmt.Sprint(o.Value))
				}
				for v := range t.Options {
					if !slices.Contains(vals, v) {
						add(at+".options."+v, "%q is not one of the option values (%s)", v, strings.Join(vals, ", "))
					}
				}
			}
		}
	}
	// In file order: the top-level fields, then setting by setting (a setting's problems together), then i18n
	sort.SliceStable(out, func(i, j int) bool { return pathOrder(out[i].Path) < pathOrder(out[j].Path) })
	return out
}

var settingIndexRE = regexp.MustCompile(`^settings\[(\d+)\]`)

func pathOrder(path string) int {
	if m := settingIndexRE.FindStringSubmatch(path); m != nil {
		n := 0
		fmt.Sscan(m[1], &n)
		return 1 + n
	}
	if strings.HasPrefix(path, "i18n") {
		return 1 << 30
	}
	return 0
}

func checkSetting(s SettingSpec, at string, add func(path, format string, a ...any)) {
	if s.Key == "" {
		add(at, "has no key")
	}
	if s.Type == "" {
		add(at+".type", "is missing (%s)", strings.Join(SettingTypes, ", "))
		return
	}
	if !slices.Contains(SettingTypes, s.Type) {
		add(at+".type", "%q is not a setting type (%s)%s", s.Type, strings.Join(SettingTypes, ", "), suggest(s.Type, SettingTypes))
		return
	}

	// Fields that belong to another type are ignored by the form: say so, they were meant to do something
	only := func(set bool, field, types string) {
		if set {
			add(at+"."+field, "only a %s uses %s: it is ignored here", types, field)
		}
	}
	isSelect, isNumber := s.Type == "select", s.Type == "number"
	only(len(s.Options) > 0 && !isSelect, "options", "select")
	only(s.OptionsFrom != nil && !isSelect, "optionsFrom", "select")
	only(s.Required && !isSelect, "required", "select")
	only((s.Min != nil || s.Max != nil) && !isNumber, "min / max", "number")
	only(s.Unit != "" && !isNumber, "unit", "number")
	only(s.URL != "" && s.Type != "link", "url", "link")
	only(s.Label != "" && !s.Valueless(), "label", "link / action")

	if s.Valueless() {
		if s.Default != nil {
			add(at+".default", "a %s has no value: default is ignored", s.Type)
		}
		if s.URL != "" && !strings.HasPrefix(s.URL, "http://") && !strings.HasPrefix(s.URL, "https://") {
			add(at+".url", "only http(s) addresses open")
		}
		return
	}

	if isSelect {
		if len(s.Options) == 0 && s.OptionsFrom == nil {
			add(at, "a select needs options (or optionsFrom)")
		}
		seen := map[string]bool{}
		for i, o := range s.Options {
			v := fmt.Sprint(o.Value)
			if seen[v] {
				add(fmt.Sprintf("%s.options[%d]", at, i), "value %q is there twice", v)
			}
			seen[v] = true
		}
	}
	if isNumber && s.Min != nil && s.Max != nil && *s.Min > *s.Max {
		add(at, "min is above max")
	}

	d := s.Default
	if d == nil {
		return
	}
	switch s.Type {
	case "number":
		n, ok := d.(float64)
		if !ok {
			add(at+".default", "should be a number, not %s", jsonOf(d))
		} else if s.Min != nil && n < *s.Min || s.Max != nil && n > *s.Max {
			add(at+".default", "%v is outside min / max", n)
		}
	case "boolean":
		if _, ok := d.(bool); !ok {
			add(at+".default", "should be true or false, not %s", jsonOf(d))
		}
	case "select":
		if len(s.Options) > 0 && !(d == "" && !s.Required) &&
			!slices.ContainsFunc(s.Options, func(o SettingOption) bool { return fmt.Sprint(o.Value) == fmt.Sprint(d) }) {
			add(at+".default", "%s is not one of the options' values", jsonOf(d))
		}
	case "color":
		if c, ok := d.(string); !ok || c != "" && !hexColorRE.MatchString(c) {
			add(at+".default", "should be a \"#rrggbb\" string, not %s", jsonOf(d))
		}
	default: // string, text, secret, icon
		if _, ok := d.(string); !ok {
			add(at+".default", "should be a string, not %s", jsonOf(d))
		}
	}
}

// unknownFields reports the object keys in raw that t (a struct, through pointers, slices and maps) has no json tag
// for, recursing into the ones it has.
func unknownFields(raw json.RawMessage, t reflect.Type, path string, out *[]Problem) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	join := func(k string) string {
		if path == "" {
			return k
		}
		return path + "." + k
	}
	switch t.Kind() {
	case reflect.Struct:
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			return // not an object: the type error is reported by Unmarshal
		}
		known := knownFields(t)
		names := mapKeys(known)
		keys := mapKeys(obj)
		for _, k := range keys {
			f, ok := known[k]
			if !ok {
				*out = append(*out, Problem{path, fmt.Sprintf("unknown field %q: it is ignored%s", k, suggest(k, names))})
				continue
			}
			unknownFields(obj[k], f.Type, join(k), out)
		}
	case reflect.Slice:
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) != nil {
			return
		}
		for i, el := range arr {
			p := fmt.Sprintf("%s[%d]", path, i)
			// A setting's path names its key, which reads better than an index alone
			var k struct {
				Key string `json:"key"`
			}
			if t.Elem() == reflect.TypeOf(SettingSpec{}) && json.Unmarshal(el, &k) == nil && k.Key != "" {
				p += " (" + k.Key + ")"
			}
			unknownFields(el, t.Elem(), p, out)
		}
	case reflect.Map:
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			return
		}
		for _, k := range mapKeys(obj) {
			unknownFields(obj[k], t.Elem(), join(k), out)
		}
	}
}

// knownFields: a struct's fields by json name (embedded structs' fields included).
func knownFields(t reflect.Type) map[string]reflect.StructField {
	out := map[string]reflect.StructField{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		if f.Anonymous && name == "" {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			for k, v := range knownFields(ft) {
				out[k] = v
			}
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = f
	}
	return out
}

// suggest: ` (did you mean "x"?)` when one of names is a near miss of s.
func suggest(s string, names []string) string {
	best, bestD := "", 3
	for _, n := range names {
		if d := editDistance(strings.ToLower(s), strings.ToLower(n)); d < bestD {
			best, bestD = n, d
		}
	}
	if best == "" || best == s {
		return ""
	}
	return fmt.Sprintf(" (did you mean %q?)", best)
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// jsonKind names a Go type the way addon.json's author thinks of it.
func jsonKind(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int64, reflect.Float64:
		return "a number"
	case reflect.Slice:
		return "a list"
	case reflect.Struct, reflect.Map:
		return "an object"
	}
	return t.String()
}

// CheckFolders: the add-on folders under dir to check — dir itself when it has an addon.json, else the folders one or
// two levels down that do (a store's checkout), the way a store finds them, but also the ones whose addon.json
// doesn't parse (a store skips those silently; that's what a check is for).
func CheckFolders(dir string) []string {
	if _, err := os.Stat(filepath.Join(dir, ManifestFile)); err == nil {
		return []string{dir}
	}
	var out []string
	var walk func(d string, depth int)
	walk = func(d string, depth int) {
		ents, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range ents {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "node_modules" {
				continue
			}
			full := filepath.Join(d, name)
			if _, err := os.Stat(filepath.Join(full, ManifestFile)); err == nil {
				out = append(out, full)
			} else if depth < 2 {
				walk(full, depth+1)
			}
		}
	}
	walk(dir, 1)
	return out
}
