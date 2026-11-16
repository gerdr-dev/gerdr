// How long a tab switch takes: from the click to the first frame painted with the new tab's chat on screen, plus the
// long tasks (> 50ms main-thread blocks) in that window and how long the main thread stays busy after it.
//   node tabswitch.mjs [--dev|--prod] [--keep=6] [--cpu=1] [--rounds=3] [--settle=1500] [--wait=0]
// --keep: keepTabs (6 is the default; 1 = every switch remounts). --cpu: CDP CPU throttling, 4 ≈ a slow laptop.
// Prepare tabs with gen.mjs (images / videos / long text) or setup.mjs, then make dev.
import fs from "node:fs";
import path from "node:path";
import { DIR, args, launch, open, setConfig, useStore } from "./lib.mjs";

const a = args({ keep: 6, cpu: 1, rounds: 3, settle: 1500 });
const { browser, ctx, page } = await launch();
// Production React is the default (internal/uisite/prod.go); --dev: development React (Settings → Labs off), --prod: ask
// for production explicitly (waits for its packages instead of falling back to development while they build)
if (a.dev || a.prod) await ctx.addCookies([{ name: "gerdr_react", value: a.dev ? "development" : "production", domain: "127.0.0.1", path: "/" }]);
await open(page);
console.log("React:", await page.evaluate(() => window.__GERDR_UI__.react));
await useStore(page);
await setConfig(page, { keepTabs: a.keep });
await page.evaluate(async () => {
  // The exact URL the page loaded (with its ?v=), or it would be a second copy of the module
  const u = performance.getEntriesByType("resource").map((e) => e.name).find((n) => /\/src\/lib\/actions\/tabs\.ts/.test(n));
  window.__tabs = await import(u);
});
if (a.cpu > 1) {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Emulation.setCPUThrottlingRate", { rate: a.cpu });
}
await page.evaluate(() => {
  window.__long = [];
  window.__loaf = [];
  new PerformanceObserver((l) => window.__long.push(...l.getEntries().map((e) => [e.startTime, e.duration]))).observe({ type: "longtask", buffered: false });
  // Long animation frames: where a slow frame went (scripts by source, then style + layout)
  new PerformanceObserver((l) => window.__loaf.push(...l.getEntries())).observe({ type: "long-animation-frame", buffered: false });
});
const tabIds = await page.evaluate(() => window.__store.get().layout.workspaces[0].tabs.map((t) => [t.id, t.root.sessionId]));
const titles = Object.fromEntries(JSON.parse(fs.readFileSync(path.join(DIR, "sessions", "index.json"), "utf8")).map((e) => [e.id, e.title]));

// Switch inside the page so t0 is the click itself; done = first rAF where the visible scroller belongs to the new
// session and has rows, then one more rAF (the frame it was painted in has passed by then).
async function switchTo(tabId, sid) {
  return page.evaluate(
    ([tabId, sid]) =>
      new Promise((done) => {
        const before = [...document.querySelectorAll("[data-chat-scroller], [data-virtuoso-scroller]")].find((s) => !s.closest(".invisible"));
        // The same action as Ctrl+<n> / a click in the tab bar (actions/tabs.ts selectTab)
        const ws = window.__store.get().layout.workspaces[0];
        const t0 = performance.now();
        window.__tabs.selectTab(ws.tabs.findIndex((t) => t.id === tabId));
        let frames = 0;
        const tick = () => {
          frames++;
          const sc = [...document.querySelectorAll("[data-chat-scroller], [data-virtuoso-scroller]")].find((x) => !x.closest(".invisible") && x.getClientRects().length);
          const ok = sc && sc !== before && sc.querySelector("[data-item-index]") && !sc.closest("[aria-hidden=true]");
          if (ok || performance.now() - t0 > 10000)
            requestAnimationFrame(() => done({ ms: Math.round(performance.now() - t0), frames, timeout: !ok, rows: sc?.querySelectorAll("[data-item-index]").length }));
          else requestAnimationFrame(tick);
        };
        requestAnimationFrame(tick);
        window.__t0 = t0;
      }),
    [tabId, sid],
  );
}

// Start on the last tab so the first switch (to tab 1) is a real one
await page.evaluate((n) => window.__tabs.selectTab(n - 1), tabIds.length);
await page.waitForTimeout(a.settle);
// --wait: sit idle first, as a person reading would, before round 1 (the first visits)
if (a.wait) await page.waitForTimeout(a.wait);
console.log("kept before switching:", await page.evaluate(() => window.__store.get().keptTabs.join(",")));
const results = {};
for (let r = 0; r < a.rounds; r++) {
  for (const [id, sid] of tabIds) {
    await page.evaluate(() => ((window.__long = []), (window.__loaf = [])));
    const res = await switchTo(id, sid);
    await page.waitForTimeout(a.settle);
    const long = await page.evaluate(() => window.__long.filter(([s]) => s >= window.__t0).map(([s, d]) => [Math.round(s - window.__t0), Math.round(d)]));
    const blocked = long.reduce((x, [, d]) => x + d, 0);
    // Frames up to the moment it showed: script time by function, and style + layout time
    const loaf = await page.evaluate((ms) => {
      const fr = window.__loaf.filter((f) => f.startTime + f.duration >= window.__t0 && f.startTime <= window.__t0 + ms);
      const by = {};
      let script = 0, style = 0;
      for (const f of fr) {
        style += f.duration - (f.styleAndLayoutStart ? f.styleAndLayoutStart - f.startTime : f.duration);
        for (const sc of f.scripts) {
          script += sc.duration;
          const k = (sc.sourceFunctionName || sc.invoker || "?") + " " + (sc.sourceURL || "").replace(/.*\/src\//, "").replace(/\?.*/, "");
          by[k] = (by[k] || 0) + sc.duration;
        }
      }
      return { n: fr.length, script: Math.round(script), style: Math.round(style), top: Object.entries(by).sort((a, b) => b[1] - a[1]).slice(0, 3).map(([k, v]) => `${k}=${Math.round(v)}`) };
    }, res.ms);
    const name = titles[sid] || sid;
    (results[name] ??= []).push(res.ms);
    console.log(`${r + 1} ${name.padEnd(28)} shown ${String(res.ms).padStart(5)}ms${res.timeout ? " (TIMEOUT)" : ""}  rows ${res.rows}  long ${blocked}ms | slow frames ${loaf.n}: script ${loaf.script} style+layout ${loaf.style}  ${loaf.top.join("  ")}`);
  }
}
console.log(`\nkeepTabs=${a.keep} cpu=${a.cpu}x — median ms to show:`);
for (const [n, v] of Object.entries(results)) console.log(`  ${n.padEnd(28)} ${v.sort((x, y) => x - y)[v.length >> 1]}   (${v.join(", ")})`);
await browser.close();
