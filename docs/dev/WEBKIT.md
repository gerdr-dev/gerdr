# WebKit (iOS Safari, the iOS App's WKWebView): what it does differently

Everything on iOS is WebKit: Safari, the Gerdr iOS App's WKWebView, Chrome and Firefox for iOS. Desktop Safari is
WebKit too. The UI is built and measured mostly in Chromium (the Android App, the desktop shells, headless Chrome in
`tools/scrolltest`), and WebKit differs in ways headless Chrome can't show. Everything below was measured on an
iPhone X (A11, iOS 16.7.7, 60Hz screen) in Safari, 2026-11-14; the problems were felt in the phone overview's page
swipe. `WEBKIT` in `ui/src/lib/swipePager.ts` is how the code tells: `AppleWebKit` in the user agent without
`Chrome` / `Chromium` / `Android` (iOS Chrome says `CriOS`, so it counts as WebKit, as it should).

## What the code does differently on WebKit

### The slide after a swipe runs frame by frame, not as a CSS transition

`useSwipePager` (`place`): on WebKit, a slide (the page turning after the finger lifts, or springing back) moves the
track with a `requestAnimationFrame` loop on the same curve as `EASE` (`ease()`), not with a CSS `transition`.

Why: starting the CSS transform transition cost two long frames on every release (~50ms and ~100ms on the iPhone X),
right when the slide began. WebKit prepares the transitioned layer along its whole path (the track holds several full
chat pages). A drag moving the same track from `touchmove` every frame held 60fps there (17ms frames), so moving it
frame by frame after release too is the cheap way. Chromium keeps the CSS transition: it hands it to the compositor
thread at once.

### The tile switches once the slide has ended

`useSwipePager` (`scheduleSwitch`): turning to another page switches the tile (`go`, a React render of the overview)
a frame after the release in Chromium, but on WebKit only once the slide has ended (`ms + 30`).

Why: the switch is ~100–140ms of React render plus ~75–80ms of style recalc on the iPhone X. With the slide still a
CSS transition, WebKit handed it to the compositor with a later layer commit that the switch held back, so the
transition arrived already over: the page jumped to the next one with no slide (user report). With the slide now
frame by frame (above), the main thread must be free while it runs anyway.

Because the switch now comes late, the pager can't make a new touch wait for it: a finger landing mid-slide grabs the
track and carries on from the page it was heading for (`goal`), and only the page the swipes stop on is switched to.
This holds in Chromium too. The overview's chip strip and Settings' tab strip follow the track any number of pages
ahead of the tile (their `progress` handlers), and Settings mounts two pages either side so quick swipes don't land on
blank pages.

### Fallbacks for APIs WebKit lacks

- `requestIdleCallback`: Safari has none. `ChatList.tsx` (`scheduleBatch`) and `OverviewPhone.tsx` (`onIdle`) fall
  back to timers.
- `scrollend`: missing before Safari 18 (iOS 16 has none). Nothing depends on it now (the native `scroll-snap` pager
  that did was dropped, docs/dev/SCROLL.md).
- `content-visibility`: Safari 18+ only, so it can't be used to keep off-screen pages out of style recalc on an
  iPhone X (iOS 16 is its last).
- Long Animation Frames (`PerformanceObserver` type `long-animation-frame`): Chromium only. On WebKit the perf probe
  sees frame gaps but not which script made them.

## Style recalc is the expensive thing

On the iPhone X a full style recalc of the overview page (1868 elements, 1632 CSS rules) took 86–126ms, about 46µs
per element. Measured by changing a custom property on `<html>` (every element inherits it) and forcing a style read,
with rules taken out one kind at a time: without any CSS 9–10ms; without the 4 `:has()` rules no change; without the
15 universal-selector rules ~10% faster; without `:where()` / `:is()` no change. The cost is spread over all the rules
(mostly Tailwind's), not one bad selector.

So on WebKit, whatever dirties the whole page's style costs a dropped frame or several:

- A custom property set on `<html>` or `<body>` (inherited by everything). `useViewportHeight` (`@gerdr/chat`
  device.ts) writes `--vvh` on `<html>` on every visual-viewport resize and scroll; not changed yet, a suspect if
  keyboard / toolbar changes stutter on iOS.
- Adding, removing or editing any `<style>` element or stylesheet, even writing the same empty text again.
- Changes high in the tree that many rules depend on.

Avoid those during gestures; a React render that changes a few classes deep in the tree only restyles that part.

## How it was measured (and what fooled the measurements)

The iPhone was plugged into another computer running Gerdr (`gerdr host list`: gerdrhomemac); its Safari opened that
computer's Gerdr, so its UI and add-ons live there. `gerdr host exec <host> -- …` runs commands there and
`gerdr host cp` copies files (UI files into its `~/.gerdr/ui`, add-ons into `~/.gerdr/addons`, recordings back).

- **Safari Web Inspector timelines** (the most precise): iPhone Settings → Safari → Advanced → Web Inspector on; on
  the Mac, Safari → Develop → the iPhone → the page → Timelines, record, swipe, Export to JSON. The export holds
  `recording.records` of types `rendering-frame`, `layout` (`recalculate-styles`, `composite`, `paint`,
  `forced-layout`, …) and `script` (`timer-fired`, `microtask-dispatched`, `event-dispatched` with the event type);
  start / end times in seconds. List each long rendering frame with the events inside it. The export also contains
  request cookies: don't paste it anywhere.
  **Recording slows the page**: drag frames came out at 30fps (33 / 66ms) with the main thread idle 70–95% of the
  time, while the same drags measured in-page ran at 60fps. Trust its breakdown of a long frame, not its frame rate.
- **In-page probes as add-ons** (Settings → Add-ons; they load on the device that opens that Gerdr). Keep them lean:
  the perf probe (`~/.gerdr/addons/perf-probe` on the user's main computer) wraps `requestAnimationFrame`,
  `addEventListener`, `getComputedStyle` and `getBoundingClientRect` to time JS, and its A/B mode rewrote a `<style>`
  on every touchstart: on WebKit that is a full style recalc each time (~110ms), and the timeline blamed it on the
  touch. A probe that only counts `requestAnimationFrame` gaps while a finger is down, and per phase after it lifts
  (held / slide 0–400ms / after), measured the real thing and pinned the release stutter. A/B CSS variants have to be
  switched while nothing moves, not at touchstart.
- **The iPhone X screen is 60Hz**: a frame is 16.7ms, so "over 13ms" (a dropped frame at 120Hz, the Android phone) is
  every frame there. Count gaps over 20ms (one dropped) and over 34ms (two).
- **Headless Chromium can't stand in for WebKit**: the per-direction and release stutters didn't show there at all.
  It is still the place to check the logic of a WebKit-only branch: set `WEBKIT` to `true` in swipePager.ts for the
  run, with a script in the style of `tools/scrolltest` that dispatches touches over CDP (turns, quick swipes, a second
  finger) and checks where the track rests and where the chip strip's box is.
