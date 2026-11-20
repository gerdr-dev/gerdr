# The desktop App in two halves: frozen and hot

Status: 2026-11-20, first version. Why: in the two days after the move back to Electron (2026-11-18), eight commits
changed the App's own files, almost all for the Browser pane, and each one made the next release a whole-App download
(~130MB instead of the backend's ~10MB) for everyone (docs/dev/UPDATE.md, `tools/shell-id.sh`). The App's code was
mostly Gerdr's logic that just happened to live in the package.

## The split

```
frozen (the App package; tools/shell-id.sh)           hot (inside the gerdr binary; updates with the backend)
  Electron (package.json's version)                      internal/desktop: the core, `gerdr shell-core`
  desktop/main.js: the loader                              views.go menu.go gerdr.go app.go backend.go el/
  desktop/preload.js: window.gerdrNative                 internal/selfupdate (compiled into the core)
  packaging, installer, icon                             internal/desktop/shell/main.js: the main-process script
```

What stays in the loader is what has to happen before Electron is ready or can never change: the App's name and
directories, the engine switches (`host-rules`, the secure origin, the background flags), the single-instance lock and
the browser-only pid file, starting the core and carrying its lines to the script. Everything else is the script:
windows, the Browser pane's views, menus, Dock, notifications, quitting.

`preload.js` has the page's bridge. Besides the messages it always had, it gives `invoke(name, …)` and `on(name, cb)`
for any `gerdr:<name>` channel, so a new feature in the script needs no change there.

## Starting

1. The loader starts the **bundle's** gerdr as `gerdr shell-core -data … -name …` (`GERDR_SHELL_RESOURCES` says where the
   bundle is: after a handoff the binary isn't in it).
2. The core asks `internal/selfupdate` for the backend to run. A downloaded one newer than the bundle's carries a newer
   core too: the core **hands over** to it (`handoff`: `exec` on macOS, same pid and pipes; on Windows a child on the same
   stdin / stdout, the parent waiting, its pid in `GERDR_HANDOFF_PARENT` so an App install waits for it too).
3. The core that runs writes its embedded script to `<shell data>/shell/<HotID>/main.js` and sends
   `{op: "script", path, hot}` as its first line (`el.Options.ShellJS`).
4. The loader `require`s it and calls `start(ctx)`; the script takes the core's messages (`ctx.onCore`) and sends
   `ready`. From then on it is the old protocol (`internal/desktop/el/el.go`), the loader only carrying lines and replies.

**Fallback.** The core exits before its script ran, sends none within 30 s, or the script throws in `start`: the loader
starts the core again with `-no-handoff`, on the bundle's own binary and script. A second failure shows an error box.
A bad backend update can't leave the App unable to open.

`ctx` is the contract between a frozen loader and scripts newer than it: `electron, appName, shellDir, pkg, port,
browserOnly, dev, preload, state {quitting}, send, onCore, coreAlive, log`. Add to it only in a new App release, and
have the script check for what it needs (an old loader runs new scripts for a long time). The same for Electron APIs:
the script runs on whatever Electron the installed App has.

## Updating

`HotID` (`internal/desktop/hot.go`) is a hash of the hot half's sources, embedded in the binary (`go:embed` of the
package's `*.go`, `el/*.go` and `shell/main.js`): nothing in the build computes or passes it. `gerdr shell-core -hot-id`
prints it.

When an update is applied (`app.go applyUpdate`), the core asks the new binary for its HotID:

- the same: the backend alone restarts, as before;
- another: the App starts again (`relaunch`: `app.relaunch()` + quit), its windows reopen from `window.json`, the new
  core and script run. With the background service the agents carry on; without it (Windows) this only happens when
  nothing is running, as a backend restart. On its own (the idle poll) it waits while a window of the App is in front
  (`focused`), since the person may be typing; a click on "update" doesn't wait.

A whole-App update (a new shell id) is unchanged: download, install on a click.

## What still needs a whole App

Electron's version, the loader, `preload.js` (its shape), `ctx`, the engine switches, packaging / signing / icon. A
feature that needs one of these: add the general hook in a release (as `invoke` / `on` above), then build on it hot.

## Development

- `make build` then `cd desktop && ./node_modules/.bin/electron .` runs the dev loader on `bin/gerdr shell-core`
  (`GERDR_CORE` picks another binary). `GERDR_SHELL_JS=<repo>/internal/desktop/shell/main.js` runs the script from
  the repository instead of the embedded one: edit it, start the dev shell again, no Go build.
- A dev shell of its own beside the App in use: `GERDR_SHELL_NAME="Gerdr shelltest" GERDR_SHELL_DATA=/tmp/x/shell
  GERDR_DIR=/tmp/x/data GERDR_PORT=7997` (another data dir also keeps the background service out of it).
  `GERDR_SHELL_DEBUG_PORT=9333` opens the DevTools protocol to drive / screenshot its pages.
- Handoff without packaging: `GERDR_SHELL_PACKAGED=1 GERDR_SHELL_RESOURCES=<dir with an older gerdr>
  GERDR_APP_VERSION=<its version> <dir>/gerdr shell-core -data <dir with backend/current.json and backend/<v>/gerdr>`;
  the log (`~/Library/Logs/<name>.log`) says "handing the core over".
