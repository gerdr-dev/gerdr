# Tailwind runs in the browser: its traps

The UI has no build step, so its styles come from Tailwind's browser build (`@tailwindcss/browser@4.1.11`, loaded by
`internal/uisite/html.go`, version in `importmap.go`). It reads `src/index.css` from `<style id="__gerdr_css__">`,
watches the DOM, and writes the CSS for each class once it first shows up on the page. Two consequences, both silent:
CSS a frame late, and CSS that never works at all. Every item below was measured, not guessed.

## A class's CSS arrives a frame late

The browser build generates a class only after it appears in the DOM, and its style sheet rewrite lands after the
commit that mounted the element: the element is laid out and usually painted without that class for a frame or more
(more on a phone). Whatever mounts with classes nobody on the page has used yet starts unstyled, then changes.

Seen so far:

- **Dialogs and sheets**: a dialog with a width nobody used yet (`sm:max-w-3xl`) drew its first frame at the fallback
  width and shrank under the zoom-in; a sheet sat mid-screen, then jumped right; `duration-200` without a
  transition-property animated a padding compiled late in from 0. Popups (menus, selects, tooltips, popovers) showed
  a first frame unanimated at full size, then restarted the animation.
  Fix: `@source inline(...)` in `ui/src/index.css` compiles those classes up front. Add a dialog size there when a
  dialog uses a new one.
- **The file viewer** (`FileViewer.tsx`): two rewrites landed inside its fade. At ~50ms its own frame classes
  (`p-0 gap-0 overflow-auto py-2 pr-12`, the close button's): padding / gap animated under `duration-200`. At ~100ms
  Streamdown's heading / code-block classes, reflowing the text. Fix: both sets in `@source inline`. To check a dialog,
  list the selectors each rewrite adds while it opens (diff the generated `<style>`'s rules before / after): nothing
  may arrive. The Streamdown list was collected by rendering a file with every Markdown element; redo it after
  upgrading it. (This was first blamed for a phone flash it didn't cause, see the next section.)
- **Transitions added on a state change** (pane zoom in `Panes.tsx`, the overview in `OverviewGrid.tsx`): a `transition-*`
  class added in the same change that moves the box is compiled one style change too late, so the first zoom of a page
  load jumped instead of animating. Fix: the transition is an inline style.
- **The chat list's first open** (`ChatList.tsx`, docs/dev/SCROLL.md): on a page load every mounted row resized one
  frame after the first paint (29 rows, the chat 320px shorter, everything on screen moved), at the moment the style
  sheet grew by 9KB. Fix: the first chat opened in a page is hidden for two frames; later opens find the CSS already
  there and show from the first frame. Hiding every open instead showed a blank chat for two frames on each session
  switch on a phone.

Picking a fix: classes known in advance → `@source inline(...)`; a style that must hold on the very change that adds
it → inline style; a whole subtree of unpredictable classes (Markdown, tool output) → hide only until the page's
first one has rendered, never on every mount.

### Source classes are compiled up front (backend)

Since the fixes above were written, the backend scans every local source file the page can load (the module graph,
lazy imports included) for candidate classes and appends them to the served CSS as `@source inline(...)` lines
(`internal/uisite/classes.go`). Loose on purpose, like Tailwind's own file scanner: every word in a string literal
that could be a class, junk included (Tailwind drops what isn't a class). The words are cached with each file's compile
result, so an edit re-scans one file, and the list is sorted, so the CSS changes (and the browser recompiles) only
when the set of classes does. Measured on gerdr's UI: ~2800 candidates; the page-load compile went 36→50ms on desktop
and 145→190ms at 4x CPU throttling; the stylesheet went 51→152KB in one write at load, with no rewrites afterwards
while opening dialogs and menus.

So most of the hand-written lists in `index.css` are now redundant (dialog / sheet / popup classes live in local
files) but harmless. What the scan can't see still arrives late and still needs the fixes above: classes from
third-party packages (Streamdown's Markdown elements, keep that list) and classes assembled at runtime (`"w-" + n`).
The chat list's two hidden frames (`ChatList.tsx`) were not re-measured; check with `tools/scrolltest/` before dropping them.

Traps of the browser build's `@source inline` (4.1.11, each silent: the rest of that line's classes just aren't
generated):

- **One word with an unclosed `(` / `[`** (`min(42rem,` from a JS expression in a template literal) drops every class
  after it in the same `@source inline`.
- **One lone `'`** (the apostrophe in UI text like `Can't`) does the same.
- Quotes, backslashes and braces can't appear at all (they end the string / are brace expansion).

`extractClasses` filters all of these, and the list is split into lines of 200 so anything it misses costs one line,
not the UI. To check: take the generated lines from the served page, append a marker class (`pb-[7.25px]`) to each,
and see whether it's generated; bisect a failing line down to the word. The scan results sit in the on-disk compile
cache: changing the extraction rules means bumping `compileCacheVersion` (modules.go), or old files keep old results.

Measuring: log per `requestAnimationFrame` the element's rect / computed style (`getComputedStyle(el).animationName`,
width, padding) with playwright, as in `tools/scrolltest/`, not by eye. To tie a change to Tailwind, log the
rewrites of its style sheet alongside: a `MutationObserver` on `document` with `childList`, `subtree` and
`characterData`, recording the time and `textContent.length` of `<style>` elements that change. A jump in that
length in the frame things move is Tailwind. rAF runs before ResizeObserver callbacks and paint, so what a rAF sees
isn't necessarily what got drawn (SCROLL.md, "哪一帧画出来了").

## Looks like Tailwind, isn't: one bad frame when an animation ends (phone)

Opening any file in the viewer on the Android app flashed one frame: the chat at full brightness with only the
viewer's text on top, text on text; the overlay and the dialog's background gone, the next frame fine. It looked like
Tailwind's late CSS and wasn't: precompiling every class that arrived late (above) left it exactly as it was.

The cause is compositing. While an `animate-in` runs, Chromium gives the animated box (overlay, dialog frame) its own
compositing layer. When the animation ends the layer is dropped and the box repainted into the layer under it; the
phone composited one frame before that repaint landed. What still showed was what had a layer of its own anyway: the
chat underneath, and the viewer's scroll box (an `overflow-auto` scroller is composited by itself). Fix
(`ui/src/components/ui/dialog.tsx` `keepLayer`, also used by `sheet.tsx`; a68a156, not yet confirmed on the phone
when written: if it still flashes, update this): `will-change: opacity` on the overlay and
content for as long as they're open, so the layers don't change when the animation ends. Inline, not a class (a class
compiled a frame late changes the layers mid-animation); opacity, not transform (a transform layer blurs dialog text
in WebKit). Any other box that animates in over a large area and then sits still needs the same if a phone shows it.

How it was found, and what didn't work:

- **Time the bad frame against the animation.** Pull every frame of the user's screen recording, not a resampled
  rate: `ffmpeg -ss <t> -t 0.6 -i rec.mp4 -fps_mode passthrough out/%04d.png` (phone recordings are variable rate;
  `-vf fps=8` skipped the bad frame or blended it with its neighbours). The bad frame came ~200ms after the fade
  started, after several fully opaque frames: the content's `duration-200` ending (the overlay's 150ms didn't show).
  A Tailwind rewrite would land at 40–100ms, inside the fade.
- **Read the bad frame for what's missing.** Missing overlay + missing background + one sub-box still there = layers,
  not styles. A late class shows a wrong size, padding or font, not a box dropping out.
- **Desktop Chrome can't show it**, throttled or not: rAF logs, `getComputedStyle` opacity and CDP screencast frames
  (`Page.startScreencast`, compositor frames) were all clean. A reduced repro (keyframes in a sheet rewritten
  mid-animation, main thread blocked) was clean too. CDP `LayerTree` snapshots didn't update after the first one and
  showed no animation reasons; don't rely on them for this. Only the phone shows it: confirm a fix there (the user's
  screen recording, or `adb` with remote debugging).
- **Wrong turns**: turning the viewer's animation off on phones (hides it for one dialog; the cause stays for every
  other); blaming Tailwind's stylesheet rewrite restarting the composited fade (never measured, and the timing is
  wrong).

## CSS that silently does nothing

The browser build compiles a stylesheet, not a project: it can't follow `@import` (except `tailwindcss`, bundled) or
load `@plugin`, and it reports nothing when it gives up.

- **Imports**: code can't import CSS, and `src/index.css` can't `@import` a package. A relative `@import "./x.css"` of
  a file inside `src/` is inlined by the backend (`internal/uisite/cssimport.go`); `browserCSS` (`runtime.go`)
  drops the rest with a warning (`uisite.go`). That is how `src/animate.css` gets in.
- **The text `@import` inside a CSS comment fails the whole stylesheet** (4.1.11): the page renders with no styles at
  all, nothing in the console. Found by bisecting the served CSS; a comment explaining the import was enough.
  `browserCSS` rewrites `@import` inside comments to `import` (`TestBrowserCSSDefusesImportInComments`). `@plugin` /
  `@property` in a comment are harmless.
- **Classes from a plugin that isn't loaded are no-ops, not errors.** shadcn's components were copied in with
  `data-[state=open]:animate-in zoom-in-95 …` while tw-animate-css never was, so no dialog, menu or popover ever
  animated and nobody noticed. A class present in the DOM proves nothing: check the computed style
  (`animationName` was `none`). tw-animate-css is now copied in as `src/animate.css`.
- **`@property` must be top level.** tw-animate-css's source nests its `@property` rules in `:root {}`; its own build
  hoists them, plain CSS doesn't allow them there. `animate.css` has them hoisted: redo that when updating it.
- **An older backend serving a newer ui/** drops `@import "./animate.css"` with a warning: everything works but
  nothing animates until the backend is upgraded. Keep it that way: nothing may depend on animate.css to function.
- Path `@source` is pointless (it scans the page, not files) and is dropped; `@source inline(...)` is kept (above).
