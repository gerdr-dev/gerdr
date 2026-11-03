// Phone: after a dropdown menu in the chat was opened (the arrow next to a `! command`'s Run button), a finger
// dragging the chat up from the bottom was pulled back to the end on every move (a phone screen recording). Radix's
// modal menu sets `pointer-events: none` on body while open, so the list never heard that touch start and read the
// scroll as the browser's, not the user's (SCROLL.md). Headless Chrome doesn't reproduce that finger (it passes
// before the fix too); this guards the menu-then-drag path around it.
//   node menutouch.mjs [--up=200] [--close=tap|escape|drag|none]
// Appends a reply ending in a `! command`, opens its menu with a tap, closes it (a tap outside by default), then drags
// the chat down with a finger: it has to end at least half the drag off the bottom.
import { args, launch, open, readEvents, useStore } from "./lib.mjs";

const a = args({ up: 200, close: "tap" });
const { browser, ctx, page } = await launch({ width: 400, height: 860, dpr: 3, scrollbars: false, mobile: true });
await open(page);
await useStore(page);
const cdp = await ctx.newCDPSession(page);
const sid = await page.evaluate(() => {
  const l = window.__store.get().layout;
  const w = l.workspaces.find((x) => x.id === l.active);
  return w.tabs.find((t) => t.id === w.activeTab)?.root.sessionId;
});
const seq = Math.max(...readEvents(sid).map((e) => e.seq)) + 1000;
const para = "这是一段用来占位置的说明文字，长度大概是一两行，模拟 agent 写的解释。".repeat(3);
const text = Array.from({ length: 8 }, () => para).join("\n\n") + "\n\n同步后就能在手机上看：`! make sync-ui`";
const events = [
  { seq: seq + 1, ts: Date.now(), kind: "update", update: { sessionUpdate: "agent_message_chunk", messageId: "msg_scrolltest_menu", content: { type: "text", text } } },
  { seq: seq + 2, ts: Date.now(), kind: "turn_end" },
];
await page.evaluate(([sid, ev]) => window.__store.applyEvents(sid, ev, false), [sid, events]);
await page.waitForTimeout(1500);

const scroller = `[...document.querySelectorAll("[data-chat-scroller]")].find((x) => !x.closest(".invisible"))`;
const offEnd = () => page.evaluate(`(() => { const s = ${scroller}; return s.scrollHeight - s.clientHeight - s.scrollTop; })()`);
const touch = (type, x, y) => cdp.send("Input.dispatchTouchEvent", { type, touchPoints: type === "touchEnd" ? [] : [{ x, y }] });
const tap = async (x, y) => {
  await touch("touchStart", x, y);
  await page.waitForTimeout(40);
  await touch("touchEnd");
  await page.waitForTimeout(400);
};

const arrows = await page.$$('button[title="换个地方运行"], button[title="Run somewhere else"]');
const arrow = arrows[arrows.length - 1];
if (!arrow) throw new Error("no Run button's arrow on screen");
const r = await arrow.boundingBox();
await tap(r.x + r.width / 2, r.y + r.height / 2);
const opened = await page.evaluate(() => !!document.querySelector('[role="menu"]'));
// Drag the chat down (= scroll up)
const drag = async () => {
  const y0 = 300;
  await touch("touchStart", 200, y0);
  const moves = 20;
  const trail = [];
  for (let i = 1; i <= moves; i++) {
    await touch("touchMove", 200, y0 + (a.up * i) / moves);
    await page.waitForTimeout(16);
    trail.push(Math.round(await offEnd()));
  }
  await touch("touchEnd");
  await page.waitForTimeout(800);
  return trail;
};
if (a.close === "tap") await tap(200, 150);
else if (a.close === "escape") await page.keyboard.press("Escape");
else if (a.close === "drag") {
  // What the recording shows: the finger lands on the chat with the menu open and drags; the menu closes. Then back
  // to the bottom for the drag that's measured.
  await drag();
  await page.evaluate(`(() => { const s = ${scroller}; s.scrollTop = s.scrollHeight; })()`);
}
await page.waitForTimeout(400);
const closed = await page.evaluate(() => !document.querySelector('[role="menu"]'));
const pe = await page.evaluate(() => document.body.style.pointerEvents);
const before = await offEnd();
const trail = await drag();
const after = await offEnd();
console.log(`menu opened ${opened}, closed ${closed} (--close=${a.close}), body pointer-events "${pe}"; ${Math.round(before)}px off the end before the drag`);
console.log(`while dragging ${a.up}px: ${trail.join(" ")}; ${Math.round(after)}px off the end after lifting`);
await browser.close();
process.exit(after >= a.up / 2 ? 0 : 1);
