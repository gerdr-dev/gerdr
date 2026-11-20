// The phone's overview, after Gerdr's (ui/src/components/OverviewPhone.tsx): the agents that need a look, one page
// each, side by side; a sideways swipe turns the page (dragged under the finger, not a cut). On top, a strip of
// chips, one per agent, tinted by status, in "look at first" order; the selection box slides from chip to chip
// with the drag.
//
// What nsh does natively here: the pages follow the finger through a native drag bound to an animated value (`pan`,
// horizontal only), and the chips' frames follow that value too: no JS per frame (the web version writes DOM styles
// from a requestAnimationFrame loop). Not a horizontal paging <Scroll>: with the chats' vertical scroll views inside
// it, vertical drags reached neither on Android (measured: no drag start on the chat's scroll view at all). A pan with
// only `x` bound begins only when the finger moves more sideways than up or down, so vertical drags stay the chat's.
import { signal, computed, effect, untracked, createAnimatedValue, windowSize, Text, View, Scroll } from "nsh";
import type { AnimatedValue } from "nsh";
import type { ScrollRef } from "nsh";
import { sessions, unread } from "./store";
import type { Session } from "./store";
import { Chat, sessionTitle } from "./Chat";

// ---- which agents, in what order (Gerdr's lib/order.ts) ----

/** 等你 0 > 出错 1 > 跑完没看 2 > 在跑 3 > 闲着 4 */
const rank = (s: Session, u: Record<string, true>) =>
  s.status === "waiting" ? 0 : s.status === "error" ? 1 : u[s.id] ? 2 : s.status === "running" ? 3 : 4;
/** Within a rank, the newer first: a running one by when its turn began (stable while it runs), others by updatedAt */
const since = (s: Session) => (s.status === "running" && s.turn ? s.turn.startedAt : s.updatedAt);

const RECENT = 3 * 3600_000;
const MAX = 12;
/** Opened by hand (the drawer) though not active: stays in the overview */
const added = signal<string[]>([]);

/** The agents worth a page: waiting, failed, unread or running, anything touched in the last 3 hours, and those added */
const activeIds = computed(() => {
  const u = unread();
  const now = Date.now();
  const auto = sessions()
    .filter((s) => s.status === "waiting" || s.status === "error" || s.status === "running" || u[s.id] || now - s.updatedAt < RECENT)
    .sort((a, b) => rank(a, u) - rank(b, u) || since(b) - since(a))
    .slice(0, MAX)
    .map((s) => s.id);
  // Picked by hand: always there, past the cap too (picking one beyond it from the drawer turned nothing)
  return [...auto, ...added().filter((id) => !auto.includes(id) && sessions().some((s) => s.id === id))];
});

// Sorted once, then held (Gerdr's phoneOrder): re-sorting live moved the strip under the finger and a swipe landed
// somewhere unexpected. Those gone drop out, newcomers go on the end.
const frozen = signal<string[]>([]);
// Follows the active list only: the order it held is read untracked. Reading `frozen` tracked made the effect depend
// on what it writes, and on a hot update the old and the new module's effects rewrote it in turn: "potential infinite
// loop: an effect keeps changing what it reads" (seen on a phone).
effect(() => {
  const ids = activeIds();
  const was = untracked(frozen);
  const kept = was.filter((id) => ids.includes(id));
  const next = [...kept, ...ids.filter((id) => !kept.includes(id))];
  if (next.join() !== was.join()) frozen.set(next);
});
export const overviewIds = frozen;

/** The page shown */
export const focusId = signal("");
/** Pages watched (their logs streamed): the shown one and its neighbours, as Gerdr's phonePages */
export const overviewPages = computed(() => {
  const ids = frozen();
  const i = Math.max(0, ids.indexOf(focusId()));
  return ids.slice(Math.max(0, i - 1), i + 2);
});

/** The pages' horizontal position: 0 for the first page, -width for the second… The drag writes it natively. */
const pagerX = createAnimatedValue(0);
/** Moves the pages to `id`'s, sliding (a swipe's settle) or at once (a jump from the strip or the drawer). */
function goTo(id: string, slide: boolean, velocity = 0) {
  const i = frozen().indexOf(id);
  if (i < 0) return;
  const to = -i * windowSize().width;
  // Only `velocity`: an option the spring doesn't take arrived as undefined ("Error converting from js 'undefined'
  // into type 'f64'")
  if (slide) pagerX.spring(to, { velocity: Number.isFinite(velocity) ? velocity : 0 });
  else pagerX.set(to);
}
/** Shows `id`: from the drawer, a chip, or a notification. Not in the list yet: it joins it. */
export function focusSession(id: string) {
  if (!frozen().includes(id)) added.set([...added(), id]);
  focusId.set(id);
  jump.set(id);
}
/** A jump asked for (drawer, chip): done once the page is in the list, which may take the list's effect a moment */
const jump = signal("");
effect(() => {
  const id = jump();
  if (id && frozen().includes(id)) {
    goTo(id, false);
    jump.set("");
  }
});

// ---- the chips ----

const TINT: Record<string, string> = { bad: "rgba(239,68,68,0.15)", warn: "rgba(245,158,11,0.15)", ok: "rgba(34,197,94,0.15)" };
const tint = (s: Session | undefined, u: boolean) =>
  !s ? undefined : s.status === "waiting" || s.status === "error" ? TINT.bad : s.status === "running" ? TINT.warn : u ? TINT.ok : undefined;
const DOT: Record<string, string> = { waiting: "#ef4444", error: "#ef4444", running: "#f59e0b" };

// The selection follows the drag as colours: a chip's frame and text are full at its own page's offset and fade out
// towards the neighbours', so while a page is dragged one chip's frame fades as the next one's comes in. Colours,
// because an animated value drives transforms, opacity and colours natively, not width or position (a sliding box of
// changing width, as on the web, would need a JS write per frame).
function Chip(props: { id: string; index: number; width: number; offset: AnimatedValue; current: boolean; onPress: () => void; onLayout: (x: number, w: number) => void }) {
  const s = computed(() => sessions().find((x) => x.id === props.id));
  const u = computed(() => !!unread()[props.id]);
  // pagerX is -index * width at this chip's page; inputRange must increase
  const around = () => [-(props.index + 1) * props.width, -props.index * props.width, -(props.index - 1) * props.width];
  const frame = () => props.offset.interpolate({ inputRange: around(), outputRange: ["rgba(59,130,246,0)", "rgba(59,130,246,1)", "rgba(59,130,246,0)"], extrapolate: "clamp" });
  return (
    <View
      onPress={props.onPress}
      onLayout={(e) => props.onLayout(e.bounds.origin.x, e.bounds.size.width)}
      class="flex-row items-center gap-1.5 rounded-md px-2 active:opacity-70"
      style={{ height: 32, maxWidth: 150, borderWidth: 1.5, borderColor: frame(), backgroundColor: tint(s(), u()) ?? "transparent" } as any}
    >
      <Text class="text-foreground text-[12px] truncate" style={{ flexShrink: 1, fontWeight: props.current ? "600" : "400" } as any}>
        {sessionTitle(s())}
      </Text>
      <If when={DOT[s()?.status ?? ""] || u()}>
        <View style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: DOT[s()?.status ?? ""] ?? "#22c55e" }} />
      </If>
    </View>
  );
}

// ---- the overview ----

export function Overview(props: { onMenu: () => void }) {
  const width = computed(() => windowSize().width);
  const page = computed(() => Math.max(0, frozen().indexOf(focusId())));
  // The pager's own height, measured: pages get it as a number. With `height: "100%"` a page in a horizontal scroll
  // view took its content's height, so the chat inside it was as tall as its whole transcript and its own vertical
  // scroll view had nothing to scroll (reported: sideways swipes worked, up and down didn't).
  const pagerH = signal(0);
  // Where each chip is in the strip, measured: the selection box is placed between them by the pager's offset
  const chipBox = signal<Record<string, { x: number; w: number }>>({});
  let strip: ScrollRef | undefined;

  // The first agent, until one is picked; the shown one gone: its neighbour
  effect(() => {
    const ids = frozen();
    if (ids.length && !ids.includes(focusId())) focusId.set(ids[Math.min(page(), ids.length - 1)] ?? ids[0]);
  });
  // The list changed (one joined from the drawer, one left): pages moved, so the position follows the shown one.
  // Without it the pager stayed where it was and showed another page's placeholder while the chip said otherwise.
  effect(() => {
    frozen();
    untracked(() => goTo(focusId(), false));
  });
  // The strip keeps the current chip in view
  effect(() => {
    const b = chipBox()[focusId()];
    if (b) strip?.scrollTo({ x: Math.max(0, b.x - 60), animated: true });
  });


  return (
    <View class="flex-1">
      <View class="flex-row items-center gap-1 border-b border-border px-1.5" style={{ height: 44 }}>
        <View onPress={props.onMenu} class="rounded-md px-2 py-1 active:opacity-60">
          <Text class="text-foreground text-[16px]">☰</Text>
        </View>
        <Scroll ref={(r) => (strip = r)} horizontal scrollbar={false} class="flex-1">
          <View class="flex-row items-center gap-1" style={{ height: 44 }}>
            <Each of={frozen()} key={(id) => id}>
              {(id, i) => (
                <Chip
                  id={id()}
                  index={i()}
                  width={width()}
                  offset={pagerX}
                  current={id() === focusId()}
                  onPress={() => focusSession(id())}
                  onLayout={(x, w) => {
                    const old = chipBox()[id()];
                    if (!old || old.x !== x || old.w !== w) chipBox.set({ ...chipBox(), [id()]: { x, w } });
                  }}
                />
              )}
            </Each>
          </View>
        </Scroll>
      </View>
      <If
        when={frozen().length}
        else={
          <View class="flex-1 items-center justify-center">
            <Text class="text-muted-foreground text-[13px]">没有进行中的 agent，从 ☰ 里选一个</Text>
          </View>
        }
      >
        <View
          class="flex-1 overflow-hidden"
          onLayout={(e) => {
            pagerH.set(e.bounds.size.height);
            goTo(focusId(), false);
          }}
          pan={{ x: pagerX }}
          // Where it settles: the page the release points to (position plus a bit of its speed), one page at most
          onPanEnd={(e) => {
            const w = width();
            const projected = (e.value?.x ?? 0) + e.velocity.x * 0.15;
            const i = Math.max(0, Math.min(frozen().length - 1, Math.max(page() - 1, Math.min(page() + 1, Math.round(-projected / w)))));
            const id = frozen()[i];
            if (!id) return;
            focusId.set(id);
            goTo(id, true, e.velocity.x);
          }}
        >
          <View class="flex-row" style={{ height: pagerH(), width: width() * frozen().length, translate: [pagerX, 0] } as any}>
            <Each of={frozen()} key={(id) => id}>
              {(id, i) => (
                <View style={{ width: width(), height: pagerH() }}>
                  {/* Only the shown page and its neighbours are live chats: the others cost nothing until near */}
                  <If when={Math.abs(i() - page()) <= 1} else={<PagePlaceholder id={id()} />}>
                    <Chat id={id()} />
                  </If>
                </View>
              )}
            </Each>
          </View>
        </View>
      </If>
    </View>
  );
}

function PagePlaceholder(props: { id: string }) {
  const s = computed(() => sessions().find((x) => x.id === props.id));
  return (
    <View class="flex-1 items-center justify-center">
      <Text class="text-muted-foreground text-[13px]">{sessionTitle(s())}</Text>
    </View>
  );
}
