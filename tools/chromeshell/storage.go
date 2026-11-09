package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// migrateStorage: the UI keeps its per-device settings in the page's localStorage (ui/src/lib/store/prefs.ts:
// language, labs switches, sizes…), which is per origin. Moving the pages from 127.0.0.1:<port> to http://gerdr
// (main.go pageHost) would reset them, so once per profile they are carried over: a hidden page on the old origin (a
// path the backend answers with a plain 404: no UI loads, but the document is that origin's) hands its localStorage
// over, and the windows get it before their first script (attach: seedJS), keys they don't have only. That page is a
// minimized window for a moment ("hidden" targets aren't allowed to a CDP client, pipe or not).
func migrateStorage(c *cdp, profile, oldOrigin, newOrigin string) {
	marker := filepath.Join(profile, "storage-moved-to-"+strings.TrimPrefix(newOrigin, "http://"))
	if oldOrigin == newOrigin || fileExists(marker) {
		return
	}
	r, err := c.call("", "Target.createTarget", map[string]any{"url": oldOrigin + storagePage, "newWindow": true, "background": true, "windowState": "minimized"})
	if err != nil {
		log.Printf("storage: %v", err)
		return
	}
	var t struct {
		TargetID string `json:"targetId"`
	}
	_ = json.Unmarshal(r, &t)
	defer c.call("", "Target.closeTarget", map[string]any{"targetId": t.TargetID})
	r, err = c.call("", "Target.attachToTarget", map[string]any{"targetId": t.TargetID, "flatten": true})
	if err != nil {
		log.Printf("storage: %v", err)
		return
	}
	var a struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(r, &a)
	var items string
	for i := 0; i < 50; i++ {
		v, err := c.eval(a.SessionID, `location.origin === `+jsString(oldOrigin)+` && document.readyState === "complete" ? JSON.stringify(Object.entries(localStorage)) : ""`)
		if err == nil {
			_ = json.Unmarshal(v, &items)
		}
		if items != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if items == "" {
		log.Printf("storage: couldn't read %s's settings; again next time", oldOrigin)
		return
	}
	_ = os.WriteFile(marker, nil, 0o644) // read: done, whatever there was
	if items == "[]" {
		log.Printf("storage: nothing to carry over from %s", oldOrigin)
		return
	}
	seedJS = `(() => {
		if (location.origin !== ` + jsString(newOrigin) + `) return;
		for (const [k, v] of ` + items + `) if (localStorage.getItem(k) === null) localStorage.setItem(k, v);
	})();`
	log.Printf("storage: carrying %d bytes of settings over from %s", len(items), oldOrigin)
}

// storagePage: what migrateStorage opens on the old origin (attach leaves it alone).
const storagePage = "/__chromeshell-storage.txt"

// seedJS: the settings migrateStorage read, for the windows' first documents ("" = none).
var seedJS string

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
