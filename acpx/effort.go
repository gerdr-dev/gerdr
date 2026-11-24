package acpx

import "strings"

var effortNames = map[string]string{
	"off": "Off", "none": "Off", "minimal": "Minimal", "low": "Low", "medium": "Medium", "high": "High",
	"xhigh": "Extra high", "max": "Max",
}

// EffortName is how a thinking level shows in the effort menu and on the composer's button, the same for every
// agent. The adapters used to show the API's own values ("medium", "xhigh") while Gerdr Agent showed "中" / "高",
// and next to "Auto · Opus 5.5" the lowercase value read like a stray word. A level not listed here is shown with
// its first letter capitalised; the value sent to the agent stays as it was.
func EffortName(v string) string {
	if n, ok := effortNames[strings.ToLower(v)]; ok {
		return n
	}
	if v == "" {
		return v
	}
	return strings.ToUpper(v[:1]) + v[1:]
}
