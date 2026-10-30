# Desktop App updates

Code: `desktop/updater.js` (download, pick the backend, install a whole App), `desktop/main.js` (when to apply),
`desktop/shell-id.sh` + `desktop/feed.sh` (what a release publishes), `cmd/gerdr/attach.go` (the background service).

## Two sizes

The App is an Electron shell (~130MB, rarely changes) around the Go backend (~38MB, ~10MB compressed, carries the UI).
A release (`.github/workflows/release.yml`) publishes, next to the dmgs:

- `gerdr-<v>-darwin-<arch>.br`: the backend alone, brotli (`desktop/compress.js`; ~25% smaller than gzip, Node unpacks it natively).
- `update.json`: version, the **shell id** it was built against, size + sha256 of every package. The App reads it
  from `releases/latest/download/update.json` (a redirect, no API rate limit), via `net.fetch` (system proxy).

Shell id = hash of `main.js preload.js updater.js package.json res/*` (`shell-id.sh`); `build.sh` writes it into the
packaged `package.json` as `gerdrShell`. Same id → download only the backend to
`~/Library/Application Support/Gerdr/backend/<v>/gerdr`, `current.json` names it, and `main.js` runs it instead of
the bundle's (whichever is newer, so installing a newer App by hand wins). Different id → download the dmg, then on a
click copy the new bundle next to the old one and swap them with two renames after quitting (copying straight into
/Applications can be stopped halfway, see Makefile `install`); anything unusual (translocated, run off a dmg,
unwritable dir) → open the dmg instead.

**Touching any file the shell id covers makes the next release a full download for everyone.** Keep shell changes rare,
and keep the shell ↔ page bridge tolerant: an old shell runs new backends (and their UI) for a long time.

Never write into the bundle: it breaks the ad-hoc code signature, and macOS App Management can block it.

Only release builds update (version is exactly x.y.z and `gerdrShell` is set); `make app` builds (`0.2.3-5-gabc`),
`GERDR_BIN` and `GERDR_NO_UPDATE=1` don't. Apps from before v0.2.4 have no updater: they need one manual install.

## When to apply: only when nothing would be cut off

A backend restart kills running turns, pending permission requests, background tasks (memory only) and terminals
(gerdr's child PTYs). Idle sessions don't care: the next message resumes them. `GET /api/busy` counts the four.

- The shell polls it every minute. Backend update waiting + all zero → restart the backend by itself. Otherwise the
  page shows a toast (`window.gerdr.appUpdate`, an older UI without the hook gets a system notification); "update now"
  with something running asks first, offering "when idle".
- A whole-App update always waits for a click (it closes the windows).
- Background service (on by default, docs/dev/SERVICE.md): the App's backend only attaches to it, so restarting that
  does nothing. The attacher (`appAttach`) restarts an outdated service only when `/api/busy` is all zero, checking
  every 10s, unless started with `GERDR_UPGRADE_NOW=1` (the shell sets it when the person chose "now" or it just saw
  idle). If the service runs a different file than ours (a downloaded backend moved it), it rewrites the plist
  (`InstalledExec` ≠ ours → `Install`), since a kickstart would restart the old file.

Local builds follow the same rule: `make install` swaps the bundle under the running App, and `appAttach` re-reads
its own binary's buildID every round, so it notices and restarts the service once idle. Staleness is decided by
`Build` (file mtime + size), not the compiled-in version, which stays the old one in a process whose file was replaced.

Not done: keeping turns alive across a restart. That needs agent processes and PTYs owned by a small long-lived holder
(like a tmux server / containerd-shim) that gerdr reconnects to, plus replaying in-flight protocol state.
