# Gerdr on nsh: spike

Could Gerdr's interface be drawn by [nsh](https://nsh.dev) (native views, no Chromium) instead of a web page in
Electron? This folder is one chat page (`App.tsx`, ~170 lines) talking to the Gerdr running on this Mac, built to
answer that with measurements instead of docs. nsh 0.6.0, macOS, 2026-11-19.

Run: `nsh run App.tsx` (needs a Gerdr on 127.0.0.1:7870; reads `~/.gerdr/token`).

## What was tried, what happened

| Question | Result |
|---|---|
| Hold Gerdr's `/ws` | **Works.** `websocket.connect(url, { headers: { Authorization } })`; `GET /api/sessions` and `POST …/send` with `fetch`. The v0.1.1 finding "no WebSocket" (herdr-web's WHY-NOT-NSH.md, gerdr's WHY-ELECTRON.md §4) is out of date. |
| A real transcript | **Works.** A 295 KB `events` frame (145 events → 55 rows): JSON.parse + building the rows 1 ms in QuickJS. Agent replies drawn by `TextView markdown` (inline code, paragraphs) natively; streaming chunks append live. |
| Selection across messages | Documented (`<View selectable>` spans Text / TextView in reading order, Copy as text / Markdown); set in the page, not tried by hand yet. |
| Edit the UI while it runs (dev) | **Works.** Every save: rebuilt in 5–20 ms, pushed to the open window, state kept. |
| Change the UI of a *packaged* app | **Works, with a catch.** `nsh package` → 43 MB `.app`; `nsh build --release` of an edited App.tsx (11 ms, 6 KB bytecode) copied over `Contents/Resources/nsh/bundle.nshc` → the app runs the new UI. The catch: that breaks the `.app`'s code signature (`codesign -v`: sealed resource invalid). A real Gerdr would load the bundle from outside the `.app` (`AppView::load(Bundle::…)` in its own Rust host, documented) or keep a dev-server connection (`AppView::connect_dev_server`, feature `dev`) for hot updates with state kept. Not built here. |
| Compiler on the user's machine | Agents' edits need compiling: the `nsh` CLI is 68 MB (no Node). `nsh build` of a TSX app needs only it; `nsh package` with a custom runner needed cargo + 5 min the first time. |

## Round 2: the Code view (App.tsx, Chat.tsx, store.ts, ~700 lines)

Sidebar of recent conversations with status dots, tabs, panes split side by side (`ResizablePanelGroup`, draggable),
each pane a live chat: user bubbles with pasted images and "中途插话 · 19:35", Markdown replies, runs of tool calls
folded into "跑了 3 条命令、读了 1 个文件" (expand to see each with a terminal / file icon, the running one peeks
out with a clock), the status line (pixel orbit, "Forging… 21s · ↓ 754 tokens · esc 打断"), "用时 … " under a
finished turn. Composer: multiline input, ⌘V pastes images (`Input onPaste` gives the bytes), mode / model / effort
`Select`s from the session's option set, context %, send / stop. Followed the web chat's own rules (summarize,
StatusLine, buildRows) so it reads the same.

Found on the way:

- **Gerdr's server pings, nsh's `websocket` module doesn't answer protocol pings**: the socket was dropped every ~70 s
  (server/ws.go readWait) until the page sent `{t:"ping"}` itself, as the web UI does.
- **A module with a side effect (the socket loop) doubles on hot reload**: same trap as the web's sync.ts (its comment
  at line 51). Real code needs a dispose hook; here the window is restarted after editing store.ts.
- Opening a long running session sends one 0.8–0.9 MB frame (the tail back to the turn's start): fine, but a turn
  bigger than 8 MB would hit nsh's per-message cap.
- Lucide icons work as SVG `data:` images; no `currentColor`, so each colour is its own image.
- `Select`'s popup needs a min width and item colour from the app (it wrapped "medium" and drew dark on dark).

## Round 3: the two hard parts

### Composer on `RichEditor` (Chat.tsx `Composer`)

nsh's ProseMirror port with Tiptap's API (the web composer is Tiptap): `starterKit()` + a `suggestion` plugin on
`@` whose query lists the session's files (`GET /api/sessions/:id/files`), ↑↓/Enter/Tab picked through the
plugin's `captureKeys` (its state counts the keys); `onPasteFiles` hands pasted images over with their bytes; Enter
sends through `keymap.bind("enter", …, { when: "always" })` while the editor has focus, skipped while an input
method composes (`onCompositionChange`) or the `@` list is open. Type-checks and renders; typing, pinyin, paste and
`@` were not driven from here (no Accessibility permission to click): to try by hand.

Rough edges: no placeholder prop (an absolute Text over the editor), no JS key handler on the editor itself (a
global keymap binding while it is focused stands in).

### Terminal pane on nsh's `Terminal` (Term.tsx)

Gerdr's own PTY (`POST /api/terms`, `/api/terms/{id}/ws`, binary frames both ways, JSON resize / ping) into nsh's
emulator (Rust, on `vte`). Driven through `POST /api/terms/{id}/input`:

| | |
|---|---|
| ls colours, 16 / 256 / 24-bit colour, bold, underline | right |
| CJK wide characters, emoji, the oh-my-zsh prompt | right |
| vim (alternate screen, syntax colours, status line), back to the shell after `:q` | right |
| herdr (`--session nsh-spike-test`) | draws; looked like its narrow layout at ~60 columns: compare by eye |
| Answers to queries | DA1 `ESC[?62;22c`, DSR 6 yes; kitty keyboard `CSI ? u`, DECRQM (2026 / 1006 / 2004 / 1004), XTVERSION: no answer |

No answer to DECRQM doesn't say a mode is unsupported, only that it can't be asked; kitty keyboard has no answer in
Gerdr's xterm.js 5.5 either. Mouse reporting, keys, pinyin in the terminal: to try by hand.

## Round 4: Android, and the voice add-on

**Same code on Android** (`nsh run --target android`, emulator): one rule for narrow screens (below 700pt one pane,
the sidebar a drawer) and nothing else per platform. Found and fixed on the phone: the composer under the software
keyboard (`env(keyboard-inset-height)`), the bottom safe area added on top of it, colours hard-coded for dark mode,
and a second socket loop after a hot update (`start()` once per VM). Reached the Mac over USB: `adb reverse` for
7870 (Gerdr) and 7899 (`tools/token-helper.py`, loopback only, hands out the token).

**Inside Gerdr's own Android app**, worktree `gerdr.worktrees/nsh-android`, branch `spike/nsh-android`:
`NshSpikeActivity` hosts an `NshView` (nsh-dev.aar, no NDK: the runtime is prebuilt) on `nsh dev` (port 8800) and
gives the page what TSX lacks, over nsh's host channel:

- `mic.start` / `mic.stop`, `mic.pcm` events: AudioRecord, 16 kHz mono PCM, ~100ms chunks as base64.
- `audio.play` / `audio.stop`: MediaPlayer. An embedded `NshView` has no filesystem ("native filesystem unavailable
  on this platform"; nsh opens one only for its own standalone / dev activities, through package-private API), so
  AudioPlayer, which takes a file or URL, can't play synthesized bytes there.

`Voice.tsx` is the add-on's voice, its settings read from Gerdr (`GET /api/addons` → voice): hold 🎤 → Doubao
streaming ASR straight from the page (nsh's WebSocket sends the headers; the add-on had to go through the backend),
the words into the composer; 🔊 reads the latest reply (Azure / Doubao TTS) paragraph by paragraph.

| | |
|---|---|
| 🔊 reading on Android | works: Azure, ~1.3 s per paragraph, played by the app (`dumpsys audio`: MediaPlayer, CONTENT_TYPE_SPEECH) |
| 🎤 chain | works end to end: mic opens, chunks reach TSX, Doubao connects, takes the audio, answers and closes cleanly |
| 🎤 real words | not seen: the emulator recorded silence (peak 0; macOS hasn't given the emulator the microphone). Try on a phone |
| Edit Voice.tsx while the app runs | picked up live (the debug logs above were added that way), no APK rebuild; Kotlin changes need one |

APK size: Gerdr's WebView APK is ~6 MB because the browser is the system's. nsh brings its own runtime:
`libnsh.so` is 50 MB stripped for arm64 (~20 MB compressed), 80 MB with symbols; the debug APK here holds three ABIs
unstripped (227 MB; stripping needs the NDK). A release with only arm64: roughly 50 MB installed / 20 MB to download,
unless nsh offers a build with only the parts an app uses (the Mac package was cut that way, 43 MB).

## Round 5: plugins, scrolling, the phone overview

**Plugins** (Plugins.tsx, plugins/voice/, NshPlugins.kt in the Android worktree): a release nsh app runs no code it
downloads, so a plugin is an nsh app of its own in its own NshView, placed by the host's native component
`gerdr.plugin` (<PluginSlot> in the composer bar). The slot's `ctx` prop goes to the plugin; the plugin asks the UI
through `host.call("gerdr.*")`, relayed by Kotlin; `mic.*` / `audio.*` come from the host. Built bundles are served by
tools/token-helper.py, which also rebuilds a plugin when its source changes; the UI polls the list every 2 s and a new
version is downloaded and run in a fresh NshView within seconds (loading into the same view left the old drawing
under the new). Downloaded code must be `bundle.js`: nsh runs bytecode only from the APK or a signed package. The
voice plugin (🔊 / 🎤) runs on a phone that way, installed beside the released Gerdr (`.nshspike` package id).

**Scrolling while an agent writes** (measured on the phone through the dev server's logs): each chunk rebuilt every
row, so every Markdown was set and laid out again and the content height wobbled ~25 pt; a scroll-to-end queued from
an update (and again 120 ms later) pulled the reader back. Now rows keep their object while unchanged (1 of 139
updates per chunk) and nothing scrolls while a finger is down or a fling runs.

**Phone overview** (Overview.tsx), after Gerdr's OverviewPhone: the agents worth a look (waiting, failed, unread,
running, recent), ordered as Gerdr's lib/order.ts and frozen while open; a strip of status-tinted chips; the agents as
pages moved by a native horizontal drag (`pan={{ x }}` on an animated value, a spring to the page on release), only
the shown one and its neighbours live. First built as a horizontal paging <Scroll>: on Android the chats' vertical
scroll views inside it then got no drags at all (sideways worked, up and down didn't); a pan bound on x only starts
when the finger moves more sideways, leaving vertical drags to the chat. Pages also need a number for their height:
`height: "100%"` inside a horizontal scroll view took the content's height. The selection follows the drag as
colours (each chip's frame interpolated from the same animated value, natively): animated values drive transforms,
opacity and colours only, not width or position, so the web's sliding box of changing width isn't possible without
a JS write per frame. A spread in a style object (`...x()`) is read once, not followed: keys must be written out.

**Flinging through a long conversation** (reported with a screen recording: content jumping back and forth, blank
frames). Measured on the emulator with the same conversation and the same flings, frame by frame (each frame's
vertical shift against the previous, tools: tools/motion.py): with every row in one Scroll the content height
changed 20 times in 4 s (-26 … +2642 pt) as rows above the screen got laid out, nothing held the visible rows still:
4 reversals, 3 blank frames. As an inverted `List` (nsh's chat pattern: newest at item 0, only rows near the screen
made, the visible rows kept in place when rows above change height, following the end by itself): 2 smaller
reversals (+62, +22 px, an estimated row height corrected as it comes in), 0 blank frames. Selection now stays within
one reply (rows are separate cells), no longer across messages.

**The jumps that remained were nsh's** (Android): measured row by row, rows don't grow after they show (29 of 30
laid out once); a slow drag over unmeasured history doesn't jump (0 reversals), a fling does (4). The list anchors
rows measured above the viewport with a ScrollBy, but Android's ScrollView sets its private fling curve's position
every frame, undoing it. Fixed in nsh's ScrollBox (the scroll views run their own fling, shifted by the anchoring):
0 reversals, 0 blank frames in three runs. PR: https://github.com/longbridge/nsh/pull/2642 (tested on 0.6.0's Java
runtime swapped into nsh-dev.aar, worktree ~/dev/bysir/nsh-v060).

Questions asked along the way, answered: QA.md.

## Size / memory (rough, not like for like)

- Packaged spike: 43 MB `.app`, 105 MB RSS showing this conversation. Gerdr's Electron App: ~330 MB, renderers 170–230 MB each. The spike draws far less than Gerdr, so this only says the floor is low.

## Still against it (unchanged by this spike)

- **Browser pane**: nsh's `WebView` is WKWebView on macOS (WebView2 on Windows). The pane exists because Electron can
  put a real Chromium page in the window (docs/dev/WHY-ELECTRON-COMEBACK.md); on nsh it is WebKit, and not the
  headless Chrome agents drive, or the picture stream.
- **A rewrite**: ~35k lines of React TSX → Solid-style (signals, components run once, `<Each>` / `<If>`), add-ons
  (they run in the page), `@gerdr/chat` for outside hosts, the gerdr-ui skill and three-way merge of the user's edits.
- Browser / phone access would need nsh's web target (renders HTML) to be good enough, or keep the React UI too.

## Next, if it goes on

1. A Rust host: loads the TSX bundle from `~/.gerdr/…` (not inside the `.app`), keeps a dev-server link to the
   backend for hot updates; check what signing / notarization allow.
2. Port one real piece end to end (ChatView with tool cards, the composer) and compare against the web one on a long session.
3. nsh's web target serving the same TSX to a browser / phone: does it hold up?
