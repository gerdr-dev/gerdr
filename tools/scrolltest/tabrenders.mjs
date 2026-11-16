// Which React components render on a tab switch between kept tabs, and how many times: a stand-in for React DevTools
// (its global hook, installed before the page loads; every commit's fiber tree is walked for fibers that did work).
//   node tabrenders.mjs [--keep=6] [--rounds=2] [--first] [--top=40] [--only=<regex>] [--owners=Tooltip] [--why=SessionPaneView]
// Names starting with + were mounted in that commit; the rest re-rendered. Development React only (the production build doesn't name components).
import { args, launch, open, setConfig, useStore } from "./lib.mjs";

const a = args({ keep: 6, rounds: 2, top: 40 });
const { browser, ctx, page } = await launch();
// Development React: the production build doesn't name components (and has no DevTools hook calls to count)
await ctx.addCookies([{ name: "gerdr_react", value: "development", domain: "127.0.0.1", path: "/" }]);
await page.addInitScript(() => {
  const counts = (window.__renders = {});
  window.__countRenders = false;
  const name = (f) => {
    const t = f.type;
    if (!t || typeof t === "string") return null;
    return t.displayName || t.name || t.render?.displayName || t.render?.name || t.type?.displayName || t.type?.name || null;
  };
  const walk = (f) => {
    for (; f; f = f.sibling) {
      // PerformedWork (1): the component function ran in this commit
      if (f.flags & 1) {
        const n = name(f);
        // "+" = mounted in this commit, otherwise an update of one already there
        if (n) {
          const k = f.alternate ? n : "+" + n;
          counts[k] = (counts[k] || 0) + 1;
          // --why: what changed for the component named there: props (keys), context, or hook state (hook numbers)
          if (window.__whyOf && n === window.__whyOf && f.alternate) {
            const a = f.alternate, why = [];
            if (f.memoizedProps !== a.memoizedProps) {
              const ks = Object.keys(f.memoizedProps || {}).filter((k) => f.memoizedProps[k] !== a.memoizedProps?.[k]);
              why.push("props:" + (ks.join("/") || "(same values)"));
            }
            for (let d = f.dependencies?.firstContext, i = 0; d; d = d.next, i++) {
              // The context's default value tells which one it is; the value it read this time
              const show = (v) => (typeof v === "object" && v ? "{" + Object.keys(v).slice(0, 4).join(",") + "}" : String(v));
              why.push("ctx" + i + ":" + (d.context.displayName || "default=" + show(d.context._defaultValue ?? d.context._currentValue2)) + " read=" + show(d.memoizedValue));
            }
            let h1 = f.memoizedState, h0 = a.memoizedState, i = 0;
            const hs = [];
            // Only state-like hooks: effects get a new record on every render anyway. useSyncExternalStore (the store's
            // hooks) keeps queue.getSnapshot; useState / useReducer a queue with a dispatch
            for (; h1 && h0; h1 = h1.next, h0 = h0.next, i++)
              if (h1.queue && h1.memoizedState !== h0.memoizedState) {
                const v = h1.memoizedState;
                hs.push(i + "=" + (typeof v === "object" ? (Array.isArray(v) ? "array" : v === null ? "null" : "object") : String(v).slice(0, 20)));
              }
            if (hs.length) why.push("hooks:" + hs.join("/"));
            const k = why.join(" ");
            (window.__why ??= {})[k] = (window.__why[k] || 0) + 1;
          }
          // --owners: for the component named there, the nearest ancestors of ours (capitalised, not Radix-looking)
          if (window.__ownersOf && n === window.__ownersOf) {
            const chain = [];
            for (let p = f.return; p && chain.length < 4; p = p.return) {
              const pn = name(p);
              if (pn && /^[A-Z]/.test(pn) && !/^(Primitive|Popper|Presence|Menu|Tooltip|Dropdown|Dialog|Context|Slot|Portal|Dismissable|Focus|Collection|Roving|Provider)/.test(pn) && !/Provider$/.test(pn)) chain.push(pn);
            }
            const ck = chain.join(" < ");
            (window.__owners ??= {})[ck] = (window.__owners[ck] || 0) + 1;
          }
        }
      }
      // A subtree React skipped isn't cloned (the new fiber's child is still the old one's): its fibers keep the flags of
      // the commit that last rendered them, so don't count them again (React DevTools does the same)
      if (!f.alternate || f.child !== f.alternate.child) walk(f.child);
    }
  };
  window.__REACT_DEVTOOLS_GLOBAL_HOOK__ = {
    supportsFiber: true,
    renderers: new Map(),
    inject(r) {
      this.renderers.set(1, r);
      return 1;
    },
    onCommitFiberRoot(_id, root) {
      if (window.__countRenders) { const c = root.current; if (!c.alternate || c.child !== c.alternate.child) walk(c.child); }
    },
    onCommitFiberUnmount() {},
    onPostCommitFiberRoot() {},
    checkDCE() {},
  };
});
await open(page);
await useStore(page);
await setConfig(page, { keepTabs: a.keep });
await page.evaluate(async () => {
  const u = performance.getEntriesByType("resource").map((e) => e.name).find((n) => /\/src\/lib\/actions\/tabs\.ts/.test(n));
  window.__tabs = await import(u);
});
const n = await page.evaluate(() => window.__store.get().layout.workspaces[0].tabs.length);
// --first: count first visits instead (each tab not open at load, once; the 1.5s after the switch)
if (!a.first)
  for (let i = n - 1; i >= 0; i--) {
    await page.evaluate((i) => window.__tabs.selectTab(i), i);
    await page.waitForTimeout(1500);
  }
await page.evaluate(([o, w]) => ((window.__countRenders = true), (window.__ownersOf = o || null), (window.__whyOf = w || null)), [a.owners, a.why]);
const open0 = await page.evaluate(() => { const w = window.__store.get().layout.workspaces[0]; return w.tabs.findIndex((t) => t.id === w.activeTab); });
const order = a.first ? [...Array(n).keys()].filter((i) => i !== open0) : Array.from({ length: a.rounds }, () => [...Array(n).keys()]).flat();
for (const i of order) {
  await page.evaluate((i) => window.__tabs.selectTab(i), i);
  await page.waitForTimeout(a.first ? 1500 : 800);
}
const counts = await page.evaluate(() => window.__renders);
const sw = order.length;
const total = Object.values(counts).reduce((x, y) => x + y, 0);
console.log(`${(total / sw).toFixed(0)} component renders per switch (${sw} switches, ${n} tabs, keepTabs ${a.keep})`);
const only = a.only ? new RegExp(a.only) : null;
for (const [k, v] of Object.entries(counts).filter(([k]) => !only || only.test(k)).sort((x, y) => y[1] - x[1]).slice(0, a.top)) console.log(`${(v / sw).toFixed(1).padStart(8)}  ${k}`);
if (a.why) {
  console.log(`\nwhy ${a.why} re-renders (per switch; ctx lists the contexts it reads, changed or not):`);
  const o = await page.evaluate(() => window.__why || {});
  for (const [k, v] of Object.entries(o).sort((x, y) => y[1] - x[1]).slice(0, 20)) console.log(`${(v / sw).toFixed(1).padStart(8)}  ${k || "(nothing: a parent re-rendered with the same props)"}`);
}
if (a.owners) {
  console.log(`\nwhere ${a.owners} re-renders from (per switch):`);
  const o = await page.evaluate(() => window.__owners || {});
  for (const [k, v] of Object.entries(o).sort((x, y) => y[1] - x[1]).slice(0, 20)) console.log(`${(v / sw).toFixed(1).padStart(8)}  ${k}`);
}
await browser.close();
