// Package i18n picks the language of the text the backend shows people: CLI output, errors the UI displays, and the
// notices / permission options / mode descriptions it writes into sessions.
//
// It is one language for the whole process, the computer's: text the backend writes into a session log is shared by
// every device looking at it (a phone and the computer may be set differently), so it can't follow any one device.
// The UI's own texts follow each device (ui/src/lib/i18n.ts); on the computer both default to the system language.
//
// Text sits next to the code as a pair, S("中文", "English"), instead of in a key table: the backend's texts are
// scattered one-offs, and a pair keeps both in view when either is edited.
package i18n

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Locale is "zh" or "en".
type Locale string

const (
	ZH Locale = "zh"
	EN Locale = "en"
)

var (
	once    sync.Once
	current Locale
)

// Current is the backend's language: GERDR_LANG if set (zh / en), else the system's (macOS: the first preferred
// language; elsewhere LC_ALL / LC_MESSAGES / LANG). Chinese in any form is zh, everything else en.
//
// Under `go test` it is zh unless GERDR_LANG says otherwise, so tests that check texts don't depend on the machine's
// language.
func Current() Locale {
	once.Do(func() { current = detect() })
	return current
}

// Set overrides the language (tests, or a caller that knows better).
func Set(l Locale) {
	once.Do(func() {})
	current = l
}

func detect() Locale {
	if v := os.Getenv("GERDR_LANG"); v != "" {
		return parse(v)
	}
	if testing.Testing() {
		return ZH
	}
	// macOS: launchd and the App don't set LANG, and LANG in a terminal often says en_US.UTF-8 whatever the user
	// picked in System Settings; AppleLanguages is what the system UI itself follows.
	// The user's list may be missing (never changed it), with the language only in the machine-wide preferences
	// (set at install) or in AppleLocale (region and language, e.g. zh-Hans_CN).
	if runtime.GOOS == "darwin" {
		for _, args := range [][]string{
			{"read", "-g", "AppleLanguages"},
			{"read", "/Library/Preferences/.GlobalPreferences", "AppleLanguages"},
			{"read", "-g", "AppleLocale"},
		} {
			out, err := exec.Command("defaults", args...).Output()
			if err != nil {
				continue
			}
			for _, f := range strings.FieldsFunc(string(out), func(r rune) bool { return strings.ContainsRune("(),\" \n\t", r) }) {
				return parse(f)
			}
		}
	}
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" && v != "C" && v != "POSIX" {
			return parse(v)
		}
	}
	return EN
}

func parse(v string) Locale {
	if strings.HasPrefix(strings.ToLower(v), "zh") {
		return ZH
	}
	return EN
}

// S returns zh or en by the backend's language.
func S(zh, en string) string {
	if Current() == ZH {
		return zh
	}
	return en
}

// IsZH reports whether the backend's language is Chinese (for the odd case a pair of S() calls doesn't fit, like
// plurals or a table).
func IsZH() bool { return Current() == ZH }
