# chromeshell: a desktop shell on the user's own Chrome

Prototype for replacing the Electron App (dmg ~120MB, App ~330MB) with the Go backend alone plus a small Go shell
that drives a Chrome-family browser already on the machine. Same engine as today (Chromium, docs/dev/WHY-ELECTRON.md),
nothing of it shipped.

## Gerdr exp.app

```sh
tools/chromeshell/package.sh install    # → /Applications/Gerdr exp.app, and dist/Gerdr-exp-mac-universal.dmg / .zip
ARCH=arm64 tools/chromeshell/package.sh # one arch: dist/Gerdr-exp-mac-arm64.dmg
```

| | dmg |
|---|---|
| universal (Apple silicon + Intel) | ~38MB |
| one arch | ~18MB |
| the Electron build, for comparison | ~120MB per arch |

Inside: the shell (~7MB per arch), the menu library, the icon, and the backend (~42MB per arch, most of it).

Opened from Finder it needs no flags. The backend runs **as Gerdr.app runs it: `gerdr --app`**, which attaches to the
Gerdr already serving (the background service) and, when that runs another binary, restarts it on ours **once nothing is
running** (cmd/gerdr appAttach). The binary: the newer of the bundle's (`Resources/gerdr`) and a downloaded one.

**Updates** (`updater.go`, `app.go`; a port of desktop/updater.js and main.js, docs/dev/UPDATE.md): the backend-only
half. Every 4 hours (first 20s after start) while automatic updates are on, `update.json` of the latest release; a newer
backend for this arch (`gerdr-<v>-darwin-<arch>.br`, ~10MB) is downloaded into `~/Library/Application Support/<name>
Shell/backend/<v>/`, checked (size, sha256), unpacked, and applied by restarting our backend once `/api/busy` is all
zero (or when the person says "now": a dialog when something would be cut off). Settings → 版本 (check, update now,
automatic updates) and the app menu's 检查更新… / 更新到 x… drive it. The feed's whole-App half (an Electron dmg) isn't
for this shell and is skipped: a new shell needs releases of its own. Local builds (`0.2.20-3-gabc`) are ahead of the
release they're built on and don't update back to it.

Leaves: `~/Library/Application Support/<name> Shell/` (profile `Chrome/`, engine `Chrome-engine/`, `backend/`,
`update-prefs.json`) and `~/Library/Logs/<name>.log` (no tokens, no notification texts). Failures show as an alert.

## Running it from the repo

```sh
go build -o /tmp/gerdr ./cmd/gerdr
# a debug instance, never the default ~/.gerdr / 7870 (that is the Gerdr in use):
GERDR_UI=$PWD/ui GERDR_DIR=~/.gerdr-dev GERDR_PORT=7871 go run ./tools/chromeshell -gerdr /tmp/gerdr
#   -selftest   exercise everything below over CDP, print, close the windows (the shell then quits)
go run ./tools/chromeshell -test -selftest   # a test page instead of Gerdr
```

Leaves `$TMPDIR/gerdr-chromeshell` (profile), `$TMPDIR/gerdr-chromeshell-engine` (the engine) and
`$TMPDIR/gerdr-chromeshell-icon.icns`.

## How it works

| Piece | File | |
|---|---|---|
| Backend | `backend.go` | the running gerdr's login link; else `gerdr --app --parent-pid`, `GERDR_READY <url>`, SIGTERM + 3s on quit, as desktop/main.js |
| Browser | `main.go` | first of Chrome / Edge / Brave / Chromium; own profile; `--remote-debugging-pipe --no-startup-window` |
| Engine | `engine_darwin.go` | APFS clone of the browser that *is* Gerdr to macOS: its bundle name, id and icon, re-signed ad hoc (below) |
| Windows | `shell.go` `launch` | the engine started again with `--app=<page>`: the running browser opens an app window (bare title bar) |
| Bridge | `bridge.js` | the `window.gerdrNative` desktop/preload.js gives, over a CDP binding; outside links to the default browser; right-click: the browser's menu only in editable fields and on selected text (Electron shows none) |
| Menu bar | `menu.go`, `menu/gerdrmenu.m` | a library loaded into the engine (`DYLD_INSERT_LIBRARIES`) edits Chrome's menu bar into Gerdr's; clicks come back over a unix socket and run as the UI's commands |
| One instance | `main.go` | lock + unix socket in the profile; a second launch asks the first for a window |

Messages handled (same set as desktop/main.js): notify (+ click → focus window, `gerdr.focusSession`), badge
(`Browser.setDockTile`), space, newWindow, open, openPath, webPid, update (answers "doesn't update itself"); menuKeys /
locale are ignored (the menu bar is the browser's). Also: outside links → default browser, page crash → reload, ⌘N / ⌘T
(the browser's new-tab page) → a Gerdr window instead, last window closed → quit.

## Verified (Chrome 156, macOS 26, 2026-11-09), against a real Gerdr (`-selftest`)

- The Dock, ⌘Tab and menu bar say Gerdr exp with Gerdr's icon. **No Chrome icon in the Dock** (only the user's own, if
  their Chrome is open). The window's title bar is bare: traffic lights and title, no "⋮", no translate button.
- Startup: window ~1.5s after launch with a warm engine; the UI then loads as in any browser.
- Bridge in place before the UI's first script; UI renders, `window.gerdr` hooks answer.
- Picked file → its path, read **synchronously** (`getPathForFile`) in the change handler: `@gerdr/chat` and
  `ui/src/lib/native.ts` need no change. Same for drops and pastes (the bridge holds the event, see bridge.js).
- Renderer pid for Settings → Resources: the right one among several (see below).
- Notification shown from the app; a click focuses the window and calls `focusSession`.
- `window.open` / links to other sites → default browser; a second launch → focuses the first; page crash → reloads with
  the bridge; ⌘N's tabbed window → swapped for a Gerdr window; closing the last window → everything exits.
- UI language: `--accept-lang` from the system's (internal/i18n), else a new profile says en-US and the UI shows English.

## Things found the hard way

- **Pages at `http://gerdr/`, not `127.0.0.1:7870`** (main.go `pageHost`): Chrome shows a web notification's origin
  under its title and nothing removes it. `--host-resolver-rules=MAP gerdr 127.0.0.1:<port>` sends the name to the
  backend, `--unsafely-treat-insecure-origin-as-secure=http://gerdr` keeps it a secure origin (notifications, clipboard),
  both only in our profile. The backend's checks hold (WebSocket: Origin == Host). localStorage is per origin, so the
  UI's per-device settings are carried over once from the old origin (storage.go). The shell's own requests and "Open
  in Browser" use the real address.
- **The Dock shows the engine's folder name**, not its CFBundleName: the clone is `<browser> <version>/<name>.app`.
- **Chrome's icon is in Assets.car** (`CFBundleIconName: AppIcon`), which macOS prefers to the `.icns`: replacing
  `app.icns` alone left Chrome's icon. The clone drops `CFBundleIconName`, and is re-registered with LaunchServices
  (`lsregister -f`) since a clone remade at the same path can keep the cached icon.
- **A second Chrome icon in the Dock** is the browser process of our profile, a Foreground app named Google Chrome.
  `lsappinfo setinfo` can't change another process's type, so the browser runs from a copy of its bundle:
  - APFS clone (`cp -c`): ~1.5s, ~0 bytes while it shares blocks with the original. One framework version (~725MB)
    becomes ours once Chrome updates past it, so it is re-cloned per version and old clones deleted.
  - Rename it, don't hide it. `LSUIElement` breaks Chromium (it then thinks it is a helper process and can't find
    `icudtl.dat`, only from the second launch, once LaunchServices registered the bundle; symlinking Chrome's binary /
    Frameworks into our own bundle fails the same way). `LSBackgroundOnly` works, but then the windows need an owner with
    a Dock icon: an installed web app (PWA), whose title bar has Chrome's "⋮" app menu and translate button, which no
    setting removes. Renamed (name, id, icon) the browser is the app itself and its `--app` windows have a bare title bar.
  - Editing the plist breaks Google's signature (SIGKILL): re-sign ad hoc, after stripping xattrs ("detritus").
  - Re-signed = another code identity: `--use-mock-keychain`, or the user's "Chrome Safe Storage" item prompts.
  - First launch of a fresh clone takes ~10s (macOS scanning it), once per Chrome version.
- **CDP can't open an app window** (`Target.createTarget` makes a tabbed browser window). Starting the engine again with
  `--app=<url>` and the same profile hands the URL to the running browser (its process singleton), which opens one.
- **`Runtime.enable` before `Runtime.addBinding`**, or the binding is gone after the next navigation (addBinding succeeds,
  `window.__gerdrPost` is undefined).
- **The bridge can miss a window's first document**: the login link's redirect is decided in the browser process, so the
  page can commit while attach is still adding the script. The shell checks: loaded on our origin without the bridge →
  reload, once.
- **Chrome CHECK-crashes if its menu bar is replaced**: it looks its items up by tag on window activation. The library
  edits Chrome's own bar instead: Chrome's menus stay, hidden, except Edit and Window (trimmed to window items); ours go
  in between. Accessibility (System Events) still lists Chrome's hidden menus, so it is no way to check the result: the
  library logs what is visible with `CHROMESHELL_NAV=1` (`$TMPDIR/gerdrmenu.log`).
- **The Dock icon's menu** is the app delegate's `applicationDockMenu:` (Chrome: New Window, New Incognito Window,
  profiles). The library swaps that method's implementation for one returning ours (spec role "dock").
- **Never `open` the engine to bring it forward**: that is a reopen, Chrome answers it with a new-tab window, the shell
  answered that with focus… a loop that ended with the window closed. The library activates the app from inside.
  A new-tab window (a Dock click) brings up the Gerdr window there is; New Window is the menu's.
- **Orphans**: Chrome doesn't quit when the CDP pipe closes. The library quits it when the shell's socket closes (the
  shell killed with SIGKILL included), and the shell kills it if `Browser.close` didn't take.
- **Which renderer is the page**: CDP has no target → process mapping, and there are more renderers than windows (a
  spare one Chrome keeps warm). The page busy-waits 150ms and the renderer whose CPU time rose is it.
- **Translate**: Chrome guesses the page's language and offers to translate; `translate.enabled: false` in the profile's
  Preferences turns it off (`--disable-features=Translate` doesn't).
- The PWA domain (tried first) is only offered over `--remote-debugging-pipe`; installing needs a manifest reached
  without a redirect, and `Fetch.enable` on the browser session to reach PWA.install's hidden page. Gerdr exp versions
  that installed it are cleaned up with `PWA.uninstall` at start. The backend now serves `/manifest.webmanifest`
  (internal/uisite) anyway, for phones and browsers.
- A just-started backend makes the page reload itself once or twice (the UI runtime's `hello` with a newer build). Not the
  shell; the same in any browser.

## Left before this could replace desktop/ on macOS

- **Updates**: nothing updates itself. desktop/updater.js (backend-only ~10MB downloads, applied when idle) lives in the
  Electron shell; Gerdr exp answers "doesn't update itself", and the backend it uses is updated only while Gerdr.app
  runs. Moving to this shell means moving that into Go (the backend checking, downloading and swapping itself).
- **Menu, not checked**: a real click on our items (the path from the socket on is tested; the click → socket part
  isn't, macOS keeps a background app's menu bar out of reach); whether keys the UI uses (⌘W, ⌘T…) reach the page
  first with Chrome's items hidden.
- **Packaging for real**: Gerdr exp.app is the shape (a launcher kept out of the Dock, the engine is the app). A real
  Gerdr.app would carry the backend too.
- **The engine opened directly** (it is an app in Application Support): it would start a browser on the default Chrome
  profile under Gerdr's name. Unlikely to be found, but it should hand over to the launcher.
- **Updates**: desktop/updater.js's backend-only update has nothing to replace here yet.
- **No Chrome-family browser** (Chrome, Edge, Brave, Chromium are looked for; only Chrome is tested): today an alert.
  Options: download a Chromium on first launch (~150MB once, ours to rename in place, but its security updates are then
  ours), point to the Electron build, or fall back to WKWebView (the problem this started from). Not on APFS / the same
  volume: run plain Chrome and accept its Dock icon.
- Not checked: Edge / Brave as the engine; Chrome updating itself while the clone runs; enterprise-managed Chrome
  (policies can block debugging); window size / place memory across launches.

**Windows** (`tools/winshell`) needs none of this: WebView2 is the system's Chromium (Edge's; on Windows 11, and Windows 10 through
updates, else a ~2MB bootstrapper) and embeds in a window of our own: taskbar icon, name, menu and notifications are
ours without clones, renaming or injecting, and it hands over dropped files' paths itself. That shell isn't written;
this one doesn't build for Windows (flock, the engine and the menu are macOS-only). It would share the backend
connection and the bridge's message handling.
