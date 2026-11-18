# The user's own Chrome: Gerdr's browser extension (`browser-extension/`, `internal/chromeext`, `gerdr browser`)

Status: first version (2026-11-17), loaded unpacked; not in the Chrome Web Store yet.

Agents work in the Chrome the user uses every day, signed in as they already are, on the sites the user allowed.
Same design as Annulo's extension (annulo repo, `browser-extension/` and `docs/browser-extension.md`, which has the
reasons: why an extension and not `--remote-debugging-port`, why `debugger`, why native messaging), ported with the
same protocol, so the extension JS can stay one code base with two names. A first try borrowed Annulo's own extension
by taking Annulo's port while Annulo wasn't running; dropped because the two apps fought over the port, other Gerdr
users would need Annulo, and the protocol is Annulo's internal detail.

## How it connects

```
gerdr browser … (CLI) → /api/browser/* (internal/server/browserapi.go)
  → chromeext.Bridge in the Gerdr backend
  ← WebSocket GET /api/browser/ext, opened by the native host (Bearer token, no Origin)
  ← the gerdr program run by Chrome as native host "com.gerdr.browser" (stdio, chromeext.RunHost)
  ← the extension's service worker (chrome.runtime.connectNative)
  → chrome.debugger on windows the extension opened
```

- Native host: Chrome starts the program named in `NativeMessagingHosts/com.gerdr.browser.json` with the extension's
  origin as argv[1]; `main` sees that (`chromeext.IsHostInvocation`) and relays to the running Gerdr, found like the CLI
  finds it (`server.json` + `token` in the data dir), re-read before every reconnect (every 2 s while Gerdr is down).
- Registration (`registerBrowserHost`, cmd/gerdr/browserhost.go): every start writes the manifest for each installed
  Chromium browser (Windows: a file in the data dir + HKCU registry keys). Only the default data dir does it, so a
  `make dev` instance doesn't take the extension from the Gerdr in use. `GERDR_BROWSER_HOST=1` makes a dev instance
  take it: Chrome starts hosts with no environment of ours, so the manifest then points at `<data dir>/browser-host`,
  a launcher that sets `GERDR_DIR`. The next start of the normal Gerdr points it back.
- `ExtensionIDs` (bridge.go) is the manifest's `allowed_origins`. The development id `okkofibhcocmcnafoamflffmkgiphppc`
  is fixed by the `key` in `browser-extension/extension/manifest.json` (public key only; the private key was not kept,
  it is only needed to pack a .crx, which we don't). Add the store id when it is listed.
- Protocol: requests `{id, op, args}` → `{id, ok, result | error, code}`; ops `hello`, `grant` (the extension's Allow
  window, can take minutes), `cancel_grant`, `open`, `cdp` (`{tab, method, params}`), `close`, `release`, `focus`,
  `revoke`; events `hello`, `name`, `sites`, `active`, `cdp`, `closed`, `detached`, `blocked`.

## Limits (in the extension, nothing on our side can widen them)

Only windows it opened; each site needs the user's Allow once per Chrome profile (`open` waits up to 10 minutes);
a page leaving the allowed sites stops the window; only allow-listed DevTools domains (Page, Runtime, DOM, Input,
Emulation, Log, Inspector, CSS, Network, Accessibility, Overlay, DOMSnapshot, Performance), and never cookie / storage
/ interception / download commands. A tab the page itself opens (target=_blank) is not taken over.

## Surface

- `POST /api/browser/act {tab, action, args}` runs one action (`chromeext.Act`, actions.go); `open`, `close`,
  `GET /api/browser` (status) and the native host's `GET /api/browser/ext` are the rest (internal/server/browserapi.go).
- Several profiles: each can be named in the popup (stored in the profile's `chrome.storage.local`, sent in `hello`
  and a `name` event). `status` shows it and `open --profile` / the MCP `open`'s `profile` take a name (any case) or
  an id, so the user can say "use the Work Chrome". Only the user names a profile: an agent can't tell them apart.
- CLI: `gerdr browser status | open | close | snapshot | text | screenshot | click | hover | drag | type | key | fill |
  scroll | wait | goto | back | forward | reload | eval | console | network | body | upload | resize | dialog | cdp`
  (cmd/gerdr/browsercmd.go); `gerdr browser mcp` serves the same as MCP tools (browsermcp.go). The built-in `gerdr`
  skill tells agents.
- Every open window also gets a fake browser-level CDP endpoint (endpoint.go, `ws://127.0.0.1:<random>/devtools/browser/<random>`,
  one client at a time). It answers what chromedp needs plus `setAutoAttach` with flatten; Playwright
  `connectOverCDP` is untested.

## Showing which window is Gerdr's

Like Claude in Chrome: the tab goes into a blue "Gerdr" tab group, and the page gets a glowing frame plus a cursor
that glides to each mouse event before the extension forwards it, with a ripple on a press (overlay.js, sent in with
`Runtime.evaluate` now and `Page.addScriptToEvaluateOnNewDocument` for later pages). It is drawn by the extension, not
by Gerdr, so the fake CDP endpoint's clients get it too. A `cdp` request with `quiet: true` (next to `tab` /
`method`) skips the cursor: for input a person makes themselves, who sees their own pointer and shouldn't wait for
the glide. Kept out of the agent's way: `pointer-events: none`, a closed
shadow root (snapshot walks only open ones), appended to `<html>` (not in `body.innerText`), hidden during
`Page.captureScreenshot`; built with the CSSOM and Web Animations only, since `innerHTML` / `<style>` fail on pages with
Trusted Types or a strict `style-src` (tested). Every overlay call gives up after 700 ms, since a page paused on an
alert never answers. When the debugger goes (release, the user's Cancel, a blocked site) the group is removed and a
content script takes the overlay off the current page (needs `scripting`; fails silently on a site not allowed).

## How the actions work (and what was measured)

Modelled on Claude in Chrome's tools.

- **snapshot** (page.js, run with Runtime.evaluate): walks the DOM (open shadow roots, same-origin iframes) and lists
  what can be acted on (roles from ARIA / tags, `contenteditable`, `onclick`, `tabindex`, and a pointer cursor that
  the parent doesn't have, which catches div buttons) plus structure (headings, landmarks, dialogs, images with alt).
  Refs `eN` map to elements through WeakRefs kept on a non-enumerable `window` property, so a ref survives later
  snapshots while its element is in the page and fails with "take a new snapshot" once it isn't. Chosen over the
  Accessibility domain's tree: one round trip, our own filtering, and the same object the actions use.
- **click / hover / drag** are `Input.dispatchMouseEvent` at the element's centre after scrolling it into view
  (the page gets isTrusted events). The click reports another element covering that point instead of silently
  clicking it.
- **type** is `Input.insertText`; **key** is `Input.dispatchKeyEvent`, with macOS editing commands (`commands:
  ["selectAll"]` for cmd+a etc.) because synthetic key events don't trigger those shortcuts there. Tested on a
  `contenteditable`: type, cmd+a, Backspace, type again leaves only the second text.
- **scroll** is `scrollBy` from inside the page on the scrollable box under the point. Measured: `Input.dispatchMouseEvent`
  `mouseWheel` never answers in a window that isn't in front (the call hit the 2-minute timeout); clicks, keys and
  `Page.captureScreenshot` work there.
- **screenshot** gives an image in CSS pixels, so a point on it is a click x,y. Not with `clip.scale: 1/devicePixelRatio`:
  in a visible window Chrome re-renders the page at that scale on screen for the capture, and the user sees it flash
  smaller (A/B on a 2x screen: every clip+scale capture flickered, no plain one did). The viewport, and an element
  that fits in it, are captured plain and cropped / scaled in Go (`toCSSPixels`); only `--full` and an element larger
  than the viewport still use Chrome's clip, and still flash.
- **console / network / dialog**: Page, Runtime, Network and Log are enabled when the window opens (on about:blank,
  then `location.replace(url)` so the first load is logged and about:blank stays out of the history); their events
  go into a per-window log (tablog.go). While an alert / confirm is open the page is paused and DevTools calls into it
  don't answer: every call fails fast then, and a call that opens one (a click) returns once it is open; `dialog`
  answers it.

## Installing (until it is in the store)

chrome://extensions → Developer mode → Load unpacked → `browser-extension/extension`. Gerdr must have started once
since (it writes the native host manifest); the extension's toolbar popup shows Connected.
`browser-extension/scripts/pack.sh` builds the store zip (without the `key`).

## Known gaps

- Windows opened before Gerdr restarted stay in Chrome (still debugged by the extension) but Gerdr no longer knows
  them; close them by hand.
- Cross-origin iframes aren't in the snapshot (they are separate targets the extension doesn't attach).
- No "find by description" with a model (Claude in Chrome's `find`): `snapshot --find <text>` instead.
