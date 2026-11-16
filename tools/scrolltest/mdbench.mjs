// What one message's Markdown costs to render, and in which step: our Markdown component (streamdown + our plugins),
// streamdown with no plugins, streamdown's block split alone, and plain text as the floor (React + DOM only).
//   node mdbench.mjs [--cpu=1] [--runs=30]
// Measured 2026-11-16 (M3, production React): a typical reply (880 chars) ~2ms, a 25-row table ~3ms (GFM tables are
// the costly part: one component per cell, plus the copy / download controls), our plugins and components +0.3-0.7ms,
// the block split ~0. Markdown per message is cheap; what a first visit pays is the number and length of the rows.
import { args, launch, open } from "./lib.mjs";

const a = args({ cpu: 1, runs: 30 });
const { browser, page } = await launch();
await open(page);
if (a.cpu > 1) await (await page.context().newCDPSession(page)).send("Emulation.setCPUThrottlingRate", { rate: a.cpu });
const para = "这一步改了 `ChatList.tsx` 里量行高的地方：以前每次都重新量，现在按 key 记住，见 [SCROLL.md](docs/dev/SCROLL.md)。Some English with **bold** and a path ~/dev/x.ts too.";
const samples = {
  "typical reply (6 paragraphs + a list, ~1.5KB)": [para, para, "- 第一点 `a`\n- 第二点 **b**\n- 第三点 [c](https://example.com)", para, para, para, para].join("\n\n"),
  "table (25 rows)": "| 文件 | 改动 | 说明 |\n|---|---|---|\n" + Array.from({ length: 25 }, (_, i) => `| src/file${i}.ts | +${i * 3} −${i} | 调整了第 ${i} 处 \`逻辑\` |`).join("\n"),
};
const res = await page.evaluate(async ({ samples, runs }) => {
  const url = (re) => performance.getEntriesByType("resource").map((e) => e.name).find((n) => re.test(n));
  const React = await import("react");
  const { createRoot } = await import("react-dom/client");
  const { flushSync } = await import("react-dom");
  const sd = await import("streamdown");
  const { Markdown } = await import(url(/packages\/chat\/src\/components\/Markdown\.tsx/));
  const box = document.createElement("div");
  box.style.cssText = "position:absolute;left:-9999px;top:0;width:760px";
  document.body.append(box);
  const h = React.createElement;
  const ways = {
    "our Markdown": (t) => h(Markdown, null, t),
    "streamdown defaults": (t) => h(sd.Streamdown, null, t),
    "defaults, no rehype": (t) => h(sd.Streamdown, { rehypePlugins: [] }, t),
    "defaults, no remark": (t) => h(sd.Streamdown, { remarkPlugins: [] }, t),
    "streamdown, no plugins": (t) => h(sd.Streamdown, { remarkPlugins: [], rehypePlugins: [] }, t),
    "plain text (floor)": (t) => h("div", null, t),
  };
  const out = {};
  for (const [name, text] of Object.entries(samples)) {
    out[name] = { chars: text.length };
    for (const [way, el] of Object.entries(ways)) {
      const ts = [];
      for (let i = 0; i < runs + 3; i++) {
        const root = createRoot(box);
        // A new string each time: nothing memoised on the exact string carries over
        const t = text + " ".repeat(i % 2);
        const t0 = performance.now();
        flushSync(() => root.render(el(t)));
        box.getBoundingClientRect(); // style + layout of what was mounted
        const d = performance.now() - t0;
        root.unmount();
        if (i >= 3) ts.push(d);
      }
      ts.sort((x, y) => x - y);
      out[name][way] = +ts[ts.length >> 1].toFixed(2);
    }
    const ts = [];
    for (let i = 0; i < runs; i++) { const t0 = performance.now(); sd.parseMarkdownIntoBlocks(text + " ".repeat(i % 2)); ts.push(performance.now() - t0); }
    ts.sort((x, y) => x - y);
    out[name]["block split only"] = +ts[ts.length >> 1].toFixed(2);
  }
  return out;
}, { samples, runs: a.runs });
console.log(`median ms per render, cpu ${a.cpu}x, React: ${await page.evaluate(() => window.__GERDR_UI__.react)}`);
console.table(res);
await browser.close();
