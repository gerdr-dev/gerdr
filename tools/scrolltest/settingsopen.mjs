// Opening Settings for the first time in a page: where do the fade's frames go? Logs per frame (rAF) the dialog's
// opacity, Long Animation Frames (script vs style/layout time, which scripts), and every rewrite of a <style> (Tailwind's
// browser build appends CSS for classes it sees for the first time).
//   node settingsopen.mjs [--cpu=4] [--times=2] [--unguarded]
import { args, launch, open, useStore } from "./lib.mjs";

const o = args({ cpu: 1, times: 2 });
const { browser, page } = await launch();
const cdp = await page.context().newCDPSession(page);
await open(page, { wait: 4000 });
await useStore(page);
// --unguarded: undo the page's skip of identical <style> writes (sameStyleScript, internal/uisite/html.go) to compare
// with how it was: Tailwind rewriting its unchanged sheet inside the fade
if (o.unguarded) await page.evaluate(() => delete HTMLStyleElement.prototype.textContent);
if (o.cpu > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: o.cpu });

for (let run = 1; run <= o.times; run++) {
  await page.evaluate(() => {
    const t0 = performance.now();
    const log = (window.__log = { t0, frames: [], styles: [], loaf: [] });
    const styleLen = () => [...document.querySelectorAll("style")].reduce((n, s) => n + s.textContent.length, 0);
    let last = styleLen();
    window.__mo = new MutationObserver(() => {
      const n = styleLen();
      if (n !== last) log.styles.push({ t: performance.now() - t0, from: last, to: n }), (last = n);
    });
    window.__mo.observe(document, { childList: true, subtree: true, characterData: true });
    window.__po = new PerformanceObserver((l) => {
      for (const e of l.getEntries())
        if (e.startTime + e.duration >= t0)
          log.loaf.push({
            start: e.startTime - t0,
            dur: e.duration,
            block: e.blockingDuration,
            style: e.startTime + e.duration - e.styleAndLayoutStart,
            render: e.renderStart ? e.styleAndLayoutStart - e.renderStart : 0,
            scripts: e.scripts.map((s) => ({
              src: (s.sourceURL || "").replace(/^.*\/(src|@[^/]+|npm)\//, "$1/").slice(0, 70),
              fn: s.sourceFunctionName,
              inv: s.invoker?.slice(0, 50),
              dur: Math.round(s.duration),
              layout: Math.round(s.forcedStyleAndLayoutDuration),
            })),
          });
    });
    window.__po.observe({ type: "long-animation-frame", buffered: false });
    let prev = t0;
    const tick = (now) => {
      const el = document.querySelector('[role="dialog"]');
      log.frames.push({ t: now - t0, gap: now - prev, op: el ? getComputedStyle(el).opacity : "-" });
      prev = now;
      if (now - t0 < 700) requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
    window.__store.set({ dialog: { kind: "settings" } });
  });
  await page.waitForTimeout(1200);
  const log = await page.evaluate(() => (window.__mo.disconnect(), window.__po.disconnect(), window.__log));
  console.log(`\n=== open #${run} (cpu x${o.cpu}) ===`);
  // The fade proper: from its first visible frame to full opacity (the frames before it are the mount, a separate cost)
  const i0 = log.frames.findIndex((f) => f.op !== "-" && +f.op > 0);
  const i1 = log.frames.findIndex((f, i) => i > i0 && +f.op >= 1);
  const gaps = log.frames.slice(i0 + 1, i1 + 1).map((f) => f.gap);
  const mount = log.frames[i0 - 1]?.t ?? 0;
  console.log(`mount→first fade frame ${mount.toFixed(0)}ms; fade: ${gaps.length} frames, longest gap ${Math.max(...gaps).toFixed(0)}ms, dropped ≈ ${gaps.reduce((n, g) => n + Math.max(0, Math.round(g / 16.7) - 1), 0)}; elements in page ${await page.evaluate(() => document.querySelectorAll("*").length)}`);
  console.log("frames (t / gap since previous / dialog opacity):");
  console.log(log.frames.map((f) => `${f.t.toFixed(0)}/${f.gap.toFixed(0)}/${f.op === "-" ? "-" : (+f.op).toFixed(2)}`).join("  "));
  console.log("style rewrites:", log.styles.map((s) => `@${s.t.toFixed(0)}ms ${s.from}→${s.to}`).join(", ") || "none");
  for (const l of log.loaf) {
    console.log(`LoAF @${l.start.toFixed(0)} dur ${l.dur.toFixed(0)} (style+layout+paint ${l.style.toFixed(0)}, render-phase rAF/RO ${l.render.toFixed(0)})`);
    for (const s of l.scripts.sort((a, b) => b.dur - a.dur).slice(0, 6)) console.log(`   ${s.dur}ms (forced layout ${s.layout}) ${s.inv} ${s.fn} ${s.src}`);
  }
  await page.evaluate(() => window.__store.set({ dialog: null }));
  await page.waitForTimeout(1000);
}
await browser.close();
