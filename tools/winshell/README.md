# winshell: Gerdr exp for Windows

The Windows counterpart of `tools/chromeshell` (macOS): the UI in **WebView2** (the system's Chromium, Edge's) inside a
window of our own, around the Go backend, through [Lightron](https://github.com/zbysir/lightron) (the window, its icon
and title, DPI, the bridge, links to the default browser, the right-click rule, one instance). No Chromium shipped, and
none of the macOS machinery: WebView2 embeds in our window, so the taskbar icon, name and notifications are ours
without cloning, renaming or injecting.

```sh
tools/winshell/package.sh   # on macOS → tools/winshell/dist/Gerdr-exp-<version>-windows-x64-setup.exe (~13MB)
                            #                              and …-portable.zip (~18MB)
```

Part of gerdr's module: it shares `internal/selfupdate` with the macOS shell. WebView2's packages (`jchv/go-webview2`,
MIT, pure Go, its loader embedded; `jchv/go-winloader`, ISC) only build for Windows. The installer (NSIS, `installer.nsi`) installs per user into `%LOCALAPPDATA%\Programs\Gerdr exp`
(no admin), with Start menu and desktop shortcuts, an entry in Installed apps, and starts it. Data:
`%LOCALAPPDATA%\Gerdr exp\` (WebView2 profile, `log.txt`); sessions stay in `~\.gerdr` (uninstalling keeps both).

**Gerdr's shell is not run on Windows yet**: built and packaged on macOS only; what is under it (Lightron's WebView2
window, its bridge, `Window.Call`, the installer) is, by Lightron's Windows end-to-end test in CI. Below is what the
code does, not what was seen.

## What it does

- The backend: `gerdr.exe --app --parent-pid`, as the Electron shell runs it (no console window: CREATE_NO_WINDOW). It
  attaches to a gerdr already serving the data dir, else serves; it exits with us by itself (no SIGTERM on Windows; it
  waits on our handle). A backend that exits on its own is started again.
- **Updates** (`update.go`, `internal/selfupdate`): the newer of the bundled `gerdr.exe` and a downloaded one runs.
  Every 4 hours (automatic updates on), the release's `update.json` → `gerdr-<v>-win32-x64.br` (the release workflow's
  `windows` job builds it) → `%LOCALAPPDATA%\Gerdr exp\backend\<v>\gerdr.exe`, checked and unpacked. Windows has no
  background service, so applying it restarts our backend: only when `/api/busy` is all zero, or the person says "now"
  (a Yes / No / Cancel box: now / when idle / cancel). Settings → 版本 drives it (no menu bar on Windows).
- One instance (Lightron): a second launch brings the window up. The backend is started by `Options.URLFunc`, so a
  second launch starts none.
- The bridge (`bridge.js`, the `window.gerdrNative` of desktop/preload.js, on `window.lightron`): open (default
  browser), openPath (open / show in Explorer), notify (web Notification, shown by WebView2 as a Windows notification;
  click → window + session; allowed without asking, as the clipboard: Lightron 0.4.2), update. Lightron: outside links → default browser, the window title follows the page's.
- **The pages are at `http://gerdr/`** (`Options.Host`, Lightron 0.4.4), as on macOS: WebView2 writes the origin on
  every notification, and keeps the UI's per-device settings (localStorage) by origin, so a backend on another port
  than 7870 no longer starts them over. Before 0.2.27 they were under `127.0.0.1:7870`: that update starts them over
  once. Where WebView2 ignores the switches that make the name (seen on GitHub's runners), Lightron leaves the pages at
  the real address. A backend restarted on another port is shown at its real address until Gerdr is opened again
  (main.go pageLink).
- Right-click: WebView2's menu only in editable fields and on selected text (same rule as macOS; Lightron).
- High DPI, the window's icon from the PNG in the exe: Lightron.
- The exe's own icon (Explorer) and its manifest (per-monitor v2 DPI, common controls v6): `rsrc_windows_amd64.syso`,
  which go build links in by itself. Checked in, made once with go-winres; again when the icon changes:
  `cd tools/winshell && go run github.com/tc-hib/go-winres@v0.3.3 simply --icon ../../desktop/res/icon.png
  --manifest gui --product-name "Gerdr exp" --file-description "Gerdr exp" --original-filename GerdrExp.exe --arch amd64
  --out rsrc`.
- No WebView2 runtime (rare: Windows 11 has it, Windows 10 gets it with updates): a message, and Microsoft's download page.

## Not in it

- Dropped / picked files' paths (the UI uploads them instead, as in a browser): go-webview2 doesn't reach WebView2's
  `postMessageWithAdditionalObjects`.
- More than one window, a taskbar badge (ITaskbarList3 overlay), remembering the window's size and place.
- Browser shortcuts (F5, Ctrl+P, Ctrl+F…): go-webview2 doesn't expose `AreBrowserAcceleratorKeysEnabled`; the page's
  own handlers can still `preventDefault` them.
- Code signing (SmartScreen will warn).
- Whole-package updates (a new installer, `installApp`) never ran before 0.2.26: PowerShell started with
  `DETACHED_PROCESS` exits without running its script (found in Lightron's `update.Install`).

## Things found the hard way

- **gerdr's backend didn't build for Windows** (so neither did `make win`): `internal/hub/shell.go` used `Setpgid` /
  `syscall.Kill` (now `shell_unix.go` / `shell_windows.go`: a new process group, `taskkill /T` to stop it), and
  dialhome's `relay/store.go` used `syscall.Flock` (fixed in dialhome f1db825: lock_unix.go / lock_windows.go).
- **Homebrew's makensis 3.12 aborts (`std::bad_alloc`) on any script when no locale is set**: package.sh runs it with
  `LC_ALL=en_US.UTF-8`. It also needs bitmap icon entries for the installer's icon (`mkico -bmp`).
