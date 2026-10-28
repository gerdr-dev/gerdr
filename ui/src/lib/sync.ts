// 和服务端保持同步：开机拉一次 /api/hello，之后靠 WebSocket 推。断了自己重连（重连后
// 重新拉 hello、重新订阅，服务端会把订阅的会话日志整份再推一遍 —— 不用自己对账）。
import { api, ApiError, clientId } from "./api";
import { toast } from "sonner";
import { validLayout } from "./layout";
import { nativeNotify } from "./native";
import { emitSessionChange } from "./plugins";
import { applyEvents, applyRemoteLayout, get, mergeConfig, onLayoutChange, removeSession, set, setLayout, upsertSession, watchedSessions } from "./store";
import type { HubEvent, SessionSummary } from "./types";

let ws: WebSocket | null = null;
let retry = 0;
let watched = "";

export async function boot() {
  try {
    const h = await api.hello();
    const sessions: Record<string, SessionSummary> = {};
    for (const s of h.sessions) sessions[s.id] = s;
    set({
      ready: true,
      authError: false,
      version: h.version,
      home: h.home,
      pluginsDir: h.pluginsDir,
      agents: h.agents,
      sessions,
      config: mergeConfig(h.config),
    });
    if (validLayout(h.layout)) set({ layout: h.layout });
    else setLayout((l) => l); // 第一次：把默认布局存下去
    pruneLayout();
    connect();
    startHeartbeat();
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      set({ authError: true, ready: true });
      return;
    }
    setTimeout(boot, 2000);
  }
}

/** 布局里引用了已经不存在的会话（被删了、换了数据目录）：那个 pane 退回「新建会话」。 */
function pruneLayout() {
  const { sessions } = get();
  setLayout((l) => {
    let changed = false;
    const fix = (n: any): any => {
      if (n.type === "pane") {
        if (n.sessionId && !sessions[n.sessionId]) {
          changed = true;
          return { ...n, sessionId: undefined };
        }
        return n;
      }
      const a = fix(n.a);
      const b = fix(n.b);
      return a === n.a && b === n.b ? n : { ...n, a, b };
    };
    const next = { ...l, workspaces: l.workspaces.map((w) => ({ ...w, tabs: w.tabs.map((t) => ({ ...t, root: fix(t.root) })) })) };
    return changed ? next : l;
  });
}

let lastHeard = 0;
let heartbeat: ReturnType<typeof setInterval> | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

function connect() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const sock = new WebSocket(`${proto}//${location.host}/ws`);
  ws = sock;
  sock.onopen = () => {
    retry = 0;
    lastHeard = Date.now();
    set({ connected: true });
    watched = "";
    syncWatch();
    // 断开期间错过的会话变化：重新拉一遍（订阅的日志服务端会整份重推）。
    refreshHello();
  };
  sock.onmessage = (ev) => {
    lastHeard = Date.now();
    let batch: any[];
    try {
      batch = JSON.parse(ev.data);
    } catch {
      return;
    }
    if (!Array.isArray(batch)) batch = [batch];
    for (const m of batch) handle(m);
  };
  sock.onclose = () => {
    if (ws !== sock) return;
    set({ connected: false });
    ws = null;
    scheduleReconnect();
  };
}

function scheduleReconnect(now = false) {
  if (reconnectTimer || ws) return;
  const delay = now ? 0 : Math.min(8000, 300 * 2 ** retry++);
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    // 重连前确认凭据还在（401 就别再连了）。
    api
      .hello()
      .then(() => connect())
      .catch((e) => {
        if (e instanceof ApiError && e.status === 401) set({ authError: true });
        else scheduleReconnect();
      });
  }, delay);
}

/** 把一条「看着连着、其实早断了」的连接收掉（睡眠唤醒、换网络之后常见），走重连。 */
function dropStale() {
  const sock = ws;
  if (!sock) return;
  ws = null;
  set({ connected: false });
  try {
    sock.close();
  } catch {}
  scheduleReconnect(true);
}

// 心跳：20 秒问一次，45 秒没有任何回音就当断了。页面不可见时浏览器会压定时器，回到前台立刻补一次。
function startHeartbeat() {
  if (heartbeat) return;
  heartbeat = setInterval(() => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    if (Date.now() - lastHeard > 45000) return dropStale();
    try {
      ws.send(JSON.stringify({ t: "ping" }));
    } catch {
      dropStale();
    }
  }, 20000);
  const wake = () => {
    if (document.visibilityState === "hidden") return;
    if (!ws) return scheduleReconnect(true);
    if (ws.readyState !== WebSocket.OPEN) return;
    // 刚醒来：问一下，3 秒没回音就重连。
    const asked = Date.now();
    try {
      ws.send(JSON.stringify({ t: "ping" }));
    } catch {
      return dropStale();
    }
    setTimeout(() => {
      if (ws && lastHeard < asked) dropStale();
    }, 3000);
  };
  document.addEventListener("visibilitychange", wake);
  window.addEventListener("online", wake);
  window.addEventListener("focus", wake);
}

async function refreshHello() {
  try {
    const h = await api.hello();
    const sessions: Record<string, SessionSummary> = {};
    for (const s of h.sessions) sessions[s.id] = s;
    set({ sessions, agents: h.agents });
    if (validLayout(h.layout)) applyRemoteLayout(h.layout);
  } catch {}
}

function handle(m: any) {
  switch (m.t) {
    case "session": {
      const prev = get().sessions[m.id];
      upsertSession(m.session);
      emitSessionChange(prev, m.session);
      break;
    }
    case "session_removed":
      removeSession(m.id);
      break;
    case "events":
      applyEvents(m.id, (m.events ?? []) as HubEvent[], !!m.reset);
      break;
    case "layout":
      if (m.data?.client !== clientId) applyRemoteLayout(m.data?.data);
      break;
    case "config":
      if (m.data?.client !== clientId) set({ config: mergeConfig(m.data?.data) });
      break;
    case "agents":
      if (Array.isArray(m.data)) set({ agents: m.data });
      break;
    case "pong":
      break;
    case "open":
      import("./actions").then((a) => a.openSessionNear(m.data?.id, m.data?.near, !!m.data?.focus));
      break;
    case "close":
      import("./actions").then((a) => a.closeSessionPanes(m.data?.id));
      break;
    case "plugins":
      import("./plugins").then((p) => p.reloadPlugins());
      break;
    case "stepup":
      // 远程设备请求加 passkey，要坐在电脑前的人批准（RemoteSettings）。只在本机这边提醒：远程那头批不了
      if (!location.protocol.startsWith("https")) {
        const body = `「${m.data?.label}」请求添加 passkey，核对码 ${m.data?.code}`;
        const open = () => set({ dialog: { kind: "settings", tab: "remote" } });
        nativeNotify("gerdr：要你批准", body);
        toast(body, { duration: 30000, action: { label: "去批准", onClick: open } });
      }
      break;
  }
}

/** 订阅屏幕上看得见的会话。 */
export function syncWatch() {
  const ids = watchedSessions(get().layout, get().keptTabs).sort();
  const key = ids.join(",");
  if (key === watched || !ws || ws.readyState !== WebSocket.OPEN) return;
  watched = key;
  ws.send(JSON.stringify({ t: "watch", ids }));
}

onLayoutChange(() => syncWatch());
