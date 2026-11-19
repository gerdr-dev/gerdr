// Mid-chat, open a subagent's conversation and come back: the main chat must show the same row at the same place.
//   node subback.mjs [--frac=0.5] [--mode=code|remount|both] [--keep=6]
// code: "查看对话" in Code splits a pane off (the main chat narrows, then widens when the sub pane closes).
// remount: what a panel's SessionChat does (the main ChatView unmounts while the sub shows, remounts on "back"),
// reproduced as a tab switch with keepTabs 1 (the same unmount / remount with the same paneId).
// Reads the row at the top of the screen by key (data-chat-key) and its offset from the screen's top, before and after.
import { args, launch, open, readEvents, setConfig, useStore } from "./lib.mjs";

const a = args({ frac: 0.5, mode: "both", keep: 6, same: 0, older: 0 });
const { browser, page } = await launch();
await open(page);
await useStore(page);
await setConfig(page, { keepTabs: a.mode === "remount" ? 1 : a.keep });
await page.waitForTimeout(500);

const sid = await page.evaluate(() => {
  const s = window.__store.get();
  const ws = s.layout.workspaces[0];
  const t = ws.tabs.find((x) => x.id === ws.activeTab) ?? ws.tabs[0];
  return t.root.sessionId ?? t.root.children?.[0]?.sessionId;
});
const tc = readEvents(sid).find((e) => e.update?._meta?.gerdr?.parentToolCallId)?.update._meta.gerdr.parentToolCallId;

const anchor = () =>
  page.evaluate(() => {
    const sc = [...document.querySelectorAll("[data-chat-scroller]")].find((x) => !x.closest(".invisible") && x.clientHeight);
    const top = sc.getBoundingClientRect().top;
    const rows = [...sc.querySelectorAll("[data-chat-key]")]
      .map((n) => ({ key: n.dataset.chatKey, r: n.getBoundingClientRect() }))
      .filter((x) => x.r.bottom > top + 1)
      .sort((x, y) => x.r.top - y.r.top);
    const r = rows[0];
    return { key: r.key, off: Math.round(r.r.top - top), scrollTop: Math.round(sc.scrollTop), h: sc.scrollHeight, w: sc.clientWidth, text: sc.querySelector(`[data-chat-key="${CSS.escape(r.key)}"]`).innerText.slice(0, 40) };
  });

// Scroll to frac of the way down, by wheel so the list counts it as the user's
async function park() {
  for (let i = 0; i < a.older; i++) {
    for (let k = 0; k < 6; k++) {
      await page.evaluate(() => ([...document.querySelectorAll("[data-chat-scroller]")].find((x) => !x.closest(".invisible") && x.clientHeight).scrollTop = 0));
      await page.mouse.wheel(0, -400);
      await page.waitForTimeout(500);
    }
    await page.waitForTimeout(1200);
  }
  const box = await page.evaluate(() => {
    const sc = [...document.querySelectorAll("[data-chat-scroller]")].find((x) => !x.closest(".invisible") && x.clientHeight);
    const r = sc.getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  });
  await page.mouse.move(box.x, box.y);
  await page.mouse.wheel(0, -1200);
  await page.waitForTimeout(400);
  await page.evaluate((f) => {
    const sc = [...document.querySelectorAll("[data-chat-scroller]")].find((x) => !x.closest(".invisible") && x.clientHeight);
    sc.scrollTop = (sc.scrollHeight - sc.clientHeight) * f;
  }, a.frac);
  await page.waitForTimeout(1500);
  await page.mouse.wheel(0, -100);
  await page.waitForTimeout(1000);
}

const action = (file, fn, ...xs) =>
  page.evaluate(
    async ([file, fn, xs]) => {
      const u = performance.getEntriesByType("resource").map((e) => e.name).find((x) => x.includes(`/src/lib/actions/${file}.ts`));
      return (await import(u))[fn](...xs);
    },
    [file, fn, xs],
  );

const tab = (_p, n) => action("tabs", "selectTab", n - 1);
let bad = 0;
const check = (name, b, c) => {
  const ok = b.key === c.key && Math.abs(b.off - c.off) <= 2;
  if (!ok) bad++;
  console.log(`${name}: before ${JSON.stringify(b)}\n${" ".repeat(name.length)}  after  ${JSON.stringify(c)}${ok ? "" : "   <- moved"}`);
};

if (a.mode === "code" || a.mode === "both") {
  await park();
  const b = await anchor();
  if (!tc) throw new Error("no subagent in this session");
  await action("panes", "openSubagent", sid, tc, "sub");
  await page.waitForTimeout(1500);
  const mid = await anchor().catch(() => null);
  await action("panes", "closeFocusedPane");
  await page.waitForTimeout(1500);
  console.log(`  while split: ${JSON.stringify(mid)}`);
  check("code", b, await anchor());
}
if (a.mode === "remount" || a.mode === "both") {
  await setConfig(page, { keepTabs: 1 });
  // --same: tab 2 shows the same session, so it stays watched while tab 1's chat is unmounted (as in a panel's
  // SessionChat, whose watch outlives the ChatView); otherwise re-watching may push the log again from its latest part
  if (a.same)
    await page.evaluate((sid) => {
      const s = window.__store.get();
      const l = structuredClone(s.layout);
      l.workspaces[0].tabs[1].root.sessionId = sid;
      window.__store.set({ layout: l });
    }, sid);
  await page.waitForTimeout(800);
  await park();
  const b = await anchor();
  await tab(page, 2);
  await page.waitForTimeout(1200);
  await tab(page, 1);
  await page.waitForTimeout(1500);
  check("remount", b, await anchor());
}
await browser.close();
process.exit(bad ? 1 : 0);
