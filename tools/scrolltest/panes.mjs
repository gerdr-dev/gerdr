// Switching panes on a phone: only the focused pane of a side-by-side split is drawn there, so every switch remounts
// the chat list from scratch. Checks that the remounted list shows up already at the bottom and doesn't jump.
//   node panes.mjs [--rounds=4] [--latency=0] [--cpu=1] [--settle=2500] [--running] [--stream] [--ro]
// Needs >= 2 tabs (setup.mjs with two sessions): merges tab 2 into tab 1 as a right-hand split, then flips focus.
// --latency=ms delays every /api request, like the phone reaching gerdr over the LAN / relay.
// --cpu=N slows the CPU down N times (CDP throttling): a phone takes several frames to mount and measure what a laptop
//   does in one, and that's where the remount goes wrong.
// --running: cut both sessions right after their last user message and mark them running (a turn just sent, the agent
//   working on it), which is what the chat looked like in the reported case. --stream: also feed that turn back in
//   while switching, chunk by chunk, as if the agent were still writing.
// --ro: judge the frames a late ResizeObserver sees (what actually gets painted) instead of the rAF ones, which run
//   before layout corrections in ResizeObservers (docs/dev/SCROLL.md "哪一帧画出来了").
import { analyze, args, fromBottom, launch, open, readEvents, replay, report, startFrameLog, stopFrameLog, streamify, useStore } from "./lib.mjs";

const a = args({ rounds: 4, latency: 0, settle: 2500 });
const { browser, ctx, page } = await launch({ width: 400, height: 860, dpr: 3, scrollbars: false, ...(a.video ? { video: a.video } : {}) });
if (a.latency) await page.route("**/api/**", (r) => setTimeout(() => r.continue().catch(() => {}), a.latency));
await open(page);
await useStore(page);
if (a.cpu > 1) await (await ctx.newCDPSession(page)).send("Emulation.setCPUThrottlingRate", { rate: a.cpu });
const actionsURL = await page.evaluate(() => performance.getEntriesByType("resource").map((e) => e.name).find((n) => /\/src\/lib\/actions\.ts/.test(n)));
await page.evaluate(async (u) => (window.__actions = await import(u)), actionsURL);
const panes = await page.evaluate(() => {
  const l = window.__store.get().layout;
  const ws = l.workspaces.find((w) => w.id === l.active);
  if (ws.tabs.length >= 2) window.__actions.mergeTab(ws.tabs[1].id, ws.tabs[0].id, undefined, "right");
  const t = window.__store.get().layout.workspaces.find((w) => w.id === l.active).tabs[0];
  const ids = [];
  const walk = (n) => (n.type === "pane" ? ids.push(n.id) : (walk(n.a), walk(n.b)));
  walk(t.root);
  return ids;
});
if (panes.length < 2) throw new Error("need two panes: run setup.mjs with two sessions");
const isUser = (e) => e.kind === "user" || e.update?.sessionUpdate === "user_message_chunk";
const tails = {};
if (a.running || a.stream) {
  const sids = await page.evaluate(() => {
    const l = window.__store.get().layout;
    const t = l.workspaces.find((w) => w.id === l.active).tabs[0];
    const out = {};
    const walk = (n) => (n.type === "pane" ? (out[n.id] = n.sessionId) : (walk(n.a), walk(n.b)));
    walk(t.root);
    return out;
  });
  for (const [pane, sid] of Object.entries(sids)) {
    const all = readEvents(sid);
    let cut = all.length - 1;
    while (cut > 0 && !isUser(all[cut])) cut--;
    tails[pane] = { sid, rest: streamify(all.slice(cut + 1).filter((e) => e.kind !== "turn_end"), { size: 8 }) };
    await page.evaluate(([sid, base]) => {
      window.__store.applyEvents(sid, base, true);
      window.__store.set((st) => ({ sessions: { ...st.sessions, [sid]: { ...st.sessions[sid], status: "running" } } }));
    }, [sid, all.slice(0, cut + 1)]);
  }
}
await page.keyboard.press("Escape"); // the phone pane switcher may be open
await page.waitForTimeout(1500);

let bad = 0;
for (let r = 0; r < a.rounds; r++) {
  const to = panes[(r + 1) % panes.length];
  await startFrameLog(page);
  await page.evaluate((id) => window.__actions.focusPane(id), to);
  // The ResizeObserver logger attaches to the scroller present when logging starts; the new pane's list only exists after
  // the switch, so restart logging once it's mounted.
  if (a.ro) await page.waitForTimeout(50), await startFrameLog(page, { ro: true });
  if (a.stream && tails[to]) await replay(page, tails[to].sid, tails[to].rest.splice(0, 40), { dt: 20 });
  await page.waitForTimeout(a.settle);
  const { frames: raf, ro } = await stopFrameLog(page);
  const frames = a.ro ? ro : raf;
  const fb = (await fromBottom(page)).find((x) => x.visible);
  // Frames where the list is visible but not at the bottom yet: what the user sees as "opens mid-way, then jumps down".
  const off = frames.filter((f) => !f.hidden && f.mounted && f.sh - f.ch - f.st > 2);
  const t0 = frames[0]?.t ?? 0;
  const firstShown = frames.find((f) => !f.hidden && f.mounted);
  const hiddenN = frames.filter((f) => f.hidden).length;
  console.log(`== round ${r + 1} (pane ${to}): ${frames.length} frames, hidden ${hiddenN}, first visible at ${firstShown ? firstShown.t - t0 : "-"}ms (st=${firstShown?.st} sh=${firstShown?.sh}), visible-but-not-at-bottom ${off.length}, ends ${fb?.fromBottom}px from bottom`);
  for (const f of off.slice(0, 6)) console.log(`   t=${f.t - frames[0].t}ms  st=${f.st} sh=${f.sh} ch=${f.ch}  ${f.sh - f.ch - f.st}px from bottom`);
  bad += report(analyze(frames));
  if (off.length) bad++;
  if (fb && fb.fromBottom > 2) bad++;
}
if (a.video) await ctx.close();
await browser.close();
process.exit(bad ? 1 : 0);
