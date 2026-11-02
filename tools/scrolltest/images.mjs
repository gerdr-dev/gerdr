// Images in the chat must keep their row's height when the row is scrolled out and back in (the virtual list remounts
// it): an <img> has no size until it loads, so without a reserved box the row grows back in steps on every pass.
//   node images.mjs [--tab=1] [--delay=200] [--rounds=6] [--watch=1500] [--width=390]
// --width: a narrow chat (a phone), where the column, not the image, sets the width and the height must follow it.
// Makes three test images of different shapes (sips, from desktop/res/icon.png) and appends an agent message with them
// between paragraphs, then a long one after it (client side only, store.applyEvents). Image responses are held back
// --delay ms, like a phone on a remote connection. Parks the images mid-screen, lets them load once, then scrolls them
// out of the rendered range and back --rounds times, watching each time: rows on screen must not change height.
// Without the fix only about every other round shows it (Chrome sometimes has the decoded image at hand on remount),
// hence the six rounds by default.
import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { analyze, args, launch, open, readEvents, report, startFrameLog, stopFrameLog, tab, useStore } from "./lib.mjs";

const a = args({ tab: 1, delay: 200, rounds: 6, watch: 1500 });
const icon = path.join(path.dirname(fileURLToPath(import.meta.url)), "../../desktop/res/icon.png");
const imgs = [
  [600, 1200],
  [900, 500],
  [400, 400],
].map(([h, w], i) => {
  const out = `/tmp/gerdr-scrolltest-img-${i}.png`;
  execFileSync("sips", ["-z", String(h), String(w), icon, "--out", out], { stdio: "ignore" });
  return out;
});

const { browser, page } = await launch(a.width ? { width: a.width, height: 800 } : {});
await page.route(/\/api\/fs\/raw/, async (r) => {
  await new Promise((ok) => setTimeout(ok, a.delay));
  await r.continue();
});
await open(page);
await useStore(page);
await tab(page, a.tab);
if (a.width) await page.keyboard.press("Escape"); // narrow layout: the tab title opens a session picker over the chat
await page.waitForTimeout(1500);
const sid = await page.evaluate(() => {
  const w = window.__store.get().layout.workspaces[0];
  return w.tabs.find((t) => t.id === w.activeTab)?.root.sessionId;
});
const seq = Math.max(...readEvents(sid).map((e) => e.seq)) + 1000;
const msg = (n, id, text) => ({ seq: seq + n, ts: Date.now(), kind: "update", update: { sessionUpdate: "agent_message_chunk", messageId: id, content: { type: "text", text } } });
const para = "这是一段用来占位置的说明文字，长度大概是一两行，模拟 agent 在图片前后写的解释。".repeat(3);
const events = [
  msg(1, "msg_scrolltest_images", [para, ...imgs.flatMap((p) => [`![测试图](${p})`, para])].join("\n\n")),
  { seq: seq + 2, ts: Date.now(), kind: "turn_end" },
  msg(3, "msg_scrolltest_after", Array.from({ length: 30 }, () => para).join("\n\n")),
  { seq: seq + 4, ts: Date.now(), kind: "turn_end" },
];
await page.evaluate(([sid, ev]) => window.__store.applyEvents(sid, ev, false), [sid, events]);
await page.waitForTimeout(800);

const scroller = () => [...document.querySelectorAll("[data-virtuoso-scroller], [data-chat-scroller]")].find((s) => !s.closest(".invisible"));
// Put the middle image in the middle of the screen (scrolling up until its row is mounted).
// Wheel over the chat itself (at a narrow width the sidebar can cover the middle of the window).
const box = await page.evaluate((s) => { const r = eval(s)().getBoundingClientRect(); return [r.x + r.width / 2, r.y + r.height / 2]; }, `(${scroller})`);
await page.mouse.move(box[0], box[1]);
let placed = false;
for (let i = 0; i < 150 && !placed; i++) {
  placed = await page.evaluate(
    ([name, scrollerSrc]) => {
      const sc = eval(scrollerSrc)();
      const el = [...sc.querySelectorAll("img")].find((e) => (e.getAttribute("src") || "").includes(encodeURIComponent(name)) || (e.getAttribute("src") || "").includes(name));
      if (!el) return false;
      sc.scrollTop += el.getBoundingClientRect().top - sc.getBoundingClientRect().top - sc.clientHeight / 2;
      return true;
    },
    [path.basename(imgs[1]), `(${scroller})`],
  );
  // A wheel, not scrollTop: at the bottom the list only leaves it on user input.
  if (!placed) await page.mouse.wheel(0, -500);
  await page.waitForTimeout(100);
}
if (!placed) throw new Error("test image not found on screen");
await page.waitForTimeout(a.delay + 1500); // first load: the row may grow once here, that is expected

let bad = 0;
for (let r = 0; r < a.rounds; r++) {
  // Relative moves: going up may load older messages in front, which shifts every scrollTop below them (the list keeps
  // the top row in place), so an absolute position to come back to would land elsewhere.
  await page.evaluate((s) => (eval(s)().scrollTop -= 6000), `(${scroller})`);
  await page.waitForTimeout(800);
  await startFrameLog(page);
  await page.evaluate((s) => (eval(s)().scrollTop += 6000), `(${scroller})`);
  await page.waitForTimeout(a.delay + a.watch);
  const back = await page.evaluate((s) => [...eval(s)().querySelectorAll("img")].some((e) => (e.getAttribute("src") || "").includes("gerdr-scrolltest-img")), `(${scroller})`);
  console.log(`== remounted (round ${r + 1})${back ? "" : ": the test images are not back on screen"}`);
  // The jump back itself swaps the whole screen; what counts is what happens after it.
  const ev = analyze((await stopFrameLog(page)).frames).filter((e) => e.kind !== "swapped");
  bad += report(ev) + ev.filter((e) => e.kind === "row-resized").length + (back ? 0 : 1);
}
await browser.close();
process.exit(bad ? 1 : 0);
