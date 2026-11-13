// Phone: long-press to select some text, scroll the chat a few screens on, drag the end handle further: the selection
// jumped to a strange range, its start in the page header (user's screen recording). The virtual list unmounted the row
// the selection started in, so the selection's start fell back to the list itself.
//   node select.mjs [--screens=4] [--up] [--tab=1]
// Selects a stretch of text on screen, scrolls `--screens` screens down (`--up`: up), then extends the selection to
// text on screen there, the way dragging the end handle does (Selection.extend). Fails if the selection's start isn't
// where it was, or the copied text misses what lies between.
import { args, launch, open, tab } from "./lib.mjs";

const a = args({ screens: 4, tab: 1 });
const { browser, ctx, page } = await launch({ width: 400, height: 860, dpr: 3, scrollbars: false, mobile: true });
await open(page);
if (a.tab !== 1) await tab(page, a.tab); // on a phone viewport, clicking the label opens the drawer over the chat
const cdp = await ctx.newCDPSession(page);
const touch = (type, y) => cdp.send("Input.dispatchTouchEvent", { type, touchPoints: type === "touchEnd" ? [] : [{ x: 200, y }] });
// A finger dragging the chat by dy (positive: finger down = scroll up), slowly enough not to fling
async function drag(dy) {
  const y = dy > 0 ? 300 : 700;
  await touch("touchStart", y);
  for (let i = 1; i <= 20; i++) {
    await touch("touchMove", y + (dy * i) / 20);
    await page.waitForTimeout(16);
  }
  await page.waitForTimeout(100);
  await touch("touchEnd");
  await page.waitForTimeout(300);
  if (a.v) console.log("  scrollTop", await page.evaluate(() => document.querySelector("[data-chat-scroller]").scrollTop));
}
// Up from the bottom, so there is room both ways
for (let i = 0; i < 2 * a.screens + 2; i++) await drag(350);
await page.waitForTimeout(800);

// The first text node of some length that is fully on screen
const pick = (sc) => {
  const s = document.querySelector(sc);
  const r = s.getBoundingClientRect();
  const w = document.createTreeWalker(s, NodeFilter.SHOW_TEXT);
  for (let n = w.nextNode(); n; n = w.nextNode()) {
    if (n.data.trim().length < 20) continue;
    const rg = document.createRange();
    rg.selectNodeContents(n);
    const b = rg.getBoundingClientRect();
    if (b.top > r.top + 40 && b.bottom < r.bottom - 40) return n;
  }
};
// Scrolling up, the handle dragged is the start: the anchor (the end that stays) goes at the end of the stretch
const start = await page.evaluate(([pick, up]) => {
  const n = new Function("return " + pick)()("[data-chat-scroller]");
  if (!n) return null;
  window.__selStart = n;
  const sel = getSelection();
  const end = Math.min(10, n.data.length);
  if (up) sel.setBaseAndExtent(n, end, n, 0);
  else sel.setBaseAndExtent(n, 0, n, end);
  return { text: sel.toString(), row: n.parentElement.closest("[data-item-index]")?.dataset.itemIndex };
}, [pick.toString(), !!a.up]);
if (!start) throw new Error("no text on screen to select");
console.log(`selected "${start.text}" in row ${start.row}`);

// Scroll on, a screen at a time (each step a sync: rows mount and unmount)
for (let i = 0; i < a.screens * 2; i++) await drag(a.up ? 350 : -350);
await page.waitForTimeout(400);

const res = await page.evaluate((pick) => {
  const sel = getSelection();
  const n = new Function("return " + pick)()("[data-chat-scroller]");
  const startOk = window.__selStart.isConnected && sel.anchorNode === window.__selStart;
  const anchor = sel.anchorNode ? (sel.anchorNode.nodeType === 3 ? "#text " + sel.anchorNode.data.slice(0, 20) : sel.anchorNode.nodeName + "." + (sel.anchorNode.className || "")) : null;
  if (!n) return { startOk, anchor, err: "no text on screen after scrolling" };
  sel.extend(n, Math.min(5, n.data.length));
  const rows = [...document.querySelectorAll("[data-item-index]")].map((e) => +e.dataset.itemIndex);
  return { startOk, anchor, text: sel.toString(), rows: [Math.min(...rows), Math.max(...rows), rows.length], header: sel.containsNode(document.querySelector("header") ?? document.body.firstElementChild, true) };
}, pick.toString());
console.log(`after scrolling: start node still there: ${res.startOk} (anchor: ${res.anchor}); mounted rows ${res.rows}`);
let bad = !res.startOk;
if (res.text !== undefined) {
  console.log(`selection is ${res.text.length} chars, starts "${res.text.slice(0, 30).replace(/\n/g, "⏎")}"`);
  if (a.up ? !res.text.endsWith(start.text) : !res.text.startsWith(start.text)) {
    console.log("  ✗ the selection no longer starts where it was selected");
    bad = true;
  }
}
console.log(bad ? "✗ FAIL" : "✓ ok");
await browser.close();
process.exit(bad ? 1 : 0);
