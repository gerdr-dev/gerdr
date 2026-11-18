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

## Next: a native view in the desktop App

On the desktop the pane could be the real thing instead of pictures: full fidelity (video, the input method's own
window, file pickers, drag and drop), no encoding. The plan, not built:

- macOS: Lightron already runs our code inside its Chrome (the menu library, `DYLD_INSERT_LIBRARIES`). A borderless
  window of that Chrome, made a child of the Gerdr window there (`-[NSWindow addChildWindow:ordered:]`, public API),
  moves, minimizes and stacks with it, placed over the pane's rect. The pane reports its rect and hides the view
  while a Gerdr menu or dialog would be under it (the same limit as Electron's WebContentsView).
- Windows: a second WebView2 controller in the same window, a `CoreWebView2Profile` per project.
- Prototype (2026-11-18, /tmp: an ad-hoc signed Chrome 157 copy with a test dylib, as Lightron runs it), measured:
  - **A profile per project in the one browser**: launching the engine again with `--user-data-dir=<the same>
    --profile-directory=<project> --app=<url>` hands over to the running instance, which opens an `--app` window in
    that profile (its folder is made and kept). CDP sees every profile's pages (`Target.getTargets`, each with its
    own `browserContextId`), so actions work on them. But `Target.createTarget` only takes the default context:
    another profile's id is "Failed to find browser context". So pages open by the command line, then get attached
    over CDP.
  - **The child window**: `[gerdrWindow addChildWindow:pageWindow ordered:NSWindowAbove]` from the dylib, after
    `styleMask = NSWindowStyleMaskBorderless`, works on Chrome's `NativeWidgetMacNSWindow`. Moving the parent moved
    the child by exactly the same amount; minimizing the parent hid the child and restoring brought it back,
    still attached. Size and place it through CDP `Browser.setWindowBounds` (screen top-left coordinates):
    `-[NSWindow setFrame:]` from the dylib moved it but Chrome put the width back.
  - Not yet tried: fullscreen and Spaces (they take over the screen), the keyboard focus moving between Gerdr and
    the page window, Gerdr's own menus over the pane (expected to need hiding the child while they are open).
- The child window must live in Lightron's process, so a separate browser per project won't do there.
- The headless browser and the App's Chrome can't hold one profile at once: the App would ask the backend to stop
  that project's headless browser before opening the profile, and give it back when it closes. Remote viewers then
  stream the App's window, which `--disable-backgrounding-occluded-windows` keeps painting.
