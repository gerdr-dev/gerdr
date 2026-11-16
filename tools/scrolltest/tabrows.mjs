// Rows a tab's first visit mounts, commit by commit, and how many of them end up on screen: what the cold open's
// first paint pays for (ChatList.tsx, `cold`). One line per tab not open at load.
//   node tabrows.mjs
import { launch, open, useStore } from "./lib.mjs";

const { browser, page } = await launch();
await open(page);
await useStore(page);
await page.evaluate(async () => {
  const u = performance.getEntriesByType("resource").map((e) => e.name).find((n) => /\/src\/lib\/actions\/tabs\.ts/.test(n));
  window.__tabs = await import(u);
});
const ws = await page.evaluate(() => { const w = window.__store.get().layout.workspaces[0]; return { n: w.tabs.length, open0: w.tabs.findIndex((t) => t.id === w.activeTab) }; });
for (let i = 0; i < ws.n; i++) {
  if (i === ws.open0) continue;
  const r = await page.evaluate((i) => new Promise((done) => {
    const old = new Set(document.querySelectorAll("[data-chat-scroller]"));
    const t0 = performance.now();
    const seen = [];
    let sc = null, painted = null;
    const mo = new MutationObserver(() => {
      sc = sc || [...document.querySelectorAll("[data-chat-scroller]")].find((x) => !old.has(x));
      if (!sc) return;
      const n = sc.querySelectorAll("[data-chat-key]").length;
      if (!seen.length || seen[seen.length - 1][1] !== n) seen.push([Math.round(performance.now() - t0), n]);
    });
    mo.observe(document.body, { childList: true, subtree: true });
    // Rows at the first frame painted with the new chat showing
    const tick = () => {
      const v = [...document.querySelectorAll("[data-chat-scroller]")].find((x) => !old.has(x) && !x.closest(".invisible"));
      if (v && v.querySelector("[data-chat-key]")) { painted = [Math.round(performance.now() - t0), v.querySelectorAll("[data-chat-key]").length]; return; }
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
    window.__tabs.selectTab(i);
    setTimeout(() => {
      mo.disconnect();
      if (!sc) return done(null);
      const s = sc.getBoundingClientRect();
      const rows = [...sc.querySelectorAll("[data-chat-key]")].map((r) => { const b = r.getBoundingClientRect(); return [Math.round(b.height), b.bottom > s.top + 1 && b.top < s.bottom - 1]; });
      done({ seen, painted, screen: Math.round(s.height), rows, fromBottom: Math.round(sc.scrollHeight - sc.clientHeight - sc.scrollTop) });
    }, 800);
  }), i);
  if (!r) { console.log(`tab ${i + 1}: no new chat (already mounted)`); continue; }
  console.log(`tab ${i + 1}: per commit [ms, rows] ${JSON.stringify(r.seen.slice(0, 6))}; at first paint ${JSON.stringify(r.painted)}; after 800ms ${r.rows.length} mounted, ${r.rows.filter((x) => x[1]).length} on screen, ${r.fromBottom}px from the bottom; heights ${r.rows.map(([h, on]) => (on ? "*" : "") + h).join(" ")}`);
}
await browser.close();
