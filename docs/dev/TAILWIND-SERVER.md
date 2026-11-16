# Tailwind compiled by the backend (moejs): findings

The UI's styles come from Tailwind's browser build (TAILWIND.md). The question looked into on 2026-11-16: can the
backend compile them instead, with Tailwind's own compiler running in moejs (the pure-Go JS engine Gerdr already
embeds for panels, `internal/work`)? Short answer: yes, and correctly; whether it is worth doing is a separate call,
see the end and docs/dev/todo/tailwind-server.md.

## It runs, and gives Node's output byte for byte

- The npm package `tailwindcss@4.1.11` (the version the browser build pins, `importmap.go`) is the whole compiler:
  `dist/lib.js` is a 231KB CommonJS bundle with no `require` at all, no native code (lightningcss and the Rust file
  scanner live in `@tailwindcss/node` / `@tailwindcss/oxide`, which we don't need). Wrapped in a `module` / `exports`
  shell it loads as a moejs module as is.
- `compile(css, {base, loadStylesheet})` is the API. `loadStylesheet` only has to answer `tailwindcss` and
  `tailwindcss/{preflight,theme,utilities}` with the package's own `index.css` etc. (~55KB); that is all the browser
  build does too. `compile` is async: call it, then read the settled promise with `moejs.PromiseResult` (it never
  waits on a timer, so no event loop is needed).
- `build(candidates)` returns the whole CSS. `@source inline(...)` lines in the input are expanded by `compile` itself,
  so with the backend's scanned class lines (`classes.go`) in the sheet, `build([])` already returns everything.
- **Trap**: the browser build prepends `@import "tailwindcss";` when the sheet has no `@import` (end of its
  `index.global.js`), and our backend strips that line (`browserCSS`, runtime.go). Without it `compile` succeeds and
  `build` returns only the theme variables and `@property` rules, not one utility, no error.
- Input: the stylesheet the running Gerdr served (238KB, ~7900 candidate classes in `@source inline` lines). Output:
  228KB unminified. Same `lib.js` and input in Node 24: byte-identical (`cmp`). Lengths printed by the two differ only
  because Go counts bytes and JS UTF-16 units.

## Timings

M-series Mac, desktop. Ranges are runs on a quiet and a busy machine.

| | moejs | Node 24 (V8, warm) |
|---|--:|--:|
| compile the 231KB tailwind module (once per process) | 15–38ms | – |
| `compile(css)` | 47–80ms (up to ~200 busy) | 13–19ms |
| `build([])`, the whole 228KB | 70–75ms (up to ~165 busy) | ~31ms |
| `build` on a warm compiler, 2 new classes | 27–135ms | – |

moejs is 2–3x slower than V8, and Go's PGO (`moejs/default.pgo`) made no measurable difference. An incremental build
costs nearly a full one: `build` re-serialises the whole sheet each time.

For comparison, what the browser build costs today:

- Page load: its compile went 36→50ms on desktop and 145→190ms with the CPU 4x slowed once the backend started adding
  the scanned classes (TAILWIND.md), plus its ~260KB script (cached after the first load).
- A tab's first visit: ~6% of the busy time at 4x (docs/dev/todo/tab-switches.md).
- In use on the phone: none. Of 11346 long-frame records the perf-probe add-on logged from the Android App (2026-11),
  none had Tailwind in its script attribution.
- Late classes: mostly gone since the backend compiles the source files' classes up front (TAILWIND.md, "Source
  classes are compiled up front").

## What a backend compiler can't see, and how normal builds handle it

The browser build reads class names off the DOM, so it also catches classes no file shows in full. A compiler that
only scans files (ours, or `@tailwindcss/vite` / the CLI in any normal project) can't, and normal builds don't try:

- **Classes in third-party packages**: Tailwind's scanner skips `node_modules`; the package's docs tell you to add a
  path, e.g. Streamdown's README: `@source "../node_modules/streamdown/dist/*.js";`. In Gerdr there is no
  `node_modules`: packages come through `/_esm/` from esm.sh and sit in `~/.gerdr/cache/esm` (`esm.go`), so a path
  `@source` would have to name a package and be resolved to those cached files, then scanned with `extractClasses`.
  Today `browserCSS` drops path `@source` lines (useless to the browser build), and `index.css` keeps a hand-collected
  list of Streamdown's classes instead.
- **Class names built at runtime** (`"w-" + n`): not supported, by rule. Tailwind's docs say to write whole class
  names (a lookup table) or list them with `@source inline(...)`.

So a missing class is never generated at all, where the browser build generates it a frame late. That matters more
for Gerdr than for a normal project: the UI is edited live by users' agents, which can't see the page and today get
away with a built-up class name.

## Verdict (2026-11-16)

Not worth a full replacement now. The late-class problem it would fix is mostly fixed already, the phone shows no
runtime cost, and the remaining gain is part of page load (one compile, ~50ms desktop / ~190ms at 4x). Against it:
reworking hot reload, mounts and the `/_esm` `@source` resolution, ~120ms+ of backend work per class-set change, and
built-up class names silently breaking for agents. If page load on the phone turns out to matter, the hybrid in
docs/dev/todo/tailwind-server.md keeps the agent-friendly fallback.

## Reproducing

`tools/twmoejs` is the experiment: it takes the stylesheet from a running Gerdr page (or a file), compiles and builds
it in moejs a few times, prints the timings and writes the CSS. Its doc comment has the commands; for the Node side,
run the same `dist/lib.js` with the same input and `cmp` the outputs.
