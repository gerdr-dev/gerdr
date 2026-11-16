# Tab switches: what's left

Reported: switching tabs felt slow on another person's computer. Measured 2026-11-16 in the debug instance with
`tools/scrolltest/gen.mjs` (synthetic sessions: many images, many videos, long text with tables and tool output) plus
two real 21MB sessions, 5 tabs, keepTabs 6; `tabswitch.mjs` times a switch to the first painted frame of the new chat,
`tabrenders.mjs` counts component renders per switch, `tabprofile.mjs [--first]` writes a CPU profile.

Done, median ms to the new chat painted, M3 with the CPU 4x slowed:

| | before | after |
|---|---|---|
| switch to a kept tab | 330–450 (development React) | 37–52 |
| a tab's first visit in the page | ~800 (development React) | 350–430 (production React; not worked on yet) |

1. App's children are memoised, the rail's project chips are the only part of it reading the layout (`RailProjects`),
   sidebar rows get no per-render callbacks or active tab id (`Row` in UnifiedSidebar.tsx): component renders per
   switch 2221 → 266.
2. Production React is the default (internal/uisite/prod.go; Settings → Labs turns it off per device).

Left:
- A tab's first visit in the page (or a switch to a tab past keepTabs) mounts cold: 100-170ms on the M3, 350-430ms
  with the CPU 4x slowed (up to ~250 / 600 for a 21MB log, whose catch-up alone takes ~230ms at 4x). Nearly all of it
  is the main thread busy, not waiting: the catch-up arrives 35-65ms after the switch and applying it takes a few ms.
  Busy time by what it was doing (`tabfirst.mjs`, 4x, shares of ~520ms with the profiler on):
  React rendering + committing the chat ~35%, the browser's own style / layout / paint ~24%, streamdown turning the
  Markdown into elements ~20%, ChatList measuring rows (forced style + layout) ~16%, Tailwind's browser build ~6%
  (scanning for class names it hasn't seen on the page and rebuilding its sheet; only new names trigger that, so it
  only shows when a lot of new content mounts at once), applying the catch-up ~1%. Cold opens already mount only the
  rows near the bottom (~12 here) before the first paint; every row is heavy though: Markdown blocks, each image with
  its actions popover, each message's action bar with tooltips and a rewind popover (Radix roots mount even closed),
  ~3400 component renders in the 1.5s after a first visit (`tabrenders.mjs --first`), most of them the idle batches
  that measure the rows off screen. Done: popups mount
  their content on first open (`ui/packages/chat/src/ui/opened.ts`: the chat package's Tooltip and DropdownMenu; the
  rewind button's popover mounts on its first click, anchored by virtualRef): component renders in the 1.5s after a
  first visit 3423 -> 2842; first-visit time 5-30% lower per tab in an alternating A/B, within the noise on some (the
  machine had other agents running). Markdown per message is cheap (`mdbench.mjs`, M3, production React: a typical
  880-char reply ~2ms, a 25-row table ~3ms, our plugins +0.3-0.7ms): its share is the number and length of the rows
  mounted before the first paint (a real reply often holds a table and a code block). Done: a cold open at the
  bottom paints only the rows covering the screen and adds the 800px overscan above two frames later (`cold` in
  ChatList.tsx): 11-12 rows mounted for the 2 on screen before (`tabrows.mjs`); first visits at 4x 468 -> 314ms
  (images), 462 -> 366 (videos), 535 -> 380 (a 21MB session), 797 -> 419 (long text), alternating A/B. Idea left:
  cache parsed Markdown per part across mounts (helps remounts: the virtual list, a tab past keepTabs; not a first
  visit, which must parse once anyway).
  Ruled out: Tailwind rewriting an identical sheet (it does, up to three times per first visit, but skipping identical
  writes made no difference outside the noise in an alternating A/B: the rewrites mostly land after the first paint);
  mounting likely tabs ahead of time when idle (35-80ms first visits, turned down: don't mount tabs nobody opened).
- Each ChatList render also mounts a few never-measured rows beside the range to measure them (BATCH).
