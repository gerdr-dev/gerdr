// 对话列表滚动 / 闪动复现脚本的公共部分。怎么用、量的是什么、踩过的坑：docs/dev/SCROLL.md。
//
// 只对着调试实例跑（make dev 那个：~/.gerdr-dev、7871）。脚本会点 tab、改 keepTabs 这类界面状态，
// 对着正在用的 7870 跑会把用户的布局点乱 —— 默认拒绝。
import { chromium } from "playwright-core";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

export const PORT = +(process.env.SCROLLTEST_PORT || 7871);
export const DIR = process.env.SCROLLTEST_DIR || path.join(os.homedir(), ".gerdr-dev");
if ((PORT === 7870 || DIR === path.join(os.homedir(), ".gerdr")) && !process.env.SCROLLTEST_FORCE) {
  console.error("别对着正在用的 gerdr（7870 / ~/.gerdr）跑：先 node setup.mjs <会话 id…> 准备调试实例，再 make dev。");
  process.exit(2);
}

/** 命令行参数：--key=value / --flag，其余的按顺序放进 _ */
export function args(defaults = {}) {
  const o = { ...defaults, _: [] };
  for (const a of process.argv.slice(2)) {
    const m = a.match(/^--([^=]+)(?:=(.*))?$/);
    if (!m) o._.push(a);
    else o[m[1]] = m[2] === undefined ? true : isNaN(+m[2]) ? m[2] : +m[2];
  }
  return o;
}

/**
 * 起浏览器。默认用系统装的 Chrome（channel: chrome，不用下载；和 Electron 一样是 Chromium）；CHROME=路径 可以换。
 * 默认开着滚动条：headless 默认藏滚动条（--hide-scrollbars），而 mac 接了鼠标时滚动条是常显的、占宽度，
 * 宽度不同行高就不同，复现不出来。dpr 默认 2（Retina，行高常常是 .5 的小数，取整误差就是在这上面攒出来的）
 */
export async function launch({ width = 1150, height = 900, dpr = 2, scrollbars = true, video, logFromLoad = false } = {}) {
  const browser = await chromium.launch({
    ...(process.env.CHROME ? { executablePath: process.env.CHROME } : { channel: "chrome" }),
    ignoreDefaultArgs: scrollbars ? ["--hide-scrollbars"] : [],
  });
  const ctx = await browser.newContext({ viewport: { width, height }, deviceScaleFactor: dpr, ...(video ? { recordVideo: { dir: video, size: { width, height } } } : {}) });
  const page = await ctx.newPage();
  await page.addInitScript(installFrameLogger);
  if (logFromLoad) await page.addInitScript(() => window.__startFrameLog());
  page.on("pageerror", (e) => console.log("页面报错:", e.message));
  return { browser, ctx, page };
}

/** 打开界面（带令牌登录），等对话列表出来 */
export async function open(page, { wait = 2500 } = {}) {
  const tok = fs.readFileSync(path.join(DIR, "token"), "utf8").trim();
  await page.goto(`http://127.0.0.1:${PORT}/auth?token=${tok}&next=/`);
  await page.waitForSelector("[data-index]", { timeout: 30000 });
  await page.waitForTimeout(wait);
}

/**
 * 页面里拿到界面自己的 store 模块（window.__store）：改 config、往会话里灌事件（applyEvents）、看 older 状态。
 * 界面是一个文件一个模块发下来的，import 同一个地址拿到的就是页面正在用的那份
 */
export async function useStore(page) {
  const url = await page.evaluate(() => performance.getEntriesByType("resource").map((e) => e.name).find((n) => /\/src\/lib\/store\.ts/.test(n)));
  if (!url) throw new Error("没找到 store 模块");
  await page.evaluate(async (u) => (window.__store = await import(u)), url);
}

export async function setConfig(page, patch) {
  await page.evaluate((p) => window.__store.set({ config: { ...window.__store.get().config, ...p } }), patch);
}

/** 点第 n 个 tab（从 1 起；tab 名是「n.目录名」） */
export async function tab(page, n) {
  const names = (await page.locator("text=/^\\d+\\./").allInnerTexts()).filter((t) => /^\d+\.\S+$/.test(t));
  const name = names.find((t) => t.startsWith(`${n}.`));
  if (!name) throw new Error(`没有第 ${n} 个 tab（有：${names.join(" ")}）`);
  await page.getByText(name, { exact: true }).first().click();
  return name;
}

/** 离底部多远（0 = 在最底下），每个滚动区一项 */
export function fromBottom(page) {
  return page.evaluate(() =>
    [...document.querySelectorAll("[data-virtuoso-scroller]")].map((sc) => ({
      visible: !sc.closest(".invisible"),
      fromBottom: Math.round(sc.scrollHeight - sc.clientHeight - sc.scrollTop),
      scrollTop: Math.round(sc.scrollTop),
      scrollHeight: sc.scrollHeight,
    })),
  );
}

/**
 * 逐帧记录（每个 requestAnimationFrame 一条）：scrollTop、总高、列表是不是还 hidden（Virtuoso 定好初始位置前会藏着）、
 * 屏幕上每一行的 top / 高度。行按 data-item-index 认（= firstItemIndex + 位置）：往前补一段时同一行的它不变；
 * data-index 是在数据里的位置，补了就全变。
 * ro: 另外挂一个 ResizeObserver 记一份 —— 它比 Virtuoso 的晚建，同一阶段排在后面跑，记到的是这一帧真正画出来的样子
 * （rAF 在 ResizeObserver 之前，rAF 里看到错位不等于画出来了，反过来也一样；见 SCROLL.md「哪一帧画出来了」）。
 * point: 多个 pane 时量哪一个（屏幕坐标）。sid: 顺便记这个会话的 older 状态（分段加载：more / loading）。
 * 记录器本身由 launch 用 init script 装进每个页面（window.__startFrameLog），所以也能从页面加载的第一帧记起（launch 的 logFromLoad）。
 */
function installFrameLogger() {
  window.__startFrameLog = ({ point: pt, sid, ro } = {}) => {
    const find = () => {
      const at = pt ? document.elementFromPoint(pt[0], pt[1])?.closest("[data-virtuoso-scroller]") : null;
      return at || [...document.querySelectorAll("[data-virtuoso-scroller]")].find((s) => !s.closest(".invisible"));
    };
    const snap = () => {
      const sc = find();
      if (!sc) return null;
      const top0 = sc.getBoundingClientRect().top, bot0 = top0 + sc.clientHeight;
      const rows = {};
      let n = 0;
      for (const it of sc.querySelectorAll("[data-item-index]")) {
        n++;
        const r = it.getBoundingClientRect();
        if (r.bottom > top0 && r.top < bot0) rows[it.dataset.itemIndex] = [+(r.top - top0).toFixed(1), +r.height.toFixed(1)];
      }
      const lst = sc.querySelector("[data-testid=virtuoso-item-list]");
      return {
        t: Math.round(performance.now()),
        st: Math.round(sc.scrollTop),
        sh: sc.scrollHeight,
        ch: sc.clientHeight,
        hidden: !!lst && getComputedStyle(lst).visibility === "hidden",
        mounted: n,
        rows,
        older: sid && window.__store ? window.__store.get().older[sid] : undefined,
      };
    };
    window.__frames = [];
    window.__roFrames = [];
    const tick = () => {
      const s = snap();
      if (s) window.__frames.push(s);
      window.__frameRaf = requestAnimationFrame(tick);
    };
    tick();
    const sc = ro && find();
    if (sc) {
      const o = new ResizeObserver(() => {
        const s = snap();
        if (s) window.__roFrames.push(s);
      });
      o.observe(sc.querySelector("[data-testid=virtuoso-item-list]"));
      o.observe(sc.firstElementChild);
      window.__ro = o;
    }
  };
}

export async function startFrameLog(page, opts = {}) {
  await page.evaluate((o) => window.__startFrameLog(o), opts);
}

export async function stopFrameLog(page) {
  return page.evaluate(() => {
    cancelAnimationFrame(window.__frameRaf);
    window.__ro?.disconnect();
    return { frames: window.__frames, ro: window.__roFrames };
  });
}

/**
 * 找跳动。相邻两帧里都在屏幕上的行，看它在屏幕上挪了多少（vis）、scrollTop 变了多少（ds）：
 * - 没在滚（scrolling=false）：vis ≠ 0 就是跳 —— 人没动，内容自己挪了（贴着底跟新输出往上走除外）
 * - 在滚：正常是 vis = -ds（内容跟着滚动走）。vis + ds ≠ 0 说明这一帧里布局也变了：
 *     vis ≈ 0 是 Virtuoso / 我们补过了（compensated，只记一笔，不算跳）；剩下的 vis 和正在滚的方向一致、
 *     不超过一帧甩动的量（step）也算补过了（补的那一帧人还在甩）；否则就是跳
 * - 两帧没有一行相同：整屏换了内容（swapped），一定是跳（除非列表还 hidden）
 * 列表还 hidden 的帧（Virtuoso 定位前）看不见，跳了也不算。
 */
export function analyze(frames, { scrolling = false, threshold = 2, step = 60 } = {}) {
  const out = [];
  for (let i = 1; i < frames.length; i++) {
    const a = frames[i - 1], c = frames[i];
    if (c.hidden) continue;
    const ds = c.st - a.st;
    const common = Object.keys(c.rows).filter((k) => k in a.rows);
    const base = { i, t: c.t, ds, sh: [a.sh, c.sh], mounted: [a.mounted, c.mounted], st: [a.st, c.st] };
    if (!common.length) {
      if (Object.keys(a.rows).length && !a.hidden) out.push({ ...base, kind: "swapped", before: Object.keys(a.rows).slice(0, 3), after: Object.keys(c.rows).slice(0, 3) });
      continue;
    }
    // 拿屏幕中间那一行看：顶上那行可能正好在卸载 / 挂上
    const k = common[Math.floor(common.length / 2)];
    const vis = +(c.rows[k][0] - a.rows[k][0]).toFixed(1);
    const layout = +(vis + ds).toFixed(1);
    if (!scrolling) {
      // 贴着底、内容正好跟着 scrollTop 走：是在跟新输出（followOutput），不算跳
      const follow = a.sh - a.ch - a.st < 80 && Math.abs(vis + ds) <= threshold;
      if (Math.abs(vis) > threshold && !follow) out.push({ ...base, kind: "jump", row: k, vis });
    } else if (Math.abs(layout) > threshold) {
      // 这一帧布局变了、也补过了，剩下的 vis 是人这一帧滚的量：方向和前几帧一致、不超过一帧甩动的量（step）就不算跳。
      // 典型的是往前补一段时挂上几百行卡了两百毫秒，攒下的滚轮量一下子走掉
      let recent = 0;
      for (let j = Math.max(1, i - 6); j < i; j++) {
        const d = frames[j].st - frames[j - 1].st;
        if (Math.abs(d) <= step * 3) recent += d; // 跳过补偿那种一下几千的，只看人在往哪滚
      }
      const withScroll = recent !== 0 && Math.sign(vis) === -Math.sign(recent) && Math.abs(vis) <= step;
      if (Math.abs(vis) <= threshold || withScroll) out.push({ ...base, kind: "compensated", row: k, vis, layout });
      else out.push({ ...base, kind: "jump", row: k, vis, layout });
    }
    // 同一行自己的高度变了（DOM 没变也可能变：content-visibility、图片、字体）
    for (const r of common) if (Math.abs(c.rows[r][1] - a.rows[r][1]) > 1) out.push({ ...base, kind: "row-resized", row: r, h: [a.rows[r][1], c.rows[r][1]] });
  }
  return out;
}

export function report(events, { limit = 40 } = {}) {
  const count = (k) => events.filter((e) => e.kind === k).length;
  for (const e of events.filter((e) => e.kind !== "compensated").slice(0, limit)) console.log(JSON.stringify(e));
  console.log(`跳 ${count("jump")}，整屏换掉 ${count("swapped")}，行自己变高矮 ${count("row-resized")}，补偿过 ${count("compensated")}`);
  return count("jump") + count("swapped");
}

/** 触控板式的一甩：一串从大到小的滚轮（惯性）。dir = -1 往上、1 往下 */
export async function fling(page, dir, { v0 = 60, decay = 0.93, gap = 8 } = {}) {
  for (let v = v0; v > 1; v *= decay) {
    await page.mouse.wheel(0, dir * v);
    await page.waitForTimeout(gap);
  }
}

/** 读会话日志（调试实例里的那份） */
export function readEvents(sid) {
  return fs
    .readFileSync(path.join(DIR, "sessions", sid + ".jsonl"), "utf8")
    .split("\n")
    .filter(Boolean)
    .map((l) => JSON.parse(l));
}

/** 把一段事件切成流式的样子：agent 说的话按 size 个字一块，seq 从 seq0 往后排 */
export function streamify(events, { size = 8, seq0 } = {}) {
  let seq = seq0 ?? Math.max(...events.map((e) => e.seq)) + 1000;
  const out = [];
  for (const e of events) {
    const u = e.update;
    if (e.kind === "update" && u?.sessionUpdate === "agent_message_chunk" && u.content?.type === "text") {
      for (let k = 0; k < u.content.text.length; k += size) out.push({ ...e, seq: ++seq, update: { ...u, content: { ...u.content, text: u.content.text.slice(k, k + size) } } });
    } else out.push({ ...e, seq: ++seq });
  }
  return out;
}

/** 往会话里一条条灌事件（要先 useStore），每条之间停 dt 毫秒 */
export async function replay(page, sid, events, { dt = 20 } = {}) {
  for (const e of events) {
    await page.evaluate(([sid, e]) => window.__store.applyEvents(sid, [e], false), [sid, e]);
    if (dt) await page.waitForTimeout(dt);
  }
}
