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
- **The phone's full-screen file viewer** (`FileViewer.tsx`): two rewrites landed inside its 150ms fade. At ~50ms
  its own frame classes (`p-0 gap-0 overflow-auto py-2 pr-12`, the close button's): padding / gap animated under
  `duration-200`, and `overflow-auto` made the body its own scroll layer mid-fade; on the phone one frame showed that
  layer bright over the chat with the overlay and the dialog frame gone, text on text. At ~100ms Streamdown's
  heading / code-block classes, reflowing the text. Fix: both sets in `@source inline`. To check a dialog, list the
  selectors each rewrite adds while it opens (diff the generated `<style>`'s rules before / after): nothing may arrive.
  The Streamdown list was collected by rendering a file with every Markdown element; redo it after upgrading it.
- **Transitions added on a state change** (pane zoom in `Panes.tsx`, the overview in `Overview.tsx`): a `transition-*`
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

Measuring: log per `requestAnimationFrame` the element's rect / computed style (`getComputedStyle(el).animationName`,
width, padding) with playwright, as in `tools/scrolltest/`, not by eye. To tie a change to Tailwind, log the
rewrites of its style sheet alongside: a `MutationObserver` on `document` with `childList`, `subtree` and
`characterData`, recording the time and `textContent.length` of `<style>` elements that change. A jump in that
length in the frame things move is Tailwind. rAF runs before ResizeObserver callbacks and paint, so what a rAF sees
isn't necessarily what got drawn (SCROLL.md, "哪一帧画出来了").

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
