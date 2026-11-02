// Phone: quick flicks up and back down at the bottom of an idle chat. Once the chat is back at the very end and still,
// the jump-to-bottom button must be gone (a phone recording: fast swipes up and down left it up at the bottom).
//   node flick.mjs [--up=300] [--down=1500] [--gap=50] [--rounds=3] [--speed=3000] [--cpu=1] [--v]
// Real touch gestures with flings (CDP Input.synthesizeScrollGesture, touch source), not scrollTop writes.
// --up: px the finger swipes down (scrolls up); --down: px it swipes up (scrolls down, past the end); --gap: ms between.
import { args, launch, open } from "./lib.mjs";

const a = args({ up: 300, down: 1500, gap: 50, rounds: 3, speed: 3000, cpu: 1 });
const { browser, ctx, page } = await launch({ width: 400, height: 860, dpr: 3, scrollbars: false, mobile: true });
if (a.v) page.on("console", (m) => m.text().startsWith("DEBUG") && console.log(m.text()));
await open(page);
const cdp = await ctx.newCDPSession(page);
if (a.cpu > 1) await cdp.send("Emulation.setCPUThrottlingRate", { rate: a.cpu });
const state = () =>
  page.evaluate(() => {
    const sc = [...document.querySelectorAll("[data-chat-scroller]")].find((x) => !x.closest(".invisible"));
    return { off: sc.scrollHeight - sc.clientHeight - sc.scrollTop, button: !!document.querySelector("button svg.lucide-arrow-down") };
  });
const s0 = await state();
if (s0.off > 2 || s0.button) throw new Error(`not at the bottom to start with (${Math.round(s0.off)}px off, button ${s0.button})`);
// Positive yDistance moves the finger down = scrolls up
const swipe = (dy) =>
  cdp.send("Input.synthesizeScrollGesture", { x: 200, y: dy > 0 ? 250 : 650, yDistance: dy, speed: a.speed, gestureSourceType: "touch", preventFling: false });
let bad = 0;
for (let r = 0; r < a.rounds; r++) {
  await swipe(a.up);
  await page.waitForTimeout(a.gap);
  await swipe(-a.down);
  await page.waitForTimeout(a.gap);
}
// Let the last fling run out
let st = await state();
for (let i = 0; i < 40; i++) {
  await page.waitForTimeout(100);
  const n = await state();
  if (Math.abs(n.off - st.off) < 0.5) break;
  st = n;
}
await page.waitForTimeout(800);
st = await state();
const ok = st.off < 2 && !st.button;
if (!ok) bad++;
console.log(`after ${a.rounds} rounds of flicks: ${Math.round(st.off)}px off the bottom, jump-to-bottom button ${st.button ? "shown" : "hidden"}`);
await browser.close();
process.exit(bad ? 1 : 0);
