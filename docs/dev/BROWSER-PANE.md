# The Browser pane: each project's own browser beside the chat

Status: first version (2026-11-18). The pane shows the page as a stream of pictures on every device. The desktop
App's native view (below, "Next") isn't built yet.

The ask: an agent on the left and a browser on the right, ideally on the user's own Chrome profile, otherwise a
fresh one, with **one profile per project**.

## What it is

```
Browser pane (ui/src/components/browser/BrowserScreen.tsx: canvas + hidden textarea for keys)
  ⇅ WebSocket GET /api/browser/view?tab=N  (server/browserapi.go → streamPictures, server/screenstream.go)
chromeext.View (internal/chromeext/view.go): the tab's screencast as the picture, input as DevTools Input.*
  ⇅ chromeext.Tab.send
projbrowser (internal/projbrowser): the installed Chrome / Edge / Brave / Chromium, --headless, a DevTools pipe,
  --user-data-dir=<data dir>/projects/<project>/browser
```

- **One profile per project**, a folder under the project's own (`projects/<id>/browser`), so signing in to a site
  once keeps that project signed in. One headless browser per project, started on its first page, stopped after
  10 minutes with none (`idleClose`), and when Gerdr exits. A browser left behind by a Gerdr that crashed holds the
  profile's lock; the next start stops it (`killStale`: the lock's pid).
- **Its pages are ordinary tabs of chromeext** (`Bridge.Adopt`, owned.go): every `gerdr browser` action, the MCP
  server and the console / network logs work on them unchanged. Only the transport differs (`Tab.send`: the
  extension, or the project browser's pipe). Agents get them by default: `gerdr browser open` opens in the
  caller's project (`ResolveSpace`), `--chrome` / `--profile` in the user's Chrome through the extension.
- **The picture uses the remote screen's stream** (Settings → Remote access → Control this computer): only the
  tiles that changed go out, as JPEG, in the same binary format (`internal/screen/diff.go`, `lib/screenStream.ts`).
  `streamPictures` takes a `pictureSource`: the display (`desktopSource`) or a tab (`browserSource`). So the pane works
  the same in the App, a plain browser, over remote access and on a phone.
- **Opening**: an agent's `open` broadcasts `browser_open`, and the UI puts a Browser pane beside that agent's
  pane (`actions/browser.ts browserOpened`, without moving the focus). The user opens one from the pane menu or
  `prefix+i` and types an address. A pane remembers its page's last URL (`PaneLeaf.browser.url`): pages don't
  outlive the backend (every update restarts it), so after a restart the pane offers to open it again. It doesn't
  reopen by itself, because every device showing the layout would open its own copy.

## Why not the obvious ways

- **The user's everyday Chrome profile directly**: impossible while Chrome runs. The profile is locked to that
  process, and since Chrome 136 remote debugging is refused on the default data directory, on purpose: other
  programs mustn't drive the user's logins. Their own Chrome stays reachable through the extension
  (docs/dev/CHROME-EXT.md, `open --chrome`), as separate windows in their Chrome, not in the pane.
- **An iframe**: Gerdr's UI is a web page, and an iframe is the only browser a page can hold. Most sites refuse to be
  framed (`X-Frame-Options` / `frame-ancestors`), cookies without `SameSite=None` aren't sent cross-site (so
  logins break), sign-in pages refuse to run in a frame, and every frame shares one profile.
- **`<webview>`**: an Electron / Chrome Apps element. Chrome Apps are gone outside ChromeOS, and the App runs on the
  user's own Chrome (Lightron), which has no such element.
- **Controlled Frame** (`<controlledframe partition="persist:…">`, the successor): only inside Isolated Web Apps,
  which desktop Chrome enables only with flags. That's the same reason the custom title bar was shelved
  (docs/dev/TITLEBAR.md): the user's Chrome updates itself every ~4 weeks outside our releases, so nothing may rest
  on flags or internals.

## Measured, and why the code is the way it is

All on Chrome 157, macOS.

- **The headless window keeps 87 px for bars it doesn't draw**: a 1280×900 window has an 813 px tall viewport.
  Resizing to the pane adds that difference back, measured from the browser's side (`Browser.getWindowBounds` against
  `Page.getLayoutMetrics`), because `Runtime.evaluate` doesn't answer while the page navigates, and the pane's
  first size arrives exactly then. Only the height: a vertical scrollbar would make the width difference count twice.
- **`HeadlessChrome` in the user agent** marks the page as a bot to many sites: `Network.setUserAgentOverride` with
  the browser's own UA minus "Headless", on every page (popups too).
- **A window under another one stops painting.** Headless windows still cover each other. After the user clicked
  in another page of the same browser, the viewed page got `Page.screencastVisibilityChanged {visible:false}` and
  no more frames, so the pane froze on its last picture while actions on the page still worked.
  `--disable-backgrounding-occluded-windows` (with `--disable-renderer-backgrounding`,
  `--disable-background-timer-throttling`) fixes it. Every page here is on show in some pane.
- **A screencast belongs to the page, not to a viewer**: one viewer's `Page.stopScreencast` stops it for all. A pane
  reconnecting opens the new stream before the old one closes, so the old one's stop landed after the new one's
  start and froze the pane. Fixed by the `caster` (view.go): one per tab, started by the first viewer, stopped by
  the last, start / stop serialized, the count checked again before stopping.
- **A page that opens a new tab hides itself.** A target=_blank link or `window.open` opens a tab in the same
  window; the page that opened it becomes a background tab (`document.visibilityState` "hidden") and gets no more
  screencast frames, so a pane on it froze (seen live: a portal's link to its other domain). The new tab is adopted
  with `Tab.Opener`, a pane showing the opener follows it, and goes back when it closes (`followNewTabs`,
  actions/browser.ts), as a browser's tab strip would. A viewer arriving on a page brings it to the front
  (`Page.bringToFront` in `caster.acquire`), and a page turning visible again restarts the cast. Only on the turn
  from hidden: starting a cast itself reports "visible", so restarting on every one would loop.
- **A navigation to another site can end the screencast** (the renderer process changes): it's started again on
  every main-frame `Page.frameNavigated`.
- **Frames are acknowledged after 30 ms** (`frameGap`): Chrome sends the next only after the ack, so a page that
  animates without pause is sampled at ~30 fps instead of costing a JPEG encode per paint. A still page sends nothing.
- **A viewer on this computer gets Chrome's JPEG as it is** (`View.Raw`, `streamPictures`' raw mode, chosen by
  `!auth.FromRemote`). Diffing tiles meant decoding each frame (40–80 ms at 1400×1800 in Go) and re-encoding a big
  change (30–40 ms), on top of the round trip: the pane felt laggy. Raw, an input reaches a new frame in 2–37 ms
  (`TestLiveInputLatency`), at ~290 KB a frame, free on loopback. Remote viewers keep the tile diff: a keystroke
  is a few KB there instead of a whole frame. A raw frame is "full" only when its size changes, since resizing the
  canvas clears it.
- **Page numbers differ from run to run** (`Bridge.New`: the first adopted id depends on the clock): a pane stores
  its page's number in the layout, and after a restart a new page with the same number appeared in an old pane.
- `--use-mock-keychain`: cookies are encrypted with a key of the profile's own instead of the macOS keychain's. The
  App's Lightron copy (re-signed) uses the same flag. Opening the same profile from that copy later (below) would
  otherwise make the keychain ask the user, or fail to decrypt.
- The DevTools **pipe**, not `--remote-debugging-port`: no other process on the computer can connect and drive a
  browser that holds the project's logins. Windows passes the pipe as inherited handles
  (`--remote-debugging-io-pipes`, launch_windows.go). **Not yet run on Windows.**

Live tests, which drive the real browser: `GERDR_LIVE_BROWSER=1 go test ./internal/projbrowser -v` (open, actions, resize,
navigation across sites, a viewer connecting during the first load, two windows, a second viewer leaving).

## Input

The pane maps the pointer to fractions of the page's viewport (`screen.Input`, as the remote screen does) and adds
what a page needs: `down` / `up` / `move` (drags, hover menus; moves at most every 50 ms unless a button is held),
`scroll` in CSS px (done in the page with `scrollBy` like the `scroll` action, not `Input`'s mouseWheel, which never
answers in a window of the user's Chrome that isn't in front), `compose` (the input method's text before a word is
picked: `Input.imeSetComposition`), `go` / `back` / `forward` / `reload`. The pane's mouse events go to a tab of the
user's Chrome with `quiet` (`Tab.callQuiet` → the extension's `cdp` with `quiet: true`): the extension otherwise draws its
cursor and waits up to ~0.5 s for it to glide to every event (docs/dev/CHROME-EXT.md), which follows the person's own
mouse around and makes every move lag. An agent's click / hover / drag keep the cursor.

Keys: a hidden textarea takes the focus when the picture is clicked. Text, the input method's result and pasted text
come in as text (`Input.insertText`). Named keys (Enter, arrows, F1–F12…) and the editing shortcuts ⌘A / C / X / Z / Y go
as keys (the `key` action, which adds the macOS editing commands). Other ⌘ combinations stay Gerdr's own shortcuts.
⌘C / ⌘X first ask for the selection (`{t:"copy"}` → header `text`) and write it to this device's clipboard: the page's
clipboard is the computer's. ⌘L goes to the address bar. A finger drag scrolls, and a tap clicks.

## The desktop App (macOS): the real page

In the macOS App the pane shows the page itself, not pictures: a window of the App's own browser placed over the
pane. Built 2026-11-18 on Lightron 0.5.0's `App.OpenView` (Lightron's docs/api.md, View).

```
the pane (BrowserScreen, nativeView) ──postMessage {type:"browserView", tab, rect, dpr} / {hide}──▶ the shell
the shell (tools/chromeshell/views.go) ──lightron View.Place / Hide──▶ the window, a child of Gerdr's
the shell ──WebSocket /api/browser/ext, the extension's protocol + "app": true──▶ chromeext (app.go)
projbrowser.Open: the App connected → OpenNative in the project's profile; else headless as before
```

- **One profile, one place** (projbrowser/location.go). With the App installed, a project's profile is a real
  directory of the App's browser data directory (`gerdr-<hash of the project>`, Lightron's `BrowserDataDir`); the App
  opens it as a view's profile, and the headless browser opens the same one while the App is closed
  (`--user-data-dir=<the App's> --profile-directory=…`). Measured, the hard way: a profile linked into a data
  directory from elsewhere never got its cookies on disk (the App's network service had only its own `Default/Cookies`
  open; a headless test with and without the network service in process: "no such table: cookies"). So every
  sign-in in the App was in memory only and gone at the next start. A data directory is one process's at a time:
  a second headless launch on it exits with "SingletonLock: File exists", so the browser itself stops two from
  sharing it. The cost: while the App is closed, one headless browser serves one project's profile at a time
  (another project's page replaces it). The App starting asks the backend to release the directory
  (`POST /api/browser/release`, before Lightron starts the browser; Lightron also waits for any browser on it), and
  the headless browser never kills a browser holding the App's directory (`lockHeld`). Without the App, a project
  has its own data directory under `projects/<p>/browser`; the first App-mode use copies that profile in (`adopt`).
- **One program on both sides.** The headless browser runs the App's own re-signed copy of the browser when the App
  has said where it is (its hello's `engine` and `dataDir`, kept in `<data dir>/browser-engine`; Lightron's
  `App.EnginePath`, `App.BrowserDataDir`), the installed Chrome only without an App. With the real Chrome headless and the copy in the App, Google asked to sign in
  again, with two-step verification, after every hand-over: its device-bound sessions (DBSC) tie a sign-in to the
  program. DBSC is also switched off in both (`noBoundSessions`), so a sign-in is plain cookies either way.
- **Agents drive it the same way**: the shell speaks the extension's protocol (`cdp` → `View.Call`, events from
  `OnEvent`), so its pages are ordinary tabs (`Tab.Native`, ids from 2000000) for every action and the stream.
- **The pane only reports where it is.** Its box's `getBoundingClientRect` and `devicePixelRatio` (so the UI's zoom
  counts), checked on resize, on any change to `<body>`'s children (a Radix menu / dialog portal) and every 250 ms. It hides
  the window while the box isn't shown (another tab, the page hidden) or a menu / dialog / toast of ours overlaps it, since the
  window is above the page. The stream still runs with `pic=0`: no picture, only the address bar's info and its input
  (go / back / reload).
- **Popups**: the browser would open them in an ordinary tabbed window; Lightron closes those and the shell opens the
  URL as another view, reported as a `popup` event: a tab with `Opener`, which the pane follows (`followNewTabs`). The
  new page's `window.opener` is lost, so a sign-in popup that reports back through it won't finish (most fall back to
  a redirect).
- **Out of sight, still awake.** A phone watches and drives the same page while the App's window is behind others,
  minimized or showing something else. Lightron's `View.Hide` veils the window (transparent, clicks through) instead
  of taking it off screen, which made the browser count it hidden and stop painting (the phone saw a frozen page); the
  App's browser runs with `--disable-backgrounding-occluded-windows` (and no renderer / timer backgrounding) so a
  covered window paints too.
- **Limits**: Windows' App (WebView2) has no views yet, so it stays headless + pictures; the window shows for a moment
  where the browser puts a new window before it is taken off screen.
- Measured in the prototype and Lightron's `TestE2EView`: the child window follows moves and minimize exactly; sizing
  goes through `Browser.setWindowBounds`, read back and set again (the browser puts an app window back where it
  remembers it, one run in two).
