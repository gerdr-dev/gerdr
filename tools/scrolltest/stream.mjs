// agent 正在输出时：人往上翻着看（内容不该动）/ 贴着底（应该一直跟到底）。
//   node stream.mjs --at="用户说的某句话" [--tab=1] [--up=600] [--dt=20] [--size=8]
// 把会话截到 --at 那句用户消息为止（含它），然后把它后面那一轮当成流式输出一点点灌进去（agent 的话按 --size 个字一块，
// 每块隔 --dt 毫秒）。--up：灌之前往上翻多少像素（0 = 贴着底，看跟不跟）。
// 只在界面这一侧灌（store.applyEvents），不碰服务端、不真跑 agent。
import { analyze, args, fromBottom, launch, open, readEvents, replay, report, startFrameLog, stopFrameLog, streamify, tab, useStore } from "./lib.mjs";

const a = args({ tab: 1, up: 600, dt: 20, size: 8 });
if (!a.at) {
  console.error('用法：node stream.mjs --at="用户说的某句话"');
  process.exit(2);
}
const { browser, page } = await launch();
await open(page);
await useStore(page);
await tab(page, a.tab);
await page.waitForTimeout(1500);
const sid = await page.evaluate(() => {
  const w = window.__store.get().layout.workspaces[0];
  return w.tabs.find((t) => t.id === w.activeTab)?.root.sessionId;
});
const all = readEvents(sid);
// 用户的话两种记法：gerdr 发的是 kind=user；agent 那边回放 / 接管来的是 user_message_chunk 更新
const isUser = (e) => e.kind === "user" || e.update?.sessionUpdate === "user_message_chunk";
const cut = all.findIndex((e) => isUser(e) && JSON.stringify(e.content ?? e.update?.content ?? "").includes(a.at));
if (cut < 0) throw new Error(`会话里没有用户说过「${a.at}」`);
// 灌到这一轮结束（turn_end），没有的话到下一句用户的话之前
let end = all.findIndex((e, i) => i > cut && e.kind === "turn_end");
if (end < 0) end = all.findIndex((e, i) => i > cut + 1 && isUser(e)) - 1;
if (end < 0) end = all.length - 1;
const rest = streamify(all.slice(cut + 1, end + 1), { size: a.size });
console.log(`截到第 ${cut} 条，往后流式灌 ${rest.length} 条`);
await page.evaluate(([sid, base]) => window.__store.applyEvents(sid, base, true), [sid, all.slice(0, cut + 1)]);
await page.waitForTimeout(2500);
const vp = page.viewportSize();
await page.mouse.move(vp.width * 0.6, vp.height * 0.45);
for (let y = 0; y < a.up; y += 50) {
  await page.mouse.wheel(0, -50);
  await page.waitForTimeout(30);
}
await page.waitForTimeout(800);
await startFrameLog(page);
await replay(page, sid, rest, { dt: a.dt });
await page.waitForTimeout(1000);
const { frames } = await stopFrameLog(page);
const fb = (await fromBottom(page)).find((x) => x.visible);
let bad;
if (a.up) bad = report(analyze(frames));
else {
  // 贴着底：内容一直在往上走是对的，只看最后是不是还在底部
  console.log(`灌完离底部 ${fb.fromBottom}px`);
  bad = fb.fromBottom > 2 ? 1 : 0;
}
await browser.close();
process.exit(bad ? 1 : 0);
