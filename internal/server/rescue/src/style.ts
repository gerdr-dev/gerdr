// 急救面板的样式：放进 Shadow DOM 里，和主界面互不影响。
// 颜色照 ui/src/index.css 的那套（灰阶 + 一个蓝），看起来是 gerdr 的一部分；但这里是一份拷贝，不读那边 ——
// 那边改了不会跟过来，也不会被改坏。
export const CSS = /* css */ `
:host { all: initial; }
.root {
  --bg: oklch(0.99 0 0); --fg: oklch(0.2 0 0); --card: oklch(1 0 0); --side: oklch(0.975 0 0);
  --muted: oklch(0.96 0 0); --muted-fg: oklch(0.52 0 0); --border: oklch(0.91 0 0); --input: oklch(0.89 0 0);
  --brand: oklch(0.58 0.15 255); --ok: oklch(0.62 0.15 150); --warn: oklch(0.75 0.15 75); --bad: oklch(0.6 0.21 25);
  --mono: "SF Mono", "JetBrains Mono", Menlo, Monaco, Consolas, monospace;
  color-scheme: light;
  font: 13px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif;
  color: var(--fg);
  -webkit-font-smoothing: antialiased;
}
.root.dark {
  --bg: oklch(0.165 0 0); --fg: oklch(0.93 0 0); --card: oklch(0.2 0 0); --side: oklch(0.14 0 0);
  --muted: oklch(0.24 0 0); --muted-fg: oklch(0.66 0 0); --border: oklch(0.29 0 0); --input: oklch(0.31 0 0);
  --brand: oklch(0.7 0.13 250); --ok: oklch(0.72 0.15 150); --warn: oklch(0.8 0.14 80); --bad: oklch(0.68 0.19 25);
  color-scheme: dark;
}
* { box-sizing: border-box; }
[hidden] { display: none !important; }
button { font: inherit; color: inherit; cursor: pointer; }
button:disabled { cursor: default; opacity: .5; }
pre, code { font-family: var(--mono); }
.grow { flex: 1; min-width: 0; }
.muted { color: var(--muted-fg); }
@keyframes spin { to { transform: rotate(360deg); } }
.spin { animation: spin .9s linear infinite; }
@keyframes slide { from { transform: translateX(24px); opacity: 0; } }

/* 关着、页面又报错了：右下角一颗 */
.pill {
  position: fixed; right: 16px; bottom: 16px; z-index: 2147483000;
  display: flex; align-items: stretch; overflow: hidden;
  border: 1px solid color-mix(in oklab, var(--bad) 40%, var(--border)); border-radius: 999px;
  background: var(--card); color: var(--bad); font-weight: 600;
  box-shadow: 0 6px 20px -6px rgb(0 0 0 / .25);
}
.pill button { display: flex; align-items: center; border: 0; background: none; color: inherit; font-weight: inherit; }
.pill .open { gap: 6px; padding: 7px 10px 7px 11px; }
/* 点掉用的 ×：手机上手指要点得中，宽一点 */
.pill .x { padding: 0 11px 0 9px; color: var(--muted-fg); border-left: 1px solid color-mix(in oklab, var(--bad) 25%, var(--border)); }
.pill button:hover { background: color-mix(in oklab, var(--bad) 8%, var(--card)); }

.panel {
  position: fixed; top: 0; right: 0; bottom: 0; z-index: 2147483000;
  display: flex; flex-direction: column; background: var(--bg);
  border-left: 1px solid var(--border);
  box-shadow: -12px 0 32px -20px rgb(0 0 0 / .35);
  animation: slide .18s ease-out;
}
@media (max-width: 767px) { .panel { width: 100% !important; border-left: 0; } .resizer { display: none; } }
.resizer { position: absolute; left: -3px; top: 0; bottom: 0; width: 6px; cursor: col-resize; z-index: 1; }
.resizer:hover, .resizer:active { background: color-mix(in oklab, var(--brand) 45%, transparent); }

.head { display: flex; align-items: center; gap: 10px; padding: 10px 10px 10px 14px; border-bottom: 1px solid var(--border); }
.logo {
  display: grid; place-items: center; width: 28px; height: 28px; flex-shrink: 0; border-radius: 8px;
  color: var(--bad); background: color-mix(in oklab, var(--bad) 12%, transparent);
}
.title { flex: 1; min-width: 0; display: flex; flex-direction: column; line-height: 1.3; }
.title b { font-size: 14px; }
.title span { font-size: 11.5px; color: var(--muted-fg); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.icon { display: grid; place-items: center; width: 28px; height: 28px; border: 0; border-radius: 6px; background: none; color: var(--muted-fg); }
.icon:hover { background: var(--muted); color: var(--fg); }

.status { margin: 10px 12px 0; padding: 9px 11px; border-radius: 8px; font-size: 12.5px; }
.status.ok { display: flex; gap: 8px; align-items: flex-start; color: var(--ok); background: color-mix(in oklab, var(--ok) 9%, transparent); }
.status.ok svg { margin-top: 2px; flex-shrink: 0; }
.status.ok small { display: block; color: var(--muted-fg); font-size: 11.5px; }
.status.bad { color: var(--bad); background: color-mix(in oklab, var(--bad) 9%, transparent); }
.status .row { display: flex; align-items: center; gap: 8px; font-weight: 600; }
.status pre { margin: 6px 0 0; white-space: pre-wrap; word-break: break-word; font-size: 11.5px; line-height: 1.5; max-height: 40vh; overflow: auto; color: var(--fg); opacity: .85; }
.status pre.clip { max-height: 4.6em; overflow: hidden; -webkit-mask-image: linear-gradient(#000 55%, transparent); mask-image: linear-gradient(#000 55%, transparent); }
.link { display: inline-flex; align-items: center; gap: 2px; border: 0; background: none; color: inherit; font-size: 12px; font-weight: 400; opacity: .8; }
.link:hover { opacity: 1; }

.tabs { display: flex; gap: 2px; margin: 10px 12px 0; padding: 3px; border-radius: 9px; background: var(--muted); }
.tabs button {
  flex: 1; display: flex; align-items: center; justify-content: center; gap: 6px; padding: 5px 8px;
  border: 0; border-radius: 6px; background: none; color: var(--muted-fg); font-size: 12.5px;
}
.tabs button.on { background: var(--card); color: var(--fg); box-shadow: 0 1px 2px rgb(0 0 0 / .08); }
.page { flex: 1; min-height: 0; display: flex; flex-direction: column; }

.btn {
  display: inline-flex; align-items: center; gap: 5px; height: 28px; padding: 0 10px; flex-shrink: 0;
  border: 1px solid var(--input); border-radius: 6px; background: var(--card); font-size: 12.5px; white-space: nowrap;
}
.btn:hover:not(:disabled) { background: var(--muted); }
.btn.sm { height: 26px; padding: 0 9px; font-size: 12px; }
.btn.primary { background: var(--brand); border-color: var(--brand); color: white; }
.btn.primary:hover:not(:disabled) { background: color-mix(in oklab, var(--brand) 88%, black); }
.btn.danger { color: var(--bad); border-color: color-mix(in oklab, var(--bad) 50%, var(--input)); }
select, input, textarea { font: inherit; color: var(--fg); background: var(--card); border: 1px solid var(--input); border-radius: 6px; outline: none; }
select:focus, input:focus { border-color: var(--brand); }
select { height: 28px; padding: 0 6px; font-size: 12.5px; min-width: 0; }

.chat { flex: 1; min-height: 0; display: flex; flex-direction: column; }
.bar { display: flex; gap: 6px; padding: 10px 12px; border-bottom: 1px solid var(--border); }
.bar { align-items: center; }
.bar-hint { font-size: 12px; color: var(--muted-fg); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.bar .agent { max-width: 110px; }

.log { flex: 1; min-height: 0; overflow: auto; padding: 14px 14px 6px; display: flex; flex-direction: column; gap: 8px; }
.empty { color: var(--muted-fg); text-align: center; font-size: 12.5px; padding: 32px 20px; }
.empty.big { display: flex; flex-direction: column; gap: 4px; margin: auto 0; }
.empty.big b { color: var(--fg); font-size: 14px; }
.user {
  align-self: flex-end; max-width: 88%; padding: 7px 11px; border-radius: 12px 12px 4px 12px;
  background: var(--muted); white-space: pre-wrap; word-break: break-word;
}
.md { word-break: break-word; }
.md > :first-child { margin-top: 0; }
.md > :last-child { margin-bottom: 0; }
.md p { margin: 0 0 8px; white-space: pre-wrap; }
.md h4 { margin: 12px 0 6px; font-size: 13.5px; }
.md ul, .md ol { margin: 0 0 8px; padding-left: 20px; }
.md li { margin: 2px 0; }
.md code, .user code { font-size: 12px; padding: 1px 4px; border-radius: 4px; background: var(--muted); }
pre.code {
  margin: 0 0 8px; padding: 8px 10px; border-radius: 6px; background: var(--muted); border: 1px solid var(--border);
  font-size: 11.5px; line-height: 1.5; white-space: pre-wrap; word-break: break-word; max-height: 300px; overflow: auto;
}
.tool { display: flex; align-items: center; gap: 6px; font-family: var(--mono); font-size: 11.5px; color: var(--muted-fg); min-width: 0; }
.tool + .tool { margin-top: -5px; }
.tool span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tool svg { flex-shrink: 0; }
.tool.completed svg { color: var(--ok); }
.tool.failed { color: var(--bad); }
.notice { text-align: center; font-size: 12px; color: var(--muted-fg); }
.notice.error { color: var(--bad); }

.card { padding: 10px 12px; border-radius: 8px; border: 1px solid color-mix(in oklab, var(--warn) 55%, var(--border)); background: var(--card); }
.card.done { border-color: var(--border); background: none; color: var(--muted-fg); font-size: 12px; padding: 6px 10px; }
.card.done .muted { opacity: .8; }
.card .q { margin-bottom: 6px; }
.card .qa { margin-bottom: 10px; }
.card pre.code { margin-bottom: 8px; }
.opts { display: flex; flex-wrap: wrap; gap: 6px; }
.opts input { flex: 1; min-width: 120px; height: 26px; padding: 0 8px; font-size: 12px; }

.composer { padding: 6px 12px 12px; }
.state { display: flex; align-items: center; gap: 6px; font-size: 11.5px; color: var(--muted-fg); padding: 0 2px 6px; }
.dot { width: 7px; height: 7px; border-radius: 50%; background: var(--muted-fg); }
.dot.running, .dot.starting { background: var(--brand); animation: pulse 1.2s ease-in-out infinite; }
.dot.waiting { background: var(--warn); }
.dot.error { background: var(--bad); }
.dot.idle { background: var(--ok); }
@keyframes pulse { 50% { opacity: .35; } }
.box { display: flex; align-items: flex-end; gap: 6px; padding: 6px 6px 6px 10px; border: 1px solid var(--input); border-radius: 12px; background: var(--card); }
.box:focus-within { border-color: var(--brand); box-shadow: 0 0 0 3px color-mix(in oklab, var(--brand) 15%, transparent); }
.box textarea { flex: 1; border: 0; background: none; resize: none; padding: 4px 0; font-size: 13px; line-height: 1.5; max-height: 240px; }
.send {
  display: grid; place-items: center; width: 30px; height: 30px; flex-shrink: 0; border: 0; border-radius: 8px;
  background: var(--brand); color: white;
}
.send.stop { background: var(--fg); color: var(--bg); }

.versions { flex: 1; min-height: 0; overflow: auto; padding: 12px 14px; }
.hint { margin: 0 0 10px; font-size: 12px; color: var(--muted-fg); }
.flash { margin-bottom: 10px; padding: 8px 10px; border-radius: 6px; font-size: 12px; color: var(--ok); background: color-mix(in oklab, var(--ok) 9%, transparent); }
.flash.bad { color: var(--bad); background: color-mix(in oklab, var(--bad) 9%, transparent); }
.versions ol { list-style: none; margin: 0; padding: 0; }
.versions li { position: relative; display: flex; align-items: center; gap: 10px; padding: 8px 0 8px 18px; }
/* 左边一条时间线 */
.versions li::before { content: ""; position: absolute; left: 4px; top: 0; bottom: 0; width: 1px; background: var(--border); }
.versions li:first-child::before { top: 50%; }
.versions li:last-child::before { bottom: 50%; }
.versions li i { position: absolute; left: 0; top: 50%; width: 9px; height: 9px; margin-top: -5px; border-radius: 50%; background: var(--bg); border: 2px solid var(--input); }
.versions li.cur i { border-color: var(--brand); background: var(--brand); }
.subj { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12.5px; }
.meta { display: flex; align-items: center; gap: 5px; font-size: 11px; color: var(--muted-fg); }
.meta code { font-size: 10.5px; }
.tag { font-size: 10px; line-height: 15px; padding: 0 5px; border-radius: 4px; border: 1px solid var(--border); }
.tag.brand { color: var(--brand); border-color: color-mix(in oklab, var(--brand) 40%, transparent); }
`;
