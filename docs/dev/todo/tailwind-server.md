# Compile the UI's Tailwind in the backend, not the browser

**Why it might be worth it**: the page would get finished CSS in its HTML: no ~260KB Tailwind browser build to load,
no compile at page load (~50ms desktop, ~190ms with the CPU 4x slowed), no class arriving a frame late, and none of
the browser build's silent `@source inline` traps (TAILWIND.md). It also opens `@import` of package CSS and `@plugin`,
which the browser build refuses (`browserCSS`, runtime.go).

**Why it's postponed** (2026-11-16): the findings are in docs/dev/TAILWIND-SERVER.md. Tailwind's compiler runs in
moejs and matches Node byte for byte, but the gain left is part of page load, the phone shows no runtime cost, and a
backend-only compiler stops built-up class names (`"w-" + n`) from working, which the user's agents rely on without
knowing it.

**Pick it up when** a measurement on a phone shows Tailwind's page-load compile worth removing (first: log the browser
build's own compile / build time at page load through the perf-probe add-on, the "start" record has none today), or
when the UI needs `@plugin` / a package's CSS via `@import`.

## How

A hybrid, so nothing that works today stops working:

1. **Compile in the backend.** Load `tailwindcss`'s `dist/lib.js` (same version as `tailwindBrowser` in
   `importmap.go`, embedded with its `index.css` / `theme.css` / `preflight.css` / `utilities.css`) into one moejs
   runtime per Renderer, as `tools/twmoejs` does. Prepend `@import "tailwindcss";` when the sheet has none. Keep the
   compiler while the CSS text is unchanged; call `build` with the scanned classes (`classes.go`, or keep them as
   `@source inline` lines). Cache the output on disk by hash of (Tailwind version, CSS, class set): the backend then
   compiles only when the set of classes changes, ~120ms or more in moejs.
2. **Serve it** as a plain `<style>` (or a cached `/_ui/...css` URL) instead of `<style type="text/tailwindcss">`,
   and push the new CSS on hot reload where `runtime.go` now swaps the style element's text.
3. **Third-party packages.** Keep path `@source` lines instead of dropping them, let them name a package
   (`@source "streamdown"`), resolve it to the files the `/_esm` cache holds for the version in the import map, and
   scan those with `extractClasses`. Then replace index.css's hand-made Streamdown list with that one line.
4. **Fallback for classes no file shows.** A small client script (not the 260KB build) watches for class names
   missing from the generated set and asks the backend to build them (`build(candidates)` on the warm compiler, then
   append). Those few arrive late, as all of them do today; everything else is there on the first frame. In
   `make dev`, also log each one, so built-up class names in our own code get found and written out.
5. **Mounts** (panels and add-ons, `mounts.go`) share the page's one compiler and class set, as they do now.

## Check before calling it done

- Diff the generated rules against what the browser build produces on the same pages (dialogs, menus, Markdown, a
  panel): nothing missing, nothing extra that changes the look.
- `tools/scrolltest/` scripts (SCROLL.md) still pass; the chat's two hidden frames on first open (`ChatList.tsx`) may
  then be removable.
- Time a cold page load on the Android App before and after.
- Update TAILWIND.md, the built-in gerdr-ui skill and ui/AGENTS.md: what agents may write (whole class names, path
  `@source` for packages) changes.
