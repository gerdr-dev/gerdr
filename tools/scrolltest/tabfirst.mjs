// Where a tab's first visit goes: CPU profile of only the window from the switch to the first painted frame of the new
// chat, for each tab not open at load, then self time grouped by file / package and by function.
//   node tabfirst.mjs [--cpu=4] [--dev] [--top=25] [--interval=1000]
import { args, launch, open, useStore } from "./lib.mjs";

const a = args({ cpu: 4, top: 25, interval: 1000 });
const { browser, ctx, page } = await launch();
if (a.dev) await ctx.addCookies([{ name: "gerdr_react", value: "development", domain: "127.0.0.1", path: "/" }]);
await open(page);
await useStore(page);
await page.evaluate(async () => {
  const u = performance.getEntriesByType("resource").map((e) => e.name).find((n) => /\/src\/lib\/actions\/tabs\.ts/.test(n));
  window.__tabs = await import(u);
});
const cdp = await page.context().newCDPSession(page);
await cdp.send("Emulation.setCPUThrottlingRate", { rate: a.cpu });
await cdp.send("Profiler.enable");
await cdp.send("Profiler.setSamplingInterval", { interval: a.interval }); // µs; coarse keeps the profiler from slowing what it measures
const n = await page.evaluate(() => window.__store.get().layout.workspaces[0].tabs.length);
const open0 = await page.evaluate(() => { const w = window.__store.get().layout.workspaces[0]; return w.tabs.findIndex((t) => t.id === w.activeTab); });
const self = {}, byFile = {}, byWhat = {};
// What a sample is doing, judged by its stack from the leaf up: the first frame that names one of these
const WHAT = [
  [/streamdown|marked|remark|rehype|micromark|mdast|hast|shiki|katex/, "Markdown (streamdown: parse + render)"],
  [/es2022\/browser\.mjs|tailwind/, "Tailwind in the browser (class scan + rebuild)"],
  [/components\/ChatList\.tsx/, "ChatList: measure rows / place (incl. forced style + layout)"],
  [/lib\/sync\.ts|lib\/store|transcript\.ts|events\.ts/, "catch-up: applying events, building the transcript"],
  [/react_dom|react-dom|scheduler/, "React (render + commit, our components inside)"],
];
let total = 0, visits = 0;
const short = (u) => u.replace(/.*\/src\//, "").replace(/.*\/_esm\/[^/]+\/[^/]+\//, "vendor:").replace(/\?.*/, "").replace(/-[A-Z0-9]{8}\.js$/, "") || "(native)";
for (let i = 0; i < n; i++) {
  if (i === open0) continue;
  await page.waitForTimeout(500);
  await cdp.send("Profiler.start");
  const r = await page.evaluate((i) => new Promise((done) => {
    const before = [...document.querySelectorAll("[data-chat-scroller]")].find((s) => !s.closest(".invisible"));
    const t0 = performance.now();
    window.__tabs.selectTab(i);
    const tick = () => {
      const sc = [...document.querySelectorAll("[data-chat-scroller]")].find((x) => !x.closest(".invisible"));
      if (sc && sc !== before && sc.querySelector("[data-item-index]")) requestAnimationFrame(() => done(performance.now() - t0));
      else requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  }), i);
  const { profile } = await cdp.send("Profiler.stop");
  visits++;
  total += r;
  const byId = new Map(profile.nodes.map((x) => [x.id, x]));
  const parent = new Map();
  for (const x of profile.nodes) for (const c of x.children || []) parent.set(c, x.id);
  for (let k = 0; k < profile.samples.length; k++) {
    const d = (profile.timeDeltas[k + 1] ?? 0) / 1000;
    const leaf = byId.get(profile.samples[k]);
    const f = leaf.callFrame;
    if (f.functionName !== "(idle)") {
      let what = ["(program)", "(garbage collector)"].includes(f.functionName) ? "browser internals: style / layout / paint / parse (program)" : "other";
      if (what === "other") for (let x = leaf; x; x = byId.get(parent.get(x.id))) { const u = x.callFrame.url; const m = WHAT.find(([re]) => re.test(u)); if (m) { what = m[1]; break; } }
      byWhat[what] = (byWhat[what] || 0) + d;
    }
    if (f.functionName === "(idle)") { self["(idle: waiting)"] = (self["(idle: waiting)"] || 0) + d; byFile["(idle: waiting)"] = (byFile["(idle: waiting)"] || 0) + d; continue; }
    const file = ["(program)", "(garbage collector)"].includes(f.functionName) ? f.functionName : short(f.url);
    byFile[file] = (byFile[file] || 0) + d;
    const key = `${f.functionName || "(anon)"}  ${file}:${f.lineNumber + 1}`;
    self[key] = (self[key] || 0) + d;
  }
  console.log(`tab ${i + 1}: ${Math.round(r)}ms to the new chat painted`);
}
const show = (o, title) => {
  console.log(`\n== ${title} (ms per first visit, mean of ${visits}; switch → painted ${Math.round(total / visits)}ms; cpu ${a.cpu}x)`);
  for (const [k, v] of Object.entries(o).sort((x, y) => y[1] - x[1]).slice(0, a.top)) console.log(`${(v / visits).toFixed(1).padStart(7)}  ${k}`);
};
show(byWhat, "busy time by what it was doing");
show(byFile, "self time by file / package");
show(self, "self time by function");
await browser.close();
