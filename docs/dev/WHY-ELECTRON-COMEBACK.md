# Why the desktop App goes back to Electron

> Decided 2026-11-18, after a day of building the Browser pane's real page on the Lightron shell
> (docs/dev/BROWSER-PANE.md, "Native view"). The story before that: docs/dev/WHY-ELECTRON.md (why Chromium, why
> Electron the first time) and tools/chromeshell/README.md (why we left it in 0.2.21: size).

**Conclusion: the desktop App becomes Electron again, on macOS and on Windows, because a web page shown inside the
App's window is something Electron does natively (`WebContentsView`) and neither current shell can do.** One shell
for both replaces tools/chromeshell (Lightron, macOS) and tools/winshell (WebView2, Windows); each stays until the
Electron one does everything it does. macOS first, Windows right after: same code, its own packaging and checks.

## 1. What we wanted

A Browser pane: agent on the left, a real browser page on the right, one profile per project, logins kept, agents
driving the same page, a phone watching and driving it too. In the App it has to feel like a browser, not like a
video of one: the pictures-over-a-WebSocket version (still what remote viewers and the headless browser get) was
called laggy and blurry the first time it was shown.

## 2. What the Lightron shell had to do, and why it kept breaking

Lightron drives the user's own Chrome over the DevTools protocol. Chrome has no way to put another page inside a
window: every page is a window. So the "view" was **a separate borderless Chrome window glued over the pane**: a
child window of the App's window, placed and sized from the UI's box, through a small Objective‑C library hooking
into Chrome's process from the outside (Lightron's internal/macmenu/objc/menu.m).

To macOS those are two windows, and every rule it has about windows applied to them. Each rule we wanted the other
way was a bug the user hit first, one per round, all in one day:

| Seen | Why (macOS treating them as two windows) | Patch |
|---|---|---|
| Corners of the UI showing round the page, the page trailing a split being dragged | two windows aligned by hand | placement coalescing, read-back of bounds |
| Phone stuck on a frozen page | App window minimized / covered → child counted hidden, painting stops | veil instead of order-out, unhook children before minimize, no occlusion backgrounding |
| Menus / dialogs of ours hidden under the page | a window is above any HTML of the window below | masks cut in the page window (later dropped for stills) |
| Clicks in those holes not reaching the menu | the window server hit-tests by frame, not by mask | per-pointer `ignoresMouseEvents`, presses redirected |
| The App looking inactive after a click in the page | the page window became key and main | private `_sharesParentKeyState` swapped into Chrome's window class |
| No text box of the App taking focus after that | the shared key state kept the keys on the page | key handed back on every press on the App window |
| A rename dialog's box taking no typing | the redirected press didn't move the keys | key handed back on redirected presses too |
| Resize cursors on the page's four edges | Chrome put `Resizable` back on its window | `setStyleMask:` swizzled to strip it |
| A click in window A's page bringing window B forward | the page was found by its bounds, B had the same ones, it became B's child | told apart by a temporary title |
| Raycast's "Paste to Gerdr" landing in the page | on reactivation AppKit gave the keys to the child sharing the key state | keys only from a press in the page |

Every patch was measured and has a test (Lightron's `TestE2EView`), and none of them fixed the cause: the page is
another window. The list wasn't done either. Not yet hit: full screen, Spaces / Mission Control, ⌘\` window cycling,
two displays with different scales, Stage Manager, the input method's candidate window, dragging files into the page,
shortcuts the page eats. Two more things made it worse than its length:

- **Tests didn't catch these first.** Synthesized events take a different path from real ones, app activation from a
  test process is flaky, and the App in use can't be driven by an agent. Most were found by the user.
- **It is private API inside someone else's program**: swizzling methods of Chrome's window class from a library
  loaded into Chrome. Giving one window a subclass of its own (as KVO does) crashed Chrome at once. Any Chrome update
  can change what these hooks rely on.

## 3. What Electron does instead

Electron's `WebContentsView` is **a view inside the App's own window**: one window to macOS, one key window, one
z-order, one minimize; focus between the UI and the page is Chromium's own focus handling inside the window, the same
as between two iframes. Every row of the table above disappears rather than being patched.

Checked on Claude Desktop (installed on the same Mac, Electron; its `app.asar` read 2026-11-18): its built-in browser
is `new WebContentsView({webPreferences: {session, sandbox: true, …}})`, one `session` per partition. Its menus over
the page show a still of the page (the page frozen while a menu is open), the same answer we reached in the end: no
HTML can be drawn over a `WebContentsView` either, but that is the only limit left, and a still covers it.

## 4. What it costs

- **Size**: Electron ships Chromium. The last Electron Gerdr was ~120 MB a download (per arch) against ~18 MB now.
  Measure again with the current Electron before the release.
- **Memory**: +100–200 MB (WHY-ELECTRON.md §5): small next to one Claude Code session.
- **Whole-App updates are big again.** Backend-only updates (internal/selfupdate, docs/dev/UPDATE.md) still apply:
  only a change to the shell (or its Electron version) downloads the whole App.
- **The user's own browser is no longer the engine.** The Lightron shell's point was "your Chrome, none of it
  shipped". With Electron the engine is ours and updates with the App.

## 5. What carries over

Most of the Browser pane is not shell code and stays:

- **The backend side** (internal/chromeext, internal/projbrowser, server/browserapi.go, screenstream.go): the App
  speaks the browser extension's protocol to `/api/browser/ext` with `"app": true` (chromeext/app.go). The Electron
  main process implements the same ops (`open`, `cdp`, `close`, events `cdp` / `popup` / `closed` / `shown`) with
  `webContents.debugger` for the protocol: screencast for the phone, input, the phone layout (`Emulation.*`).
- **The UI**: BrowserScreen's native mode posts its box (`{type: "browserView", tab, rect, dpr}`) and shows a still
  (`/api/browser/shot`; in Electron `webContents.capturePage` would be faster, through the same message) while a menu
  of ours overlaps it. The bridge stays `window.gerdrNative` (ui/src/lib/native.ts), so the UI doesn't change.
- **Everything the Lightron shell does for Gerdr** (tools/chromeshell/README.md): backend start / attach, one-time
  login code instead of a token on a command line, notifications and their clicks, badge, menus with the UI's keys,
  windows per space, self-update, renderer pid for Settings → Resources. The old Electron main.js (git history before
  e1e8efb) did most of these and is the starting point.

## 6. Questions to answer before switching (measure, don't guess)

1. **Logins between the App and the headless browser.** Today the headless browser (App closed) runs the App's own
   engine on the App's profiles, so a sign-in is the same cookies either way (BROWSER-PANE.md "One profile, one
   place"). An Electron partition encrypts cookies with the App's own Keychain item, not Chrome's, so headless Chrome
   can't read it. Likely answer: with the App installed, the "headless" browser is Electron itself run without a
   window (the same binary in a browser-only mode); measure that it keeps cookies and serves the screencast.
2. **The UI's origin.** Per-device settings live in localStorage under `http://gerdr/` (chromeshell README "Things to
   keep"). Electron must serve the UI at the same origin (host mapping) and the first start must carry the settings
   over from the Lightron profile, or every device setting resets once.
3. **The notification permission** is per bundle id: keep `io.github.zbysir.gerdr`.
4. **Size and start time** of the current Electron, signed, per arch.
5. **Windows** (decided: it switches too). WebView2 can't host another profile's page inside its window either, so
   the Browser pane there has been headless + pictures. What to check: the installer (nsis), notifications (the
   AppUserModelID winshell uses, so the permission and the Start menu entry carry over), the backend's quit without
   SIGTERM (WHY-ELECTRON.md §6), and the upgrade from winshell (UPDATE.md).

## 7. Plan

1. Prototype in desktop/: an Electron shell that opens the UI and a `WebContentsView` per Browser pane page, speaking
   the `/api/browser/ext` protocol. Answer §6.1 and §6.4 with it.
2. Port what tools/chromeshell does (its README's table), the Lightron shell kept working meanwhile.
3. Updates: shell id, feed, full-App download path (docs/dev/UPDATE.md) for the switch itself.
4. Switch `make app` / `make install` and the release to it on macOS; the Lightron shell retired (keep Lightron's
   views work as a library feature, it has other users).
5. Windows: the same shell packaged for it (`make win`), winshell retired, its upgrade path checked on a real
   Windows machine.

When to look at this again: if Chrome (or Lightron) ever gets a real way to embed a page in a window, or if size
becomes the constraint that matters most.
