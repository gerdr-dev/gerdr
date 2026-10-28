// 急救面板的脚本（见 index.html 开头的说明）。原生 JS、不依赖任何包：主界面坏了的时候这一页还得能用。
// 只做修界面要的那点：看错误、回退版本、和一个 agent 对话（发消息、看回复和工具调用、审批 / 提问、打断）。
"use strict";

const $ = (id) => document.getElementById(id);
const el = (tag, attrs, ...kids) => {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (k === "class") e.className = v;
    else if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
    else if (v !== undefined && v !== false) e.setAttribute(k, v === true ? "" : v);
  }
  for (const k of kids.flat()) if (k !== null && k !== undefined && k !== false) e.append(k instanceof Node ? k : String(k));
  return e;
};

async function req(method, path, body) {
  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: body !== undefined ? { "Content-Type": "application/json", "X-Gerdr": "1" } : { "X-Gerdr": "1" },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let data;
  try {
    data = text ? JSON.parse(text) : undefined;
  } catch {
    data = undefined;
  }
  if (!res.ok) throw new Error((data && data.error) || res.status + " " + res.statusText);
  return data;
}

const uuid = () => (crypto.randomUUID ? crypto.randomUUID() : String(Date.now()) + Math.random().toString(16).slice(2));
const ago = (sec) => {
  const m = Math.floor((Date.now() / 1000 - sec) / 60);
  if (m < 1) return "刚刚";
  if (m < 60) return m + " 分钟前";
  const h = Math.floor(m / 60);
  return h < 24 ? h + " 小时前" : Math.floor(h / 24) + " 天前";
};

const state = {
  hello: null,
  uiDir: "",
  status: null,
  sessions: {}, // id → summary
  current: "", // 正在看的会话
  events: [],
  lastSeq: 0,
};

// ---- 界面状态 ----

function errorText() {
  const st = state.status;
  if (!st) return "";
  const parts = [];
  if (st.build_error) parts.push("编译错误：\n" + st.build_error);
  for (const e of st.runtime_errors || []) parts.push("运行时错误（" + (e.source || "") + "）：" + e.message + (e.detail ? "\n" + e.detail : ""));
  if (st.git && st.git.merging) parts.push("界面目录正合并到一半，有冲突：" + (st.git.conflicts || []).join(", "));
  return parts.join("\n\n").slice(0, 6000);
}

async function loadStatus() {
  const box = $("status");
  try {
    state.status = await req("GET", "/api/ui/status");
    state.uiDir = state.status.dir;
    box.textContent = "";
    const t = errorText();
    box.append(el("div", { class: "muted" }, "界面目录：", el("code", {}, state.uiDir)));
    if (t) box.append(el("pre", { class: "err" }, t));
    else box.append(el("div", { class: "ok" }, "没有编译错误，也没有页面报上来的运行时错误。"));
    const g = state.status.git || {};
    if (g.uncommitted_files) box.append(el("div", { class: "muted" }, g.uncommitted_files + " 个文件改了还没提交"));
    if (!state.status.runtime_errors && !state.status.build_error)
      box.append(el("div", { class: "muted" }, "（运行时错误要主界面开着才报得上来）"));
  } catch (e) {
    box.textContent = "读不到：" + e.message;
  }
}

// ---- 版本 ----

async function loadHistory() {
  const box = $("history");
  let list;
  try {
    list = await req("GET", "/api/ui/history");
  } catch (e) {
    box.textContent = e.message;
    return;
  }
  box.textContent = "";
  for (const c of list) {
    const btn = el("button", {}, "回到这一版");
    let armed = false;
    btn.addEventListener("click", async () => {
      // 点两下才动手：防手滑（这一页不用浏览器的 confirm 弹窗，Electron 里会卡住页面）
      if (!armed) {
        armed = true;
        btn.textContent = "确定？再点一次";
        btn.classList.add("danger");
        setTimeout(() => {
          armed = false;
          btn.textContent = "回到这一版";
          btn.classList.remove("danger");
        }, 4000);
        return;
      }
      btn.disabled = true;
      btn.textContent = "回退中…";
      // 提示放在列表刷新之后：loadHistory 会清空这一块
      let msg, bad = false;
      try {
        const r = await req("POST", "/api/ui/restore", { sha: c.sha });
        msg = "已回到 " + c.sha.slice(0, 8) + (r.saved ? "（之前的样子存成了 " + r.saved.slice(0, 8) + "）" : "") + "。打开主界面看看。";
      } catch (e) {
        (msg = "回退失败：" + e.message), (bad = true);
      }
      await loadHistory();
      flash(msg, bad);
      setTimeout(loadStatus, 800);
    });
    box.append(
      el(
        "div",
        { class: "ver" },
        c.upstream ? el("span", { class: "tag" }, "原版") : null,
        c.current ? el("span", { class: "tag" }, "现在") : null,
        el("span", { class: "s", title: c.subject }, c.subject || c.sha.slice(0, 8)),
        el("span", { class: "t" }, ago(c.time)),
        c.current ? null : btn,
      ),
    );
  }
  if (!list.length) box.textContent = "没有历史";
}

function flash(text, bad) {
  const box = $("history");
  box.prepend(el("div", { class: bad ? "err" : "ok", style: "margin-bottom:6px;font-size:12px" }, text));
}

// ---- 会话 ----

function rescueSessions() {
  return Object.values(state.sessions)
    .filter((s) => s.cwd === state.uiDir)
    .sort((a, b) => b.updatedAt - a.updatedAt);
}

function renderSessionPicker() {
  const sel = $("sessions");
  sel.textContent = "";
  const list = rescueSessions();
  sel.append(el("option", { value: "" }, list.length ? "界面目录下的对话…" : "（还没有）"));
  for (const s of list) sel.append(el("option", { value: s.id, selected: s.id === state.current }, (s.title || s.agent) + " · " + s.status));
  const a = $("agent");
  if (!a.options.length) {
    for (const ag of state.hello.agents) a.append(el("option", { value: ag.id, disabled: !ag.available, selected: ag.id === state.hello.config?.defaultAgent }, ag.name));
  }
}

function renderBar() {
  const s = state.sessions[state.current];
  $("st").textContent = s ? { idle: "空闲", running: "在干活…", waiting: "等你处理", error: "出错了" + (s.error ? "：" + s.error : ""), starting: "启动中…" }[s.status] || s.status : "";
  $("cancel").hidden = !(s && (s.status === "running" || s.status === "waiting"));
}

async function newSession() {
  const agent = $("agent").value;
  if (!agent) return;
  $("new").disabled = true;
  try {
    const mode = (state.hello.config?.defaultModes || {})[agent];
    const s = await req("POST", "/api/sessions", { agent, cwd: state.uiDir, mode: mode || undefined, title: "急救：修界面" });
    state.sessions[s.id] = s;
    select(s.id);
    // 先填好第一句（带上错误），人看一眼再发
    const t = errorText();
    $("input").value =
      "gerdr 的界面（就是这个目录）被改坏了，帮我修好。先读 AGENTS.md，再跑 `gerdr ui status` 看错误；" +
      "修完再跑一次确认没有错误了。只改坏掉的地方，别顺手重构。" +
      (t ? "\n\n现在报的错：\n" + t : "");
    $("input").focus();
  } catch (e) {
    alertLine("开会话失败：" + e.message);
  } finally {
    $("new").disabled = false;
  }
}

function select(id) {
  state.current = id;
  state.events = [];
  state.lastSeq = 0;
  renderSessionPicker();
  renderBar();
  renderLog();
  watch();
}

async function send() {
  const text = $("input").value.trim();
  if (!text || !state.current) return;
  $("send").disabled = true;
  try {
    await req("POST", "/api/sessions/" + state.current + "/send", { content: [{ type: "text", text }], id: uuid() });
    $("input").value = "";
  } catch (e) {
    alertLine("发送失败：" + e.message);
  } finally {
    $("send").disabled = false;
  }
}

function alertLine(text) {
  $("log").append(el("div", { class: "notice error" }, text));
  scrollDown(true);
}

// ---- 对话：日志 → 一列 ----

function renderLog() {
  const log = $("log");
  const stick = log.scrollHeight - log.scrollTop - log.clientHeight < 80;
  log.textContent = "";
  if (!state.current) {
    log.append(el("div", { class: "empty" }, "选一个对话，或者新建一个：agent 在界面目录里干活，错误信息会自动带上。"));
    return;
  }
  // 流式片段拼成整段；工具调用按 id 合并；审批 / 提问画成卡片，答过的变灰
  const items = [];
  const tools = new Map();
  const reqs = new Map();
  let text = null;
  const textOf = (content) => (content || []).map((c) => (c.type === "text" ? c.text : c.type === "resource_link" ? "[" + c.name + "]" : "")).join("");
  for (const e of state.events) {
    if (e.kind === "user") {
      text = null;
      items.push({ k: "user", text: textOf(e.content) });
    } else if (e.kind === "update" && e.update) {
      const u = e.update;
      if (u.sessionUpdate === "agent_message_chunk") {
        const t = u.content && u.content.type === "text" ? u.content.text : "";
        if (!text) items.push((text = { k: "text", text: "" }));
        text.text += t;
      } else if (u.sessionUpdate === "tool_call" || u.sessionUpdate === "tool_call_update") {
        text = null;
        let t = tools.get(u.toolCallId);
        if (!t) {
          t = { k: "tool", title: "", status: "pending" };
          tools.set(u.toolCallId, t);
          items.push(t);
        }
        if (u.title) t.title = u.title;
        if (u.status) t.status = u.status;
      }
    } else if (e.kind === "permission" || e.kind === "ask") {
      text = null;
      const it = { k: e.kind, reqId: e.reqId, request: e.request, done: false };
      reqs.set(e.reqId, it);
      items.push(it);
    } else if (e.kind === "permission_done" || e.kind === "ask_done") {
      const it = reqs.get(e.reqId);
      if (it) (it.done = true), (it.response = e.response);
    } else if (e.kind === "turn_end") {
      text = null;
      if (e.error) items.push({ k: "notice", level: "error", text: e.error });
    } else if (e.kind === "notice") {
      text = null;
      items.push({ k: "notice", level: e.level, text: e.text });
    }
  }
  for (const it of items) log.append(renderItem(it));
  if (!items.length) log.append(el("div", { class: "empty" }, "还没有消息"));
  scrollDown(stick);
}

function renderItem(it) {
  switch (it.k) {
    case "user":
      return el("div", { class: "msg user" }, it.text);
    case "text":
      return el("div", { class: "msg text" }, it.text);
    case "tool":
      return el("div", { class: "tool" + (it.status === "failed" ? " failed" : "") }, (it.status === "completed" ? "✓ " : it.status === "failed" ? "✗ " : "… ") + (it.title || "工具调用"));
    case "notice":
      return el("div", { class: "msg notice" + (it.level === "error" ? " error" : "") }, it.text);
    case "permission":
      return permissionCard(it);
    case "ask":
      return askCard(it);
  }
  return el("div");
}

function permissionCard(it) {
  const r = it.request || {};
  const title = (r.toolCall && r.toolCall.title) || "要你批准";
  const card = el("div", { class: "card" + (it.done ? " done" : "") }, el("div", { class: "q" }, "要你批准：", el("b", {}, title)));
  const input = r.toolCall && r.toolCall.rawInput;
  if (input && (input.command || input.file_path)) card.append(el("pre", { class: "muted", style: "margin-bottom:6px" }, input.command || input.file_path));
  if (it.done) {
    const opt = (r.options || []).find((o) => o.optionId === (it.response || {}).optionId);
    card.append(el("div", { class: "muted" }, opt ? "→ " + opt.name : "已取消"));
    return card;
  }
  const opts = el("div", { class: "opts" });
  for (const o of r.options || []) {
    opts.append(
      el(
        "button",
        {
          class: o.kind && o.kind.startsWith("allow") ? "primary" : "",
          onclick: (ev) => {
            ev.target.disabled = true;
            req("POST", "/api/sessions/" + state.current + "/answer", { reqId: it.reqId, response: { optionId: o.optionId } }).catch((e) => alertLine(e.message));
          },
        },
        o.name,
      ),
    );
  }
  card.append(opts);
  return card;
}

function askCard(it) {
  const qs = (it.request && it.request.questions) || [];
  const card = el("div", { class: "card" + (it.done ? " done" : "") });
  if (it.done) {
    const ans = (it.response || {}).answers || {};
    for (const q of qs) card.append(el("div", { class: "muted" }, q.question + " → " + ((ans[q.id] || []).join(", ") || "（没回答）")));
    return card;
  }
  const chosen = {};
  for (const q of qs) {
    chosen[q.id] = [];
    card.append(el("div", { class: "q" }, el("b", {}, q.question)));
    const opts = el("div", { class: "opts", style: "margin-bottom:8px" });
    for (const o of q.options || []) {
      const b = el("button", { title: o.description || "" }, o.label);
      b.addEventListener("click", () => {
        const cur = chosen[q.id];
        const i = cur.indexOf(o.label);
        if (q.multiSelect) i >= 0 ? cur.splice(i, 1) : cur.push(o.label);
        else chosen[q.id] = i >= 0 ? [] : [o.label];
        for (const x of opts.children) x.classList.toggle("primary", chosen[q.id].includes(x.textContent));
      });
      opts.append(b);
    }
    const other = el("textarea", { rows: "1", placeholder: "或者自己写", style: "flex:1;min-width:160px;padding:3px 6px;font-size:12px" });
    other.addEventListener("input", () => (chosen[q.id + ":other"] = other.value.trim()));
    opts.append(other);
    card.append(opts);
  }
  const submit = el("button", { class: "primary" }, "提交");
  submit.addEventListener("click", () => {
    const answers = {};
    for (const q of qs) {
      const v = [...chosen[q.id]];
      const o = chosen[q.id + ":other"];
      if (o) q.multiSelect ? v.push(o) : v.splice(0, v.length, o);
      answers[q.id] = v;
    }
    submit.disabled = true;
    req("POST", "/api/sessions/" + state.current + "/answer", { reqId: it.reqId, response: { answers } }).catch((e) => alertLine(e.message));
  });
  const skip = el("button", {}, "跳过");
  skip.addEventListener("click", () =>
    req("POST", "/api/sessions/" + state.current + "/answer", { reqId: it.reqId, response: { cancelled: true, reason: "skipped" } }).catch((e) => alertLine(e.message)),
  );
  card.append(el("div", { class: "opts" }, submit, skip));
  return card;
}

function scrollDown(force) {
  const log = $("log");
  if (force) log.scrollTop = log.scrollHeight;
}

// ---- 推送：/ws，只订阅正在看的这一个会话 ----

let ws = null;
let retry = 0;

function connect() {
  const sock = new WebSocket((location.protocol === "https:" ? "wss:" : "ws:") + "//" + location.host + "/ws");
  ws = sock;
  sock.onopen = () => {
    retry = 0;
    watch();
  };
  sock.onmessage = (ev) => {
    let batch;
    try {
      batch = JSON.parse(ev.data);
    } catch {
      return;
    }
    if (!Array.isArray(batch)) batch = [batch];
    let dirtyLog = false;
    let dirtyList = false;
    for (const m of batch) {
      if (m.t === "session" && m.session) {
        const prev = state.sessions[m.id];
        // agent 一轮干完（多半刚改过界面）：左边的状态和历史跟着刷新，不用人去点「刷新」
        if (m.id === state.current && prev && (prev.status === "running" || prev.status === "waiting") && m.session.status !== prev.status && m.session.status !== "waiting")
          setTimeout(() => (loadStatus(), loadHistory()), 600);
        state.sessions[m.id] = m.session;
        dirtyList = true;
      } else if (m.t === "session_removed") {
        delete state.sessions[m.id];
        dirtyList = true;
      } else if (m.t === "events" && m.id === state.current) {
        if (m.reset) (state.events = []), (state.lastSeq = 0);
        for (const e of m.events || []) {
          if (e.seq <= state.lastSeq) continue;
          state.events.push(e);
          state.lastSeq = e.seq;
        }
        dirtyLog = true;
      }
    }
    if (dirtyList) renderSessionPicker(), renderBar();
    if (dirtyLog) renderLog();
  };
  sock.onclose = () => {
    if (ws !== sock) return;
    ws = null;
    setTimeout(connect, Math.min(8000, 300 * 2 ** retry++));
  };
}

function watch() {
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ t: "watch", ids: state.current ? [state.current] : [] }));
}

setInterval(() => {
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ t: "ping" }));
}, 20000);

// ---- 开机 ----

async function boot() {
  try {
    state.hello = await req("GET", "/api/hello");
  } catch (e) {
    $("log").textContent = "";
    $("log").append(el("div", { class: "empty" }, "连不上 gerdr：" + e.message + "（没登录的话先从 gerdr 给的链接打开一次）"));
    return;
  }
  for (const s of state.hello.sessions) state.sessions[s.id] = s;
  await loadStatus();
  renderSessionPicker();
  const last = rescueSessions()[0];
  if (last) select(last.id);
  loadHistory();
  connect();
}

$("refresh").addEventListener("click", () => (loadStatus(), loadHistory()));
$("new").addEventListener("click", newSession);
$("sessions").addEventListener("change", (e) => e.target.value && select(e.target.value));
$("send").addEventListener("click", send);
$("cancel").addEventListener("click", () => state.current && req("POST", "/api/sessions/" + state.current + "/cancel", {}).catch((e) => alertLine(e.message)));
$("input").addEventListener("keydown", (e) => {
  if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && !e.isComposing) {
    e.preventDefault();
    send();
  }
});
boot();
