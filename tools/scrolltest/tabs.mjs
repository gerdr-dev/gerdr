// 在几个 tab 之间来回切，每次切回来看对话是不是还在最底下。
//   node tabs.mjs [--rounds=6] [--keep=1]
// --keep：保留几个 tab 不卸载（设置里的 keepTabs；1 = 「tab 不保留」，每次切回来都重新挂载）。
// 切过去后隔 16 / 150 / 600 / 1500ms 各量一次：前面几次在半中间是正常的（还在挂、在量），1500ms 还不在底就是停住了。
// 要切走时也在出内容的情况：node stream.mjs --up=0 配合着看。
import { args, fromBottom, launch, open, setConfig, tab, useStore } from "./lib.mjs";

const a = args({ rounds: 6, keep: 1 });
const { browser, page } = await launch();
await open(page);
await useStore(page);
await setConfig(page, { keepTabs: a.keep });
// Prefix match only: a tab label can carry a status mark after the name ("2.gerdr ‖"), see tab() in lib.mjs.
const n = (await page.locator("text=/^\\d+\\.\\S+/").allInnerTexts()).length;
if (n < 2) throw new Error("要 ≥ 2 个 tab（setup.mjs 给两个会话）");
let bad = 0;
for (let r = 0; r < a.rounds; r++) {
  for (let i = 1; i <= n; i++) {
    const name = await tab(page, i);
    const seen = [];
    for (const w of [16, 150, 600, 1500]) {
      await page.waitForTimeout(w);
      seen.push((await fromBottom(page)).find((x) => x.visible)?.fromBottom);
    }
    const stuck = seen[seen.length - 1] > 2;
    if (stuck) bad++;
    console.log(`${r + 1} ${name}  离底部 ${seen.join(" → ")}${stuck ? "   ← 停在半中间" : ""}`);
  }
}
await browser.close();
process.exit(bad ? 1 : 0);
