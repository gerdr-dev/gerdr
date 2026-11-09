// Labs "stop at the start of long text" (readFromTop, ChatList's readCap): while a reply streams in and the chat follows
// the bottom, a text that grows taller than the screen must stop at its own start (READ_GAP from the screen's top),
// leave the bottom, and nothing on screen may move after that, however many rows come in below it.
//   node readtop.mjs [--at="something the user said"] [--tab=1] [--dt=30] [--size=12] [--off] [--height=900] [--back] [--v]
// --back: once it stopped, press the jump-to-bottom button halfway through the rest; from then on that same text must
// not stop it again (it follows the bottom, unless a later text grows taller than the screen).
// Without --at it takes the user message followed by the longest single stretch of agent text. --off runs with the
// switch off (the old behaviour, for comparison: the text's start runs off the top, the chat stays at the bottom).
import { args, launch, open, readEvents, replay, setConfig, streamify, tab, useStore } from "./lib.mjs";

const GAP = 40; // READ_GAP in ChatList.tsx
const a = args({ tab: 1, dt: 30, size: 12, height: 900 });
const { browser, page } = await launch({ height: a.height });
await open(page);
await useStore(page);
await setConfig(page, { readFromTop: !a.off });
await tab(page, a.tab);
await page.waitForTimeout(1500);
const sid = await page.evaluate(() => {
  const w = window.__store.get().layout.workspaces[0];
  return w.tabs.find((t) => t.id === w.activeTab)?.root.sessionId;
});
const all = readEvents(sid);
const isUser = (e) => e.kind === "user" || e.update?.sessionUpdate === "user_message_chunk";
const su = (e) => e.update?.sessionUpdate;
let cut = -1;
if (a.at) cut = all.findIndex((e) => isUser(e) && JSON.stringify(e.content ?? e.update?.content ?? "").includes(a.at));
else {
  let best = 0;
  for (let i = 0; i < all.length; i++) {
    if (!isUser(all[i])) continue;
    let run = 0;
    for (let j = i + 1; j < all.length && !isUser(all[j]); j++) {
      if (su(all[j]) === "agent_message_chunk") run += all[j].update.content?.text?.length ?? 0;
      else if (su(all[j]) === "tool_call") run = 0;
      if (run > best) (best = run), (cut = i);
    }
  }
}
if (cut < 0) throw new Error("no user message to cut at");
let end = all.findIndex((e, i) => i > cut && e.kind === "turn_end");
if (end < 0) end = all.length - 1;
const rest = streamify(all.slice(cut + 1, end + 1), { size: a.size });
console.log(`readFromTop ${a.off ? "off" : "on"}: cut at event ${cut}, streaming ${rest.length} events`);
await page.evaluate(([sid, base]) => window.__store.applyEvents(sid, base, true), [sid, all.slice(0, cut + 1)]);
await page.waitForTimeout(2500);

// Every 50ms: scrollTop, distance from the end, and each mounted row's top relative to the screen
await page.evaluate(() => {
  window.__rt = [];
  window.__rtTimer = setInterval(() => {
    const s = [...document.querySelectorAll("[data-chat-scroller]")].find((x) => !x.closest(".invisible"));
    if (!s) return;
    const st = s.getBoundingClientRect().top;
    const rows = [...s.querySelectorAll("[data-chat-key]")]
      .map((n) => ({ key: n.dataset.chatKey, idx: +n.dataset.index, top: Math.round(n.getBoundingClientRect().top - st), h: Math.round(n.getBoundingClientRect().height) }))
      .sort((x, y) => x.idx - y.idx);
    window.__rt.push({ top: s.scrollTop, end: s.scrollHeight - s.clientHeight - s.scrollTop, ch: s.clientHeight, rows });
  }, 50);
});
let pressedAt = -1;
if (a.back) {
  const n = Math.floor(rest.length / 2);
  await replay(page, sid, rest.slice(0, n), { dt: a.dt });
  pressedAt = await page.evaluate(() => window.__rt.length);
  await page.locator('button[title$="回到底部"], button[title$="the bottom"]').first().click();
  await page.waitForTimeout(800);
  await replay(page, sid, rest.slice(n), { dt: a.dt });
} else await replay(page, sid, rest, { dt: a.dt });
await page.waitForTimeout(800);
const samples = await page.evaluate(() => (clearInterval(window.__rtTimer), window.__rt));
await browser.close();

// The last row that isn't the status line, per sample
const lastOf = (x) => [...x.rows].reverse().find((r) => r.key !== "working");
let bad = 0;
let held = null; // the row it stopped at: { key, top } and the sample index
const byRow = new Map();
samples.forEach((x, i) => {
  const r = lastOf(x);
  if (!r) return;
  const o = byRow.get(r.key) ?? { minTop: Infinity, maxH: 0, firstTop: r.top };
  o.minTop = Math.min(o.minTop, r.top);
  o.maxH = Math.max(o.maxH, r.h);
  byRow.set(r.key, o);
  if (!held && r.h > x.ch - GAP && x.end > 2) held = { key: r.key, top: r.top, i };
  if (a.v) console.log(`  ${i}: end ${Math.round(x.end)} last ${r.key} top ${r.top} h ${r.h}`);
});
if (!a.off) {
  if (!held) {
    console.log("FAIL: no text grew taller than the screen while off the bottom (try --at a longer reply or a smaller --height)");
    bad++;
  } else {
    // From the stop on, the held row may not move on screen
    for (let i = held.i; i < (pressedAt >= 0 ? pressedAt : samples.length); i++) {
      const r = samples[i].rows.find((r) => r.key === held.key);
      if (r && Math.abs(r.top - held.top) > 1) {
        console.log(`FAIL: sample ${i}: held row moved ${held.top} -> ${r.top}`);
        bad++;
        break;
      }
    }
    console.log(`held at ${held.key}, its start ${held.top}px from the screen's top (want ${GAP}), ended ${Math.round(samples.at(-1).end)}px from the bottom`);
    if (Math.abs(held.top - GAP) > 2) bad++;
  }
}
if (a.back && pressedAt >= 0) {
  // After the button's scroll settled (~800ms): the row it held at is past READ_GAP, so following goes on with it
  const after = samples.slice(pressedAt + 20).filter((x) => lastOf(x)?.key === held?.key);
  const off = after.filter((x) => x.end > 2).length;
  console.log(`after ↓: ${after.length} samples while that text was still the last row, ${off} off the bottom`);
  if (off) bad++;
}
for (const [k, o] of byRow) if (o.maxH > 300) console.log(`  row ${k}: tallest ${o.maxH}px, its start went as high as ${o.minTop}px`);
console.log(bad ? "FAIL" : "ok");
process.exit(bad ? 1 : 0);
