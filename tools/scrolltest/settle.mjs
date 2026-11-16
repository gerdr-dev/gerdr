// A chat opened cold at the bottom must not move while the rows off screen get measured in the background (the batches
// ChatList mounts when idle, scheduleBatch). Pinned to the bottom with no new output, no row on screen may move and
// none may be drawn over another, in any painted frame, and the content's height may barely change (the scrollbar
// thumb jumps with it: measuring the rows off screen in batches, each estimated at 60px, made it jump ~25 times).
//   node settle.mjs [--tab=3] [--x=1640] [--y=500] [--width=1864] [--height=1050] [--watch=6000] [--cpu=1] [--reload] [--up=150] [--nudge [--frac]]
// --tab: the tab to switch to (its chat opens cold: not kept yet); --x/--y: a point inside the chat to watch (split
// panes); --reload: open the page on that tab instead of switching to it.
import { analyze, args, launch, open, report, startFrameLog, stopFrameLog, useStore } from "./lib.mjs";

const a = args({ maxHeightChanges: 5, tab: 3, x: 1640, y: 500, width: 1864, height: 1050, watch: 6000, cpu: 1 });
const { browser, page } = await launch({ width: a.width, height: a.height });
await open(page);
await useStore(page);
if (a.cpu > 1) await (await page.context().newCDPSession(page)).send("Emulation.setCPUThrottlingRate", { rate: a.cpu });
await page.evaluate(async () => {
  const u = performance.getEntriesByType("resource").map((e) => e.name).find((n) => /\/src\/lib\/actions\/tabs\.ts/.test(n));
  window.__tabs = await import(u);
});
if (a.reload) {
  // Like a page reload with the whole transcript cached: switch to the tab, load every older segment (scrolled to the
  // top until there are none), back to the bottom, then reload and watch from the page's first frame.
  await page.evaluate((i) => window.__tabs.selectTab(i), a.tab - 1);
  await page.waitForTimeout(1500);
  for (let k = 0; k < 40; k++) {
    const more = await page.evaluate(([x, y]) => {
      const sc = document.elementFromPoint(x, y)?.closest("[data-chat-scroller]");
      if (!sc) return false;
      sc.scrollTop = 0;
      return [...Object.values(window.__store.get().older)].some((v) => v === "more" || v === "loading");
    }, [a.x, a.y]);
    await page.waitForTimeout(500);
    if (!more) break;
  }
  await page.evaluate(([x, y]) => { const sc = document.elementFromPoint(x, y)?.closest("[data-chat-scroller]"); sc.scrollTop = sc.scrollHeight; }, [a.x, a.y]);
  await page.waitForTimeout(1500);
  await page.addInitScript(([x, y]) => {
    const go = () => (document.body ? window.__startFrameLog({ point: [x, y] }) : requestAnimationFrame(go));
    requestAnimationFrame(go);
  }, [a.x, a.y]);
  await page.reload();
  await page.waitForSelector("[data-chat-scroller]");
  await useStore(page);
  await page.waitForTimeout(400);
} else {
  await startFrameLog(page, { point: [a.x, a.y], ro: false });
  await page.evaluate((i) => window.__tabs.selectTab(i), a.tab - 1);
  await page.waitForTimeout(400);
}
// The painted frames too: a ResizeObserver on the new chat, created after ChatList's (SCROLL.md "哪一帧画出来了")
await page.evaluate(([x, y]) => {
  const sc = document.elementFromPoint(x, y)?.closest("[data-chat-scroller]");
  window.__roFrames = [];
  if (!sc) return;
  const snap = () => {
    const top0 = sc.getBoundingClientRect().top, bot0 = top0 + sc.clientHeight, rows = {};
    for (const it of sc.querySelectorAll("[data-item-index]")) { const r = it.getBoundingClientRect(); if (r.bottom > top0 && r.top < bot0) rows[it.dataset.itemIndex] = [+(r.top - top0).toFixed(2), +r.height.toFixed(1)]; }
    return { t: Math.round(performance.now()), st: Math.round(sc.scrollTop), sh: sc.scrollHeight, ch: sc.clientHeight, mounted: sc.querySelectorAll("[data-item-index]").length, rows };
  };
  const o = new ResizeObserver(() => window.__roFrames.push(snap()));
  o.observe(sc.querySelector("[data-chat-list]")); o.observe(sc);
}, [a.x, a.y]);
// What the chat's session does meanwhile: older segments loaded in front (store.older), rows in the transcript
const info = await page.evaluate(([x, y]) => {
  const sc = document.elementFromPoint(x, y)?.closest("[data-chat-scroller]");
  const pane = sc?.closest("[data-pane-id]")?.dataset.paneId;
  const st = window.__store.get();
  let sid;
  for (const w of st.layout.workspaces) for (const t of w.tabs) (function walk(n) { if (n.type === "pane") { if (n.id === pane) sid = n.sessionId; } else { walk(n.a); walk(n.b); } })(t.root);
  window.__olderLog = [];
  window.__store.useStore.subscribe((s, p) => { if (s.older[sid] !== p.older[sid]) window.__olderLog.push([Math.round(performance.now()), s.older[sid]]); });
  return { width: sc?.clientWidth, sid };
}, [a.x, a.y]);
console.log(`watching ${info.sid} at ${info.width}px wide`);
// --up: scroll up by wheel this soon after the open (ms), while the rows off screen are still being measured
if (a.up !== undefined) {
  await page.waitForTimeout(a.up);
  await page.mouse.move(a.x, a.y);
  for (let k = 0; k < 30; k++) (await page.mouse.wheel(0, -120), await page.waitForTimeout(16));
}
await page.waitForTimeout(a.watch);
console.log("older-segment state changes [ms, state]:", JSON.stringify(await page.evaluate(() => window.__olderLog)));
let nudged = null;
// Frames before the nudges: the height changes those make on purpose don't count
const nudgeAt = await page.evaluate(() => performance.now());
// --nudge: a row above the screen grows by half a pixel (as a row measured off screen does): the text at the bottom
// must not move at all (the content's whole-pixel total, ChatList's layout)
if (a.nudge) {
  nudged = await page.evaluate(async ([x, y, frac]) => {
    const sc = document.elementFromPoint(x, y)?.closest("[data-chat-scroller]");
    // --frac: the chat box a fraction of a pixel tall (a panel's task drawer was 792.5px): the end is fractional then
    if (frac) {
      sc.style.height = sc.clientHeight - 0.5 + "px";
      await new Promise((ok) => requestAnimationFrame(() => requestAnimationFrame(ok)));
    }
    const box = sc.getBoundingClientRect();
    const above = [...sc.querySelectorAll("[data-item-index]:not([data-measure-only])")].filter((r) => r.getBoundingClientRect().bottom < box.top);
    const out = [];
    // Grow by half a pixel, then back (a shrink: the browser clamps scrollTop to the real end then), three times
    const r = above[above.length - 1];
    const shot = () => [...sc.querySelectorAll("[data-item-index]")].filter((e) => { const b = e.getBoundingClientRect(); return b.bottom > box.top && b.top < box.bottom; }).map((e) => e.getBoundingClientRect().top);
    for (const pad of [0.5, 0, 0.5, 0, 0.5, 0]) {
      const before = shot();
      r.firstElementChild.style.paddingBottom = pad + "px";
      await new Promise((ok) => requestAnimationFrame(() => requestAnimationFrame(ok)));
      const after = shot();
      out.push(Math.max(...after.map((t, i) => Math.abs(t - before[i]))));
      window.__ends = (window.__ends || []).concat([[sc.scrollTop, sc.scrollHeight - parseFloat(getComputedStyle(sc).height)]]);
    }
    return out.map((d) => +d.toFixed(2));
  }, [a.x, a.y, !!a.frac]);
  console.log("half a pixel more, then less, above the screen, 3 times: rows on screen moved by", JSON.stringify(nudged));
  console.log("  [scrollTop, real end] after each:", JSON.stringify(await page.evaluate(() => window.__ends)));
}
const { frames } = await stopFrameLog(page);
const ro = await page.evaluate(() => window.__roFrames);

// Movement of rows on screen between consecutive frames of the same scroller, when it sits at the bottom
function moves(fs, label) {
  let bad = 0;
  const lines = [];
  for (let i = 1; i < fs.length; i++) {
    const p = fs[i - 1], c = fs[i];
    if (p.sc !== undefined && p.sc !== c.sc) continue;
    if (c.hidden) continue;
    const common = Object.keys(c.rows).filter((k) => k in p.rows);
    if (!common.length) continue;
    // Sub-pixel too (--min, default 0.2px): half a pixel up and down on every height change reads as shaking text
    const ds = common.map((k) => +(c.rows[k][0] - p.rows[k][0]).toFixed(2)).filter((d) => Math.abs(d) > (a.min ?? 0.2));
    const grow = common.filter((k) => Math.abs(c.rows[k][1] - p.rows[k][1]) > 1);
    if (ds.length || grow.length) {
      bad++;
      if (lines.length < 25) lines.push(`  ${label} t=${c.t} rows on screen moved by ${JSON.stringify([...new Set(ds)].slice(0, 4))}${grow.length ? `, ${grow.length} rows resized` : ""}; scrollTop ${p.st}→${c.st}, height ${p.sh}→${c.sh}, mounted ${p.mounted}→${c.mounted}, ${c.sh - c.ch - c.st}px from the bottom`);
    }
  }
  return { bad, lines };
}
if (a.up !== undefined) {
  // Scrolled by hand: rows move with the scroll; a jump is a move the scroll doesn't account for
  const bad = report(analyze(frames, { scrolling: true, step: 130 })); // the wheel steps here are 120px
  const over = frames.filter((f) => f.overlap > 1).length;
  console.log(`rows drawn over each other in ${over} frames`);
  await browser.close();
  process.exit(bad || over ? 1 : 0);
}
const t0 = frames[0]?.t ?? 0;
const r1 = moves(frames, "rAF");
const r2 = moves(ro, "painted");
const over = frames.filter((f) => f.overlap > 1);
for (const l of [...r2.lines]) console.log(l.replace(/t=(\d+)/, (_, t) => `t=+${t - t0}ms`));
console.log(`frames ${frames.length} (painted samples ${ro.length}); frames with rows on screen moving: rAF ${r1.bad}, painted ${r2.bad}; frames with rows drawn over each other: ${over.length}${over.length ? ` (up to ${Math.max(...over.map((f) => f.overlap))}px)` : ""}`);
// The content's height changing while pinned to the bottom: the rows on screen stay, the scrollbar thumb jumps
const at = frames.filter((f) => f.sh - f.ch - f.st <= 2 && (!a.nudge || f.t < nudgeAt));
let hs = 0, hsum = 0;
for (let i = 1; i < at.length; i++) if (at[i].sc === at[i - 1].sc && at[i].sh !== at[i - 1].sh) (hs++, (hsum += Math.abs(at[i].sh - at[i - 1].sh)));
if (at.length) console.log(`height changes at the bottom (the scrollbar jumping): ${hs}, ${hsum}px in all, ${at[0].sh} → ${at[at.length - 1].sh}`);
const last = frames[frames.length - 1];
if (last) console.log(`end: ${last.sh - last.ch - last.st}px from the bottom, ${last.mounted} rows mounted`);
await browser.close();
process.exit(r2.bad || over.length || hs > (a.maxHeightChanges ?? 5) || (nudged && nudged.some((d) => d > 0.1)) ? 1 : 0);
