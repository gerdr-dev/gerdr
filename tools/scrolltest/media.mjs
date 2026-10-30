// A broken image (or video) in the chat must not make the list jump: not while it fails and retries, and not when its
// row is scrolled out and back in (the virtual list remounts it).
//   node media.mjs [--tab=1] [--src=/tmp/gerdr-scrolltest-missing.png] [--watch=14000]
// Appends two agent messages to the session (client side only, store.applyEvents): one with the broken media between two
// paragraphs, then a long one after it. Scrolls up so the broken one sits mid-screen (not at the bottom, so any
// movement counts), watches long enough to cover every automatic retry, clicks the retry button on the "can't open"
// chip, then scrolls it out and back in and watches again.
import { analyze, args, launch, open, readEvents, report, startFrameLog, stopFrameLog, tab, useStore } from "./lib.mjs";

const a = args({ tab: 1, src: "/tmp/gerdr-scrolltest-missing.png", watch: 14000 });
const { browser, page } = await launch();
await open(page);
await useStore(page);
await tab(page, a.tab);
await page.waitForTimeout(1500);
const sid = await page.evaluate(() => {
  const w = window.__store.get().layout.workspaces[0];
  return w.tabs.find((t) => t.id === w.activeTab)?.root.sessionId;
});
const seq = Math.max(...readEvents(sid).map((e) => e.seq)) + 1000;
const msg = (n, id, text) => ({ seq: seq + n, ts: Date.now(), kind: "update", update: { sessionUpdate: "agent_message_chunk", messageId: id, content: { type: "text", text } } });
const para = "这是一段用来占位置的说明文字，长度大概是一两行，模拟 agent 在图片前后写的解释。".repeat(3);
const events = [
  msg(1, "msg_scrolltest_media", `${para}\n\n![诊断面板（模拟的出问题状态）](${a.src})\n\n${para}`),
  { seq: seq + 2, ts: Date.now(), kind: "turn_end" },
  msg(3, "msg_scrolltest_after", Array.from({ length: 12 }, () => para).join("\n\n")),
  { seq: seq + 4, ts: Date.now(), kind: "turn_end" },
];
await page.evaluate(([sid, ev]) => window.__store.applyEvents(sid, ev, false), [sid, events]);
await page.waitForTimeout(800);
// Put the broken media in the middle of the screen.
const placed = await page.evaluate((src) => {
  const sc = [...document.querySelectorAll("[data-virtuoso-scroller]")].find((s) => !s.closest(".invisible"));
  const name = src.split("/").pop();
  const el = [...sc.querySelectorAll("img, video, button")].find((e) => (e.getAttribute("src") || "").includes(encodeURIComponent(name)) || (e.textContent || "").includes(name));
  if (!el) return false;
  sc.scrollTop += el.getBoundingClientRect().top - sc.getBoundingClientRect().top - sc.clientHeight / 2;
  return true;
}, a.src);
if (!placed) throw new Error("broken media not found on screen");
await page.waitForTimeout(300);

let bad = 0;
console.log(`== failing and retrying (${a.watch}ms)`);
await startFrameLog(page);
await page.waitForTimeout(a.watch);
bad += report(analyze((await stopFrameLog(page)).frames));

// The manual retry on the "can't open" chip.
const name = a.src.split("/").pop();
const clicked = await page.evaluate((name) => {
  const b = [...document.querySelectorAll("button")].find((x) => (x.textContent || "").includes(name) && !x.disabled);
  if (!b) return false;
  window.__retryBtn = b;
  return true;
}, name);
if (!clicked) {
  console.log("no enabled retry chip after the automatic retries");
  bad++;
} else {
  console.log("== clicked retry, watching 3000ms");
  // Besides jumps, check the chip itself never leaves the screen: swapping it for the broken <img> and back can be too
  // short to move anything, but it shows as a flash.
  await page.evaluate((name) => {
    window.__chipGone = 0;
    window.__chipFrames = 0;
    const tick = () => {
      window.__chipFrames++;
      if (![...document.querySelectorAll("button")].some((x) => (x.textContent || "").includes(name))) window.__chipGone++;
      window.__chipRaf = requestAnimationFrame(tick);
    };
    tick();
  }, name);
  await startFrameLog(page);
  await page.evaluate(() => window.__retryBtn.click());
  await page.waitForTimeout(3000);
  bad += report(analyze((await stopFrameLog(page)).frames));
  const gone = await page.evaluate(() => (cancelAnimationFrame(window.__chipRaf), [window.__chipGone, window.__chipFrames]));
  console.log(`chip missing in ${gone[0]} of ${gone[1]} frames`);
  if (gone[0]) bad++;
}

// Scroll it out of the rendered range and back: the row remounts.
const vp = page.viewportSize();
await page.mouse.move(vp.width * 0.6, vp.height * 0.45);
for (let r = 0; r < 2; r++) {
  const st = await page.evaluate(() => [...document.querySelectorAll("[data-virtuoso-scroller]")].find((s) => !s.closest(".invisible")).scrollTop);
  await page.evaluate(() => {
    const sc = [...document.querySelectorAll("[data-virtuoso-scroller]")].find((s) => !s.closest(".invisible"));
    sc.scrollTop -= 6000;
  });
  await page.waitForTimeout(600);
  await page.evaluate((st) => {
    const sc = [...document.querySelectorAll("[data-virtuoso-scroller]")].find((s) => !s.closest(".invisible"));
    sc.scrollTop = st;
  }, st);
  console.log(`== remounted (round ${r + 1}), watching ${a.watch}ms`);
  await page.waitForTimeout(100);
  await startFrameLog(page);
  await page.waitForTimeout(a.watch);
  bad += report(analyze((await stopFrameLog(page)).frames));
}
await browser.close();
process.exit(bad ? 1 : 0);
