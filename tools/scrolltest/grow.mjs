// Phone: what the frame costs when the page grows back as the soft keyboard goes down (Android app: the WebView grows
// from 462 to 778 CSS px in one step, and a long chat took ~140-200ms there, stalling the composer that follows the
// keyboard). Traces each height change and sums the main thread's time by kind.
//   node grow.mjs [--rounds=4] [--cpu=4] [--low=462] [--high=778] [--top=12]
import fs from "node:fs";
import { args, launch, open } from "./lib.mjs";

const a = args({ rounds: 4, cpu: 4, low: 462, high: 778, top: 12 });
const W = 369;
const { browser, ctx, page } = await launch({ width: W, height: a.low, dpr: 3.25, scrollbars: false, mobile: true });
await open(page, { wait: 4000 });
const cdp = await ctx.newCDPSession(page);
if (a.cpu > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: a.cpu });

async function trace(fn) {
  const chunks = [];
  cdp.on("Tracing.dataCollected", (e) => chunks.push(...e.value));
  await cdp.send("Tracing.start", {
    categories: "devtools.timeline,disabled-by-default-devtools.timeline,blink,v8.execute" + (a.why ? ",disabled-by-default-devtools.timeline.invalidationTracking,disabled-by-default-devtools.timeline.stack" : ""),
    transferMode: "ReportEvents",
  });
  await fn();
  const done = new Promise((r) => cdp.once("Tracing.tracingComplete", r));
  await cdp.send("Tracing.end");
  await done;
  cdp.removeAllListeners("Tracing.dataCollected");
  return chunks;
}

const KINDS = ["UpdateLayoutTree", "Layout", "PrePaint", "Paint", "Layerize", "Commit", "FunctionCall", "EventDispatch", "TimerFire", "FireAnimationFrame", "ResizeObserver", "UpdateLayer", "IntersectionObserverController::computeIntersections"];
for (let r = 0; r < a.rounds; r++) {
  for (const h of [a.high, a.low]) {
    await page.waitForTimeout(600);
    const ev = await trace(async () => {
      await page.setViewportSize({ width: W, height: h });
      await page.waitForTimeout(700);
    });
    // Main thread: the renderer's CrRendererMain
    const main = ev.find((e) => e.name === "thread_name" && e.args?.name === "CrRendererMain");
    const on = ev.filter((e) => main && e.pid === main.pid && e.tid === main.tid && e.ph === "X" && e.dur);
    const sum = {};
    for (const e of on) if (KINDS.includes(e.name)) sum[e.name] = (sum[e.name] || 0) + e.dur / 1000;
    const longest = on.filter((e) => e.name === "RunTask").sort((x, y) => y.dur - x.dur)[0];
    const lay = on.filter((e) => e.name === "Layout").map((e) => `${(e.dur / 1000).toFixed(1)}ms(${e.args?.beginData?.dirtyObjects}/${e.args?.beginData?.totalObjects})`);
    const sty = on.filter((e) => e.name === "UpdateLayoutTree").map((e) => `${(e.dur / 1000).toFixed(1)}ms(${e.args?.elementCount ?? "?"})`);
    console.log(`${h === a.high ? "grow  " : "shrink"} longest task ${(longest?.dur / 1000).toFixed(0)}ms | ${Object.entries(sum).filter(([, v]) => v > 1).map(([k, v]) => `${k} ${v.toFixed(0)}`).join(", ")}`);
    console.log(`        layouts ${lay.slice(0, 6).join(" ")} | styles ${sty.slice(0, 6).join(" ")}`);
    if (a.why) {
      // What scheduled each style recalc: the invalidation tracking events with their JS stacks
      for (const e of ev.filter((e) => /ScheduleStyle|StyleRecalcInvalidationTracking|StyleInvalidatorInvalidationTracking/.test(e.name)).slice(0, a.why)) {
        const d = e.args?.data || {};
        const st = (d.stackTrace || []).slice(0, 3).map((f) => `${f.functionName}@${(f.url || "").split("/").pop()}:${f.lineNumber}`).join(" < ");
        console.log(`        ${e.name} ${d.reason || ""} ${d.nodeName || ""} ${d.extraData || ""} ${st}`);
      }
    }
    if (r === a.rounds - 1 && h === a.high) fs.writeFileSync("/tmp/grow-trace.json", JSON.stringify({ traceEvents: ev }));
  }
}
await browser.close();
