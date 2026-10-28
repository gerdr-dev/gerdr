// 准备调试实例：把正在用的 gerdr 里的几个会话复制到 ~/.gerdr-dev，每个会话一个 tab，打开虚拟滚动。
//   node setup.mjs <会话 id> [<会话 id>…]
// 会话 id 用 `gerdr session list` 看；闪的那个会话直接拿来最好 —— 闪不闪跟内容关系很大（代码块、表格、图片、几千行的大回复）。
// 只读 ~/.gerdr，不改它。要在调试实例没开着的时候跑（开着的话它退出时会把自己的布局写回去，盖掉这里写的）。
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { DIR, PORT } from "./lib.mjs";

const ids = process.argv.slice(2);
if (!ids.length) {
  console.error("用法：node setup.mjs <会话 id> [<会话 id>…]");
  process.exit(2);
}
const busy = await new Promise((ok) => {
  const s = net.connect(PORT, "127.0.0.1", () => (s.destroy(), ok(true)));
  s.on("error", () => ok(false));
});
if (busy) {
  console.error(`调试实例（${PORT}）开着：先停掉再跑，跑完再 make dev`);
  process.exit(2);
}

const SRC = path.join(os.homedir(), ".gerdr");
const index = JSON.parse(fs.readFileSync(path.join(SRC, "sessions", "index.json"), "utf8"));
fs.mkdirSync(path.join(DIR, "sessions"), { recursive: true });
const picked = ids.map((id) => {
  const e = index.find((x) => x.id === id || x.id.startsWith(id));
  if (!e) throw new Error(`~/.gerdr 里没有会话 ${id}`);
  fs.copyFileSync(path.join(SRC, "sessions", e.id + ".jsonl"), path.join(DIR, "sessions", e.id + ".jsonl"));
  return e;
});
fs.writeFileSync(path.join(DIR, "sessions", "index.json"), JSON.stringify(picked));
const tabs = picked.map((e, i) => ({ id: `t${i + 1}`, focus: `p${i + 1}`, root: { type: "pane", id: `p${i + 1}`, sessionId: e.id } }));
fs.writeFileSync(path.join(DIR, "layout.json"), JSON.stringify({ version: 1, active: "w1", workspaces: [{ id: "w1", name: "default", activeTab: "t1", tabs }] }));
const cfgPath = path.join(DIR, "config.json");
const cfg = fs.existsSync(cfgPath) ? JSON.parse(fs.readFileSync(cfgPath, "utf8")) : {};
fs.writeFileSync(cfgPath, JSON.stringify({ ...cfg, virtualList: true }));
for (const [i, e] of picked.entries()) console.log(`tab ${i + 1}: ${e.id}  ${e.title ?? ""}  (${(fs.statSync(path.join(DIR, "sessions", e.id + ".jsonl")).size / 1e6).toFixed(1)} MB)`);
console.log("好了：make dev（或 GERDR_UI=$PWD/ui GERDR_DIR=~/.gerdr-dev GERDR_PORT=7871 go run ./cmd/gerdr）起调试实例");
