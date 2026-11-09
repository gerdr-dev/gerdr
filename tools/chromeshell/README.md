# chromeshell: Gerdr.app on macOS

Gerdr's macOS App: the Go backend plus a small shell that shows the UI on the user's own Chrome (or Edge, Brave,
Chromium) through [Lightron](https://github.com/zbysir/lightron). Same engine as the Electron App it replaced (Chromium,
docs/dev/WHY-ELECTRON.md), none of it shipped: a dmg is ~18MB per arch, most of it the backend.

Lightron does the browser part: the copy of the browser that is Gerdr to macOS (its name and icon in the Dock, ⌘Tab,
the menu bar and notifications), the `--app` windows, the DevTools pipe, the menu library, files' paths, links to the
default browser, one instance. How and why, and what was found the hard way: Lightron's docs/architecture.md. Changes
there that Gerdr needs go into Lightron as general hooks (it has other users), then a Lightron release, then go.mod.

What is Gerdr's lives here:

| | File | |
|---|---|---|
| Startup | `main.go` | flags / Info.plist, the log file, `lightron.Run` with Gerdr's options |
| Backend | `backend.go` | `gerdr --app --parent-pid`, `GERDR_READY <url>`, SIGTERM + 3s on quit, as desktop/main.js |
| Updates | `app.go` | backend-only updates applied when idle, whole-App ones on a click (internal/selfupdate, docs/dev/UPDATE.md) |
| Bridge | `bridge.js` | the `window.gerdrNative` the UI knows (ui/src/lib/native.ts), on `window.lightron` (`Options.Script`) |
| Messages | `gerdr.go` | notify (+ click → the window of that session, `gerdr.focusSession`), badge, space, newWindow, open, openPath, webPid, update, menuKeys, locale |
| Menus | `menu.go` | desktop/main.js's menu bar: the UI's commands (`gerdr.runCommand`) with the keys its settings give them, in its language |

## Building

```sh
tools/chromeshell/package.sh            # → dist/<arch>/Gerdr exp.app, dist/Gerdr-exp-<v>-mac-<arch>.dmg / .zip
NAME=Gerdr ID=io.github.zbysir.gerdr tools/chromeshell/package.sh install   # as the release builds it, into /Applications
```

From the repo, on a debug instance (never the default `~/.gerdr` / 7870, the Gerdr in use):

```sh
go build -o /tmp/gerdr ./cmd/gerdr
GERDR_UI=$PWD/ui GERDR_DIR=~/.gerdr-dev GERDR_PORT=7871 go run ./tools/chromeshell -gerdr /tmp/gerdr -data /tmp/gerdr-cs
```

Opened from Finder it needs no flags. The backend runs **as Gerdr.app runs it: `gerdr --app`**, which attaches to the
Gerdr already serving (the background service) and, when that runs another binary, restarts it on ours **once nothing
is running** (cmd/gerdr appAttach). The backend is started by `Options.URLFunc`, so only once this is the one instance:
a second launch brings the first one's window up and starts nothing.

Leaves `~/Library/Application Support/<name> Shell/` (Lightron's `profile/` and `engine/`, `backend/` downloads,
`update-prefs.json`) and `~/Library/Logs/<name>.log` (no tokens, no notification texts). Shells before Lightron (0.2.24
and earlier) kept the profile in `Chrome/` and the engine in `Chrome-engine/`: the first start moves the profile and
deletes the old engine (main.go moveLayout).

## Things to keep

- **The pages are at `http://gerdr/`** (`Options.Host`), not `127.0.0.1:7870`: Chrome writes the origin under every
  notification. It is also the origin the UI's per-device settings (localStorage) have been kept under since 0.2.23:
  changing it resets them. The shell's own requests and "Open in Browser" use the backend's real address.
- **`ID` is the App's bundle id**: the engine's is `<ID>.engine`, and macOS keeps the notification permission per
  bundle id. Changing it asks people to allow notifications again.
- **Which renderer is the page** (Settings → Resources): the DevTools protocol has no target → process mapping, and
  there are more renderers than windows (a spare one Chrome keeps warm). The page busy-waits 150ms and the renderer
  whose CPU time rose is it (gerdr.go rendererPid, over `App.Call`).
- **Reload / Rescue go through the login link** (`/auth?token=…&next=`): a plain reload of a page the backend refused
  stays refused.
- A second `GERDR_READY` on another port (the gerdr it attached to went away and it took over elsewhere): the
  browser's mapping of `gerdr` is fixed at its start, so the windows follow only once Gerdr is opened again. Rare: the
  port is fixed (7870).
- **No token on a command line**: the first window opens with `--app=<link>`, which any process on the Mac can read
  (`ps`). The link carries a one-time code (`/auth?code=…`, POST /api/auth/code, internal/server/logincode.go: one use,
  within a minute), not the backend's token; later windows open at the same link and get in with the cookie. A
  background service too old to give codes: the token, as before (gerdr.go windowLink).
