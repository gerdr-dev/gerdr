// Synthetic sessions for measuring tab switches: one heavy on images, one on videos, one on long text, written into
// the debug instance (~/.gerdr-dev) with one tab each (plus any real sessions given, copied like setup.mjs does).
//   node gen.mjs [--turns=40] [--media=/tmp/gerdr-tabmedia] [<real session id>…]
// --media: a folder with shot*.png (screenshots), paste*.jpg (pasted into the composer, stored inline as base64 the
// way older logs keep them) and clip*.mp4. Run it with the debug instance stopped, like setup.mjs.
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { DIR, PORT, args } from "./lib.mjs";

const a = args({ turns: 40, media: "/tmp/gerdr-tabmedia" });
const busy = await new Promise((ok) => {
  const s = net.connect(PORT, "127.0.0.1", () => (s.destroy(), ok(true)));
  s.on("error", () => ok(false));
});
if (busy) {
  console.error(`debug instance (${PORT}) is running: stop it first, then make dev`);
  process.exit(2);
}
const files = fs.readdirSync(a.media).sort();
const pick = (re) => files.filter((f) => re.test(f)).map((f) => path.join(a.media, f));
const shots = pick(/^shot.*\.png$/), pastes = pick(/^paste.*\.jpg$/), clips = pick(/^clip.*\.mp4$/);
if (!shots.length || !pastes.length || !clips.length) throw new Error(`need shot*.png, paste*.jpg and clip*.mp4 in ${a.media}`);
const pasteData = pastes.map((p) => fs.readFileSync(p).toString("base64"));

const para = (n) =>
  Array.from({ length: n }, (_, i) => `第 ${i + 1} 点：这里是一段模拟的说明文字，讲清楚为什么这么改、改了哪些文件、有什么风险。Some English in between, \`inline code\` and a [link](https://example.com).`).join("\n\n");
const code = (lines) => "```ts\n" + Array.from({ length: lines }, (_, i) => `export function fn${i}(x: number): number { return x * ${i} + ${i % 7}; } // line ${i}`).join("\n") + "\n```";
const table = (rows) => "| 文件 | 改动 | 说明 |\n|---|---|---|\n" + Array.from({ length: rows }, (_, i) => `| src/file${i}.ts | +${i * 3} −${i} | 调整了第 ${i} 处逻辑 |`).join("\n");
const output = (lines) => Array.from({ length: lines }, (_, i) => `2026-11-16T08:${String(i % 60).padStart(2, "0")}:00Z INFO request ${i} handled in ${i % 97}ms path=/api/items/${i}`).join("\n");

function session(id, cwd, title, turn) {
  let seq = 1, ts = Date.now() - a.turns * 120000;
  const ev = [];
  const push = (e) => ev.push({ seq: seq++, ts: (ts += 1500), ...e });
  for (let i = 0; i < a.turns; i++) {
    const mid = `${id}-u${i}`;
    const t = turn(i);
    push({ kind: "user", messageId: mid, content: t.user });
    push({ kind: "turn_start", messageId: mid });
    for (const [k, tool] of (t.tools || []).entries()) {
      const tc = `${id}-t${i}-${k}`;
      push({ kind: "update", update: { sessionUpdate: "tool_call", toolCallId: tc, kind: "execute", status: "pending", title: tool.title } });
      push({ kind: "update", update: { sessionUpdate: "tool_call_update", toolCallId: tc, kind: "execute", status: "completed", title: tool.title, rawInput: { command: tool.title }, content: [{ type: "content", content: { type: "text", text: tool.out } }] } });
    }
    push({ kind: "update", update: { sessionUpdate: "agent_message_chunk", messageId: `${id}-a${i}`, content: { type: "text", text: t.reply } } });
    push({ kind: "turn_end", messageId: mid, stopReason: "end_turn", durationMs: 30000 });
  }
  fs.writeFileSync(path.join(DIR, "sessions", id + ".jsonl"), ev.map((e) => JSON.stringify(e)).join("\n") + "\n");
  return { id, agent: "claude", cwd, title, createdAt: ev[0].ts, updatedAt: ts, mode: "default" };
}

fs.mkdirSync(path.join(DIR, "sessions"), { recursive: true });
const made = [
  session("aaaaimages001", "/tmp/images", "多图", (i) => ({
    user: [{ type: "image", mimeType: "image/jpeg", data: pasteData[i % pasteData.length] }, { type: "text", text: `看下这张截图，第 ${i} 轮` }],
    tools: [{ title: "screenshot", out: "saved" }],
    reply: `${para(2)}\n\n${[0, 1, 2].map((k) => `![截图 ${k}](${shots[(i * 3 + k) % shots.length]})`).join("\n\n")}\n\n${para(1)}`,
  })),
  session("bbbbvideos001", "/tmp/videos", "多视频", (i) => ({
    user: [{ type: "resource_link", uri: "file://" + clips[i % clips.length], name: path.basename(clips[i % clips.length]) }, { type: "text", text: `这段录屏第 ${i} 轮` }],
    reply: `${para(2)}\n\n![录屏](${clips[(i + 1) % clips.length]})\n\n![录屏](${clips[(i + 2) % clips.length]})\n\n${para(1)}`,
  })),
  session("cccctext00001", "/tmp/text", "长文本", (i) => ({
    user: [{ type: "text", text: `第 ${i} 轮：${para(1)}` }],
    tools: [{ title: "cat logs", out: output(400) }, { title: "git diff", out: output(150) }],
    reply: `${para(6)}\n\n${code(60)}\n\n${table(25)}\n\n${para(4)}`,
  })),
];
// Real sessions, copied as they are
const SRC = path.join(os.homedir(), ".gerdr");
const index = JSON.parse(fs.readFileSync(path.join(SRC, "sessions", "index.json"), "utf8"));
for (const id of a._) {
  const e = index.find((x) => x.id === id || x.id.startsWith(id));
  if (!e) throw new Error(`no session ${id} in ~/.gerdr`);
  fs.copyFileSync(path.join(SRC, "sessions", e.id + ".jsonl"), path.join(DIR, "sessions", e.id + ".jsonl"));
  made.push(e);
}
fs.writeFileSync(path.join(DIR, "sessions", "index.json"), JSON.stringify(made));
const tabs = made.map((e, i) => ({ id: `t${i + 1}`, focus: `p${i + 1}`, root: { type: "pane", id: `p${i + 1}`, sessionId: e.id } }));
fs.writeFileSync(path.join(DIR, "layout.json"), JSON.stringify({ version: 1, active: "w1", workspaces: [{ id: "w1", name: "default", activeTab: "t1", tabs }] }));
const cfgPath = path.join(DIR, "config.json");
const cfg = fs.existsSync(cfgPath) ? JSON.parse(fs.readFileSync(cfgPath, "utf8")) : {};
fs.writeFileSync(cfgPath, JSON.stringify({ ...cfg, virtualList: true }));
for (const [i, e] of made.entries()) console.log(`tab ${i + 1}: ${e.id}  ${e.title ?? ""}  (${(fs.statSync(path.join(DIR, "sessions", e.id + ".jsonl")).size / 1e6).toFixed(1)} MB)`);
console.log("now: make dev, then node tabswitch.mjs");
