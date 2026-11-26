---
name: gerdr-browser
description: "Working on web pages from inside Gerdr (`gerdr browser`, also as MCP): open a page in the project's own browser, shown to the user in the Browser pane beside the chat and signed in per project, or in the user's own Chrome through the Gerdr browser extension (`--chrome`); an outline of the page with refs, clicking / typing / filling forms with real input, waiting, screenshots, its console and network requests. Use to check a web app or dev server you changed, use a site that needs signing in, or show the user a page. Requires GERDR_ENV=1."
---

# Gerdr: web pages

Only inside Gerdr (the GUI you run in). Check first; if not, say so and stop:

```bash
test "${GERDR_ENV:-}" = 1
```

Commands print JSON; an error is one `{"error": "..."}` line on stderr and exit code 1. `--help` on any command has the
installed version's exact flags.

## Web pages (`gerdr browser`)

Two browsers, the same actions on both:

- **The project's browser** (default): a Chrome Gerdr runs in the background with a profile per project, so a site
  signed in to once stays signed in for that project. A page you open comes up **minimized**: a chip in the corner of
  Gerdr (beside your chat), or the browser button of the panel when you are a panel's agent or one of its workers,
  with a dot saying there is something new; it opens only when the user clicks it, so don't assume they saw it. When the user has to act in it (a sign-in, a captcha, a choice of theirs), run
  `gerdr browser show` first: it docks as the **Browser pane** beside the chat, the real page they can click and type
  in; then ask them, and carry on when they say so. To show the user a page (a dev server you started, a report):
  `open --show`. `close` when done: its pane goes too.
- **The user's own Chrome** (`open --chrome`, or `--profile`): their everyday Chrome through the Gerdr browser
  extension, signed in as they already are (their ChatGPT / Doubao / a company back office). Only when the user asks
  for it or the work needs their own accounts. The extension must be installed and Chrome open (`gerdr browser
  status` shows whether one is connected). Not connected: tell the user to install it from Gerdr's Settings → Browser
  (it links to the Chrome Web Store and shows the connection), and keep Chrome open.

Not for fetching what a plain HTTP request or your own tools can get.

```bash
gerdr browser open http://localhost:5173/  # the project's browser; waits for the page; prints `tab <n>`
gerdr browser show                         # dock it beside the chat for the user to act in (--show on open: at once)
gerdr browser open https://www.doubao.com/chat/ --chrome --why "ask Doubao about X"   # the user's own Chrome
gerdr browser snapshot                     # what's on the page, each thing with a ref: [e12] button "发送"
gerdr browser click e12                    # real mouse events; or x,y from a screenshot
gerdr browser type "你好" --into e8 --submit # real typing, rich editors too; --submit presses Enter
gerdr browser fill e3 "value"              # replace a field / pick an option / check a box (true|false)
gerdr browser key cmd+a Backspace          # keys in turn; Enter, Tab, Escape, Arrow*, cmd/ctrl/shift/alt+…
gerdr browser wait --text "已完成" --timeout 2m   # or --gone / --selector / --url / --load / --for 3s
gerdr browser text                         # URL, title, visible text
gerdr browser screenshot                   # prints a PNG path (kept in ~/.gerdr/screenshots/); look at it with your image reader
gerdr browser scroll down --amount 10      # or scroll e40 (into view), --on e5 (a scrollable panel)
gerdr browser network --filter api         # requests since open; `body <id>` reads a response
gerdr browser console --errors
gerdr browser close                        # its Browser pane closes too; --keep leaves page and pane to the user
```

- The user can also open pages in the Browser pane themselves; `status` lists your project's pages.
- **Each project's browser is its own**: you see and drive only your project's pages (a panel's workers: the panel's
  project). Without `-t`, actions take the last page you opened (else your project's last); another project's page number is refused
  as unknown. In the user's Chrome, a page another project's agent opened is theirs; pages the user opened are everyone's.
- Several Chrome profiles can be connected (`status` lists them, each with the name the user gave it in the
  extension's popup, like "Work"). When the user says which one ("the Work Chrome"), pass `open --profile Work`
  (a name or an id); otherwise `open` picks one that allowed the site. A profile without a name: ask the user to name
  it in the Gerdr extension's popup (toolbar icon) rather than guessing between ids.
- Your project's other conversations share its browser: pass `-t` when you have several pages. Refs come from the latest snapshot; after the page changes,
  take a new one (an old ref that is gone says so). `snapshot --find 发送` narrows a long page.
- Also: `hover`, `drag <from> <to>`, `upload <ref> <files>`, `goto / back / forward / reload`, `eval <js>`,
  `resize 390x844 --mobile`, `dialog accept|dismiss` (the page is paused while an alert / confirm is open), `cdp`.
- `--chrome`: the first time a site is used, the extension pops a window asking the user to Allow: `open` waits (up
  to 10 minutes). Tell the user to look at Chrome. Going to a site that isn't allowed stops the window. Cookies and
  storage are refused by the extension, by design, and tabs the page opens by itself aren't taken over (in the
  project's browser they are: they show up in `status` as new pages).
- The same actions as MCP tools (images come back as images): `gerdr mcp add browser -- gerdr browser mcp`.
- Close the pages you opened when you're done, unless the user is meant to look at them.

