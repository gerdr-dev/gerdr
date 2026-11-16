// CPU profile of tab switches between kept tabs: which functions (React components, our code) the time goes to.
//   node tabprofile.mjs [--dev|--prod] [--keep=6] [--cpu=4] [--rounds=2] [--first] [--top=40] [--out=/tmp/tabswitch.cpuprofile]
// Warms up by visiting every tab once (first visits also pay the catch-up; tabswitch.mjs shows those), then profiles
// `rounds` passes over all tabs. Prints self time and inclusive time per function; --out writes the profile for
// Chrome DevTools (Performance → load profile).
import fs from "node:fs";
import { args, launch, open, setConfig, useStore } from "./lib.mjs";

const a = args({ keep: 6, cpu: 4, rounds: 2, top: 40, out: "/tmp/tabswitch.cpuprofile" });
const { browser, ctx, page } = await launch();
// Production React is the default (internal/uisite/prod.go); --dev: development React (Settings → Labs off), --prod: ask
// for production explicitly (waits for its packages instead of falling back to development while they build)
if (a.dev || a.prod) await ctx.addCookies([{ name: "gerdr_react", value: a.dev ? "development" : "production", domain: "127.0.0.1", path: "/" }]);
await open(page);
console.log("React:", await page.evaluate(() => window.__GERDR_UI__.react));
await useStore(page);
await setConfig(page, { keepTabs: a.keep });
await page.evaluate(async () => {
  const u = performance.getEntriesByType("resource").map((e) => e.name).find((n) => /\/src\/lib\/actions\/tabs\.ts/.test(n));
  window.__tabs = await import(u);
});
const n = await page.evaluate(() => window.__store.get().layout.workspaces[0].tabs.length);
// --first: profile first visits instead (every tab but the one open at load, once each; the catch-up included)
if (!a.first)
  for (let i = n - 1; i >= 0; i--) {
    await page.evaluate((i) => window.__tabs.selectTab(i), i);
    await page.waitForTimeout(1500);
  }
const cdp = await page.context().newCDPSession(page);
if (a.cpu > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: a.cpu });
await cdp.send("Profiler.enable");
await cdp.send("Profiler.setSamplingInterval", { interval: 200 });
await cdp.send("Profiler.start");
const open0 = await page.evaluate(() => { const w = window.__store.get().layout.workspaces[0]; return w.tabs.findIndex((t) => t.id === w.activeTab); });
const order = a.first ? [...Array(n).keys()].filter((i) => i !== open0) : Array.from({ length: a.rounds }, () => [...Array(n).keys()]).flat();
for (const i of order) {
  await page.evaluate((i) => window.__tabs.selectTab(i), i);
  await page.waitForTimeout(a.first ? 1500 : 800);
}
const { profile } = await cdp.send("Profiler.stop");
fs.writeFileSync(a.out, JSON.stringify(profile));

// Self and inclusive time per function (recursion counted once per sample)
const byId = new Map(profile.nodes.map((x) => [x.id, x]));
const parent = new Map();
for (const x of profile.nodes) for (const c of x.children || []) parent.set(c, x.id);
const key = (x) => {
  const f = x.callFrame;
  const url = f.url.replace(/.*\/src\//, "").replace(/.*\/_esm\/[^/]+\/[^/]+\//, "vendor:").replace(/\?.*/, "");
  return `${f.functionName || "(anon)"}  ${url}:${f.lineNumber + 1}`;
};
const self = {}, incl = {};
const dt = profile.timeDeltas;
for (let i = 0; i < profile.samples.length; i++) {
  const d = (dt[i + 1] ?? 0) / 1000;
  let x = byId.get(profile.samples[i]);
  if (["(idle)", "(program)", "(garbage collector)"].includes(x.callFrame.functionName)) {
    self[x.callFrame.functionName] = (self[x.callFrame.functionName] || 0) + d;
    continue;
  }
  self[key(x)] = (self[key(x)] || 0) + d;
  const seen = new Set();
  for (; x; x = byId.get(parent.get(x.id))) {
    const k = key(x);
    if (!seen.has(k)) (seen.add(k), (incl[k] = (incl[k] || 0) + d));
  }
}
const switches = order.length;
const show = (o, title) => {
  console.log(`\n== ${title} (ms per switch, ${switches} switches, cpu ${a.cpu}x)`);
  for (const [k, v] of Object.entries(o).sort((x, y) => y[1] - x[1]).slice(0, a.top)) console.log(`${(v / switches).toFixed(1).padStart(8)}  ${k}`);
};
show(self, "self");
show(Object.fromEntries(Object.entries(incl).filter(([k]) => !k.includes("vendor:"))), "inclusive, our code");
console.log(`\nprofile: ${a.out}`);
await browser.close();
