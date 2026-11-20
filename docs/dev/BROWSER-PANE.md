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
  pane (`actions/browser.ts browserOpened`, without moving the focus). **Floating** by default, in a corner as
  pictures at a desktop's size (asked for: an agent testing its own work only needs watching, and a docked pane took
  half the screen each time). When the user has to act in the page (sign in, a captcha) the agent runs `gerdr browser
  show` (or `open --show`): `POST /api/browser/show` broadcasts `browser_open` with `show`, and the pane docks as the
  App's real page. A panel's browser does the same (`PanelFloat` in core/SessionBrowser.tsx, over the panel's corner;
  its title bar has the float / dock switch). An agent's `close` (not `--keep`) broadcasts `browser_close` and the
  panes showing the page close with it (`browserClosed`; not one alone in its tab, nor the user's own "close page"). The user opens one from the pane menu or
  `prefix+i` and types an address. A pane remembers its page's last URL (`PaneLeaf.browser.url`): pages don't
  outlive the backend (every update restarts it), so after a restart the pane offers to open it again. It doesn't
  reopen by itself, because every device showing the layout would open its own copy.

- **A panel's agents** (its agent column, the workers it started: `tags.panel`) are in no pane, so `browserOpened`
  has nowhere to put a page. Each page records the session that opened it (`chromeext Tab.Session`, inherited by
  the pages it opens and by its move into the App's browser), and the panel gets a browser column at its right
  (`core/SessionBrowser.tsx PanelBrowser`, opened by itself on the next open, × closes it until then; a third tab on
  a phone). Asked for: one place for the whole panel, not a tab beside each chat (the first cut). Its "open in new
  window" asks the App for a window on `/<project>?browser=<tab>` (internal/desktop `newWindow` with a `path`), which
  App.tsx draws as that page alone (`BrowserWindow`); the App's real page moves there (one window per page).
  It is a move, not a copy (asked for: the window turned to pictures when the column was opened again, and each click
  opened another window): the core keeps which window has which page (internal/desktop browserwin.go, the window's
  page saying so), the column shows a card for a page that is out (show the window / put it back), opening it again
  brings that window forward, and closing the window puts the page back in the column. A Code Browser pane has the
  same button (`components/browser/BrowserOut.tsx`); it can't fold, so it holds the card while the page is out, and it
  stays: a Browser pane closed closes its page (`server/browserpanes.go`).

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
- **A page that fails to load is blank.** Chrome's error page (`chrome-error://chromewebdata/`) has no content in
  the headless browser nor in the App's views, so a dropped connection looked like a page that never drew. The page
  info reads `location.href` with the title (`editableJS`); on the error page `Failed` carries the main document's
  network error from the tab's log (`docFailure`), and the pane draws its own error with a retry (`go` to the
  history entry's address, which keeps the one that failed). The App's real page is kept out of sight meanwhile.
  Until that happens the error page is on screen, white: a flash in a dark theme. So the pane tells the stream its
  background (`bg` input), and a failed document's `Network.loadingFailed` sets it as the page's default background
  (`Emulation.setDefaultBackgroundColorOverride`), before the error page commits; the next document's request clears
  it, since a site without a background of its own is meant to be white. Not for `net::ERR_ABORTED` (a new
  navigation replacing the load: its request came first). The pane also places the page again at once on a failure
  instead of on its 250 ms check.
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
the core (internal/desktop/views.go) ──el View.Place / Hide──▶ a WebContentsView inside Gerdr's window (desktop/main.js)
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
  counts), checked on resize, when a menu / dialog / toast of ours is added anywhere in the document (a Radix portal under `<body>`, the lightbox inside the chat: watching only `<body>`'s children left the page over the lightbox until the next timed check) and every 250 ms. It hides
  the window while the box isn't shown (another tab, the page hidden). The stream still runs with `pic=0`: no picture,
  only the address bar's info and its input (go / back / reload).
- **Our menus / dialogs over the page: a still.** The window is above the UI, so nothing of ours can be drawn over it.
  What counts as ours is a list of selectors (`OVER` in BrowserScreen.tsx): Radix's popups, dialogs, tooltips, toasts,
  and `data-over-page` on anything else (the tab / pane drag layer, which is the whole window so the page steps aside
  for the whole drag; the right-click menu; a rail add-on's panel). Something new drawn over panes needs the attribute,
  set only while shown: a tab dragged over the page was drawn under it until the drag layer had it.
  While one of our menus / dialogs / toasts overlaps the box, the box shows a still of the page (`GET
  /api/browser/shot`, `Page.captureScreenshot`, ~0.1 s) and the window steps aside; it comes back when they close. The
  page is frozen meanwhile (as Claude Desktop does). The still is a canvas drawn from a decoded `ImageBitmap`, not an
  `<img>`: an `<img>` decodes on its own schedule (a big one is left out of the first frame it is in, even when
  another `Image()` decoded the same URL before), so the window stepped aside over an empty box and most menu opens
  flashed black for a frame. Stepping aside must not resize the page either (desktop/main.js `parkView` keeps its
  size): parked at another size, it came back drawn at that size for a frame or two. Tried first and dropped: cutting their shapes out of the window
  (Lightron's `View.Mask`) kept the page live, but clicks in the holes and the keyboard had to be handed between the
  two windows by hand, and each case was a bug of its own (a rename dialog's text box that took no typing).
- **A floating pane shows the stream, of a page at a fixed size.** A Browser pane floated over the others
  (picture-in-picture, `Tab.float`) doesn't place the App's page: it shows the stream's pictures and sends clicks and
  keys through it, as a phone does (`nativeView` is off while floating). Stills every second came first and couldn't
  be clicked. Floating is for shrinking the page into a corner, and a page laid out at the small box's size switched
  to its phone layout; so the page is laid out at a fixed size picked in the pane's ⋯ menu (`FLOAT_FITS` in
  ui/src/lib/layout.ts: pc 1280x800, pad 820x1180, mobile 390x844, the last two as mobile devices) and the box keeps
  that shape within a size range per fit (Panes.tsx `FloatPane`). The size is a device-metrics override set by the
  backend (`POST /api/browser/fit`, chromeext phone.go `Fit`). A remote viewer's phone layout wins over it while one
  looks (at the fit's size the page ran below a phone's pane, keyboard up, and its bottom couldn't be reached). The
  fit ends when the App shows the page again (docked) or the pane says so; not when the pane unmounts (its tab dropped), or the
  first pictures after switching back had the docked shape. Picking another size holds the box's old shape until the
  first picture at the new one is drawn (`onFitDrawn`), then both change in one frame: switched at once, the old
  picture showed in the new box. pad / mobile are also that device to sites: Chrome on an Android tablet / phone as the
  user agent and client hints (`fitUA`, the real engine version), reloaded when the device changes (sites pick their
  page on the request), and the browser's own again, reloaded, once docked; the size alone left sites that go by the
  user agent sending the desktop page. Agents: `gerdr pane float <pane> --fit pad`.
  Two things the stream needed for it: a page that doesn't paint sends no screencast frame when the cast starts (a
  parked App page sat on "hasn't drawn anything yet"), so after 0.5 s without one a screenshot stands in
  (`View.stillFrame`); and one cast feeds every viewer of a page, so it runs at the widest viewer's width (`caster`
  `widest`): it was the last one to ask, and a phone got the floating pane's 960 px pictures, blurry.
- **A page size picked for any pane** (its ⋯ menu, every device; `PaneLeaf.browser.fit`, `gerdr pane fit`): Auto (unset)
  is the pane's own size, and on a phone the phone layout above, with the browser's own user agent (asked for: no
  reload when a phone looks). pc / pad / mobile lay the page out at that size like a floating pane (same `FLOAT_FITS`,
  same user agents). In the App a docked pane keeps the real page (asked for: pictures only for a floating pane or
  another device), as Chrome's device toolbar does: the device's width shrunk to fit by Emulation's `scale`
  (`Fit.Scale`, its height the pane's divided by the scale), centred on a muted ground. The view is placed over exactly
  that area, and the size is sent from the same numbers in the same place (`nativeLayout`, BrowserScreen `report`):
  computed apart, the emulated area was smaller than the view and its unpainted part showed the still kept under the
  page (below). The pane with the real page owns its size: one without a size clears it (a page moved into its own
  window kept the column's scale). A size change drops the still until a fresh one. The pinned size survives the App's
  pane showing it (`desktopShown` only drops a floating box's). Elsewhere it is the
  stream's pictures. Asked for on
  the phone, which had no way to get a site's desktop page. Unlike a floating box's size it is pinned (`Fit.Pin`): it
  wins over a remote viewer's phone layout, since the person on the phone picked it. The layout is shared, so every
  device shows the page at that size; a desktop draws it at most at its own size and within the box, a phone
  width-fit (its keyboard shortens the box) with pinch zoom.
  A phone draws a floating pane like any other (one pane at a time), so there it is a docked pane: its menu has these
  sizes, not the floating box's (Panes.tsx `useFloats`); read from the layout alone, the phone offered only the box's
  sizes, which its own layout wins over, and no pick ever showed. The desktop's floating pane re-sends its box's size
  only when that size changes (BrowserScreen: deps on `fixed`), not when the pane's picked size changes under it, or
  it undid each pick made on the phone. Known gap: the backend keeps one size per page (`phoneLayout.fit`), so the
  desktop's floating pane mounting again still puts its box's size over a pick; two slots (picked wins) would end it.
  While the pictures catch up with a new size, the old one is dimmed under a spinner and "Loading…" (`resizing`).
  Tried and dropped: drawing the App's page at the phone's pixel ratio for sharper pictures (Emulation's
  `deviceScaleFactor` 1.5–3 while a phone looks; the App's page draws at the computer's ratio, 1 on a plain monitor,
  so a phone's page at Auto is soft). With a ratio other than the window's, the App page's screencast fell to 1–4
  frames a second (measured while a phone scrolled: 585x1266 at 1.5x 1–4 fps, the desktop size 12–27, the tablet
  size at 1x 27–47), and scrolling on the phone froze. Also tried and dropped
  (`View.sharpen`): the screencast as is while the page moves, and once it is still 300 ms a screenshot re-rendered
  at the phone's ratio (`Page.captureScreenshot` with `clip.scale`) as the frame. Sharp at rest, but that screenshot
  stops the App page's screencast for 2–3 s (measured: 0 fps scrolling right after it, 31 before and 3 s later), and
  even with the cast restarted right after it (29 fps at once when measured here) the phone still stuttered after
  each sharp still (user recording). Not measured on the phone's own path; a way to run a local viewer as a remote
  one would let the next attempt be measured here first.
- **A device's iframes play it too** (`frameDevice`, phone.go). Overrides are per DevTools target and a cross-site
  iframe is a target of its own: with the page playing an Android phone, an iframe's `navigator` still said MacIntel
  with no touch (measured), and Bing's Cloudflare Turnstile check failed on that (user report). While pad / mobile
  is on, the page auto-attaches its iframes paused (`waitForDebuggerOnStart`), each gets the same user agent and
  touch, attaches its own the same way, and is let go. Non-flattened sessions (`Target.sendMessageToTarget`), so the
  App's shell, which carries only the page's session, needed no change (a full App download for everyone otherwise).
  App pages only. A target is always let go, even if its set-up failed: one left waiting never runs.
- **A pane closed closes its page.** Left open, pages piled up out of sight (each a whole renderer; a video played on
  with no pane to stop it). On every layout save (layout.json's `OnSet`, server/browserpanes.go), whatever closed the
  pane: a page whose pane is gone and that no pane shows is closed, unless the agent session that opened it (`gerdr
  browser open`) is still there, as it may be driving it (it stays in the page list). A pane that only switched to
  another page leaves the old one open, in its list. ⌘⇧T brings the pane back and opens its address again
  (`reopenBrowserPage`).
- **No floating Browser pane over a docked one.** The App's page is a window over the UI, so a docked Browser pane's
  page covered a floating Browser pane in the same tab. A Browser pane doesn't float in a tab with another docked one
  (`floatable`, the server's setFloat), and a floating one is docked back once a docked one comes into its tab,
  whatever did it (a split, the new-session screen's button, a drag, an agent's layout call): one place each side, not
  each way in, `dockFloatsUnderBrowsers` in ui/src/lib/layout.ts (run by setLayout) and server/layoutapi.go (run by
  ApplyLayout). Several docked Browser panes in a tab are fine (each its own window), and so are several floating ones
  (pictures, drawn by us). Merging a tab's Browser panes into one was tried first and dropped: two in a tab is wanted.
  A pane's page list leaves out the pages other panes show (picking one took it from that pane).
- **A page is in one pane.** Picking, in a pane's page list, a page another pane shows swaps the two panes'
  pages (`showBrowserTab`). Two panes on one page read as two pages (the second shows the first's pictures), and a
  reload in either looked like every pane reloading to one address.
- **A page is in one window.** With two App windows on the same project both UIs mount the pane. The last pane to
  place the page has it; the window it was taken from gets `gerdr:browsertaken` and shows the page's pictures (the
  stream) instead, until that pane lets go (`release` on its last hide) or its window closes (`gerdr:browserfree`).
  A hide from a window that doesn't have the page is dropped (internal/desktop views.go `placeView`, desktop/main.js): a
  new window's out-of-sight pane reported "hidden" and the page in the first window went black.
- **The still stays up under the page** (Claude Desktop's "underlay frame", read from its bundle): drawn into the
  canvas each time one is taken (the pointer back from the page, a press, 500 ms after the pointer or the page's
  input went quiet), never hidden while the pane has the page. So when one of ours mounts, the still has been on
  screen for a long time and the page steps aside at once, in main.js on the bridge message itself (not after the
  trip to the core). Put up only as ours mounted, the page had to wait two frames for the still to be on screen
  and covered a dialog's fade-in for 4-6 frames (a shortcut-opened Settings, a recording). What is left is the
  window's views changing, a frame or two.
- **Focus between the two windows** (Lightron's menu.m): a click in the page makes its window key; it shares the
  App window's key state (`_sharesParentKeyState`), so the App doesn't look inactive, and a press on the App's window
  takes the keys back. Two App windows at the same bounds: the host is told apart by a title of its own for a moment
  (it was found by its bounds alone, the newest won, and a click in the page brought the other window forward).
- **Popups**: the browser would open them in an ordinary tabbed window; Lightron closes those and the shell opens the
  URL as another view, reported as a `popup` event: a tab with `Opener`, which the pane follows (`followNewTabs`). The
  new page's `window.opener` is lost, so a sign-in popup that reports back through it won't finish (most fall back to
  a redirect).
- **Out of sight, still awake.** A phone watches and drives the same page while the App's window is behind others,
  minimized or showing something else. Lightron's `View.Hide` veils the window (transparent, clicks through) instead
  of taking it off screen, which made the browser count it hidden and stop painting (the phone saw a frozen page); the
  App's browser runs with `--disable-backgrounding-occluded-windows` (and no renderer / timer backgrounding) so a
  covered window paints too.
- **With the screen locked the screencast goes silent** (Electron, 2026-11-19, measured): a phone drove a page parked in
  its hidden window while the Mac was locked; the page navigated and took typing (`location.href`, a screenshot both
  showed it), but `Page.startScreencast` sent no frame after the first, so the phone kept the picture taken when it
  connected (`stillFrame`) whatever happened. `Page.captureScreenshot` still works then. So after a change (a
  navigation, its load, an input other than a move / press, a new size) an App page whose screencast hasn't spoken
  within 200 ms gets screenshots as its frames, at most every 400 ms for 8 s, stopping once two in a row are the same
  (`View.catchUp`). A live screencast answers well within that, and then nothing extra is taken.
- **Limits**: Windows' App (WebView2) has no views yet, so it stays headless + pictures; the window shows for a moment
  where the browser puts a new window before it is taken off screen.
- Measured in the prototype and Lightron's `TestE2EView`: the child window follows moves and minimize exactly; sizing
  goes through `Browser.setWindowBounds`, read back and set again (the browser puts an app window back where it
  remembers it, one run in two).
