// 和服务端保持同步：开机拉一次 /api/hello，之后靠 WebSocket 推。断了自己重连（重连后
// 重新拉 hello、重新订阅）。订阅时带上手里每个会话日志的最后序号，服务端接得上就只补后面的，
// 接不上（重启过、日志被改写过）整份重推 —— 对账在服务端（hub.eventsAfter），这边照单全收。
import { api, ApiError, clientId } from "./api";
import { toast } from "sonner";
import { validLayout, withView } from "./layout";
import { nativeNotify } from "./native";
import { emitSessionChange } from "./plugins";
import {
  applyEvents,
  applyRemoteLayout,
  get,
  loadView,
  mergeConfig,
  onLayoutChange,
  removeSession,
  set,
  setLayout,
  transcripts,
  upsertSession,
  watchedSessions,
} from "./store";
import type { HubEvent, SessionSummary } from "./types";

// The session this window was opened on (?space=, the App's "open in new window"), read before anything renders: App.tsx
// rewrites the parameter to the session being shown, and it renders the not-yet-loaded default layout first
const wantSpace = new URLSearchParams(location.search).get("space");

let ws: WebSocket | null = null;
let retry = 0;
let watched = "";
let stopped = false;

// 热更新改到非组件模块时，运行时按新 URL 把整棵模块树重新 import、重新挂 App（uisite/runtime.go），
// 于是这个文件会有一份新实例再 boot 一次。旧实例的 WebSocket 没人关、心跳还在 ping，服务端的读超时永远不到，
// 「正在连接」里一台手机列好几条（踩过：手机在后台攒了几次热更新，回前台一下多出 4 条），每条还都在收全量推送、挡着睡眠。
// 所以新实例 boot 时先把上一份收掉；句柄挂在 window 上，因为新旧两份模块的变量互相看不见。
function stop() {
  stopped = true;
  if (reconnectTimer) clearTimeout(reconnectTimer);
  reconnectTimer = null;
  if (heartbeat) clearInterval(heartbeat);
  heartbeat = null;
  document.removeEventListener("visibilitychange", wake);
  window.removeEventListener("online", wake);
  window.removeEventListener("focus", wake);
  const sock = ws;
  ws = null; // 先置空：onclose 里认出不是当前这条，不会再去重连
  sock?.close();
}

// App remounts (hot update with changed hooks, or a changed module that this one doesn't import) call boot() again
// on the *same* instance of this module. Starting over would open a second WebSocket and orphan the first: both stay
// live, every broadcast is handled twice (the stepup toast showed up once per remount). So boot once per instance.
let booting: Promise<void> | null = null;
export function boot() {
  return (booting ??= start());
}

async function start() {
  if (stopped) return;
  const w = window as any;
  if (w.__gerdrSyncStop !== stop) w.__gerdrSyncStop?.();
  w.__gerdrSyncStop = stop;
  try {
    const h = await api.hello();
    if (stopped) return; // 等 hello 的时候被新实例收掉了
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
      remoteConns: h.remoteConns ?? [],
    });
    // 打开页面：服务端那份布局 + 这个端上次自己看的位置（不然一刷新就跳到别的端最后看的地方）
    if (validLayout(h.layout)) set({ layout: withView(h.layout, loadView()) });
    else setLayout((l) => l); // 第一次：把默认布局存下去
    pruneLayout();
    if (wantSpace) import("./actions").then((a) => a.switchSpace(wantSpace));
    connect();
    startHeartbeat();
    api.doctor().then(
      (doctor) => set({ doctor }),
      () => {},
    );
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      set({ authError: true, ready: true });
      return;
    }
    setTimeout(start, 2000);
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
  if (stopped) return;
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  set({ conn: { state: "connecting" } });
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const sock = new WebSocket(`${proto}//${location.host}/ws`);
  ws = sock;
  sock.onopen = () => {
    retry = 0;
    lastHeard = Date.now();
    set({ connected: true });
    watched = "";
    syncWatch();
    // 断开期间错过的会话变化：重新拉一遍（订阅的日志服务端会补上）。
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
  if (stopped || reconnectTimer || ws) return;
  const delay = now ? 0 : Math.min(8000, 300 * 2 ** retry++);
  set({ conn: delay ? { state: "waiting", at: Date.now() + delay } : { state: "connecting" } });
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    set({ conn: { state: "connecting" } });
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

/** Reconnect right away instead of waiting out the backoff (the "retry now" button next to the connection status). */
export function reconnectNow() {
  if (stopped || ws) return;
  if (reconnectTimer) clearTimeout(reconnectTimer);
  reconnectTimer = null;
  scheduleReconnect(true);
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
  document.addEventListener("visibilitychange", wake);
  window.addEventListener("online", wake);
  window.addEventListener("focus", wake);
}

function wake() {
  if (document.visibilityState === "hidden") return;
  if (!ws) return scheduleReconnect(true);
  if (ws.readyState !== WebSocket.OPEN) return;
  // 刚醒来：问一下，8 秒没回音就重连。
  // 只收问的那一条：focus / visibilitychange / online 常常一起来，3 秒的版本在远程慢网上会把刚连上的新连接也当死的收掉，
  // 一直重连（服务端「正在连接」里一台手机列好几条）。8 秒是给远程留的：pong 会排在正在写的一大帧日志后面。
  const sock = ws;
  const asked = Date.now();
  try {
    sock.send(JSON.stringify({ t: "ping" }));
  } catch {
    return dropStale();
  }
  setTimeout(() => {
    if (ws === sock && lastHeard < asked) dropStale();
  }, 8000);
}

async function refreshHello() {
  try {
    const h = await api.hello();
    const sessions: Record<string, SessionSummary> = {};
    for (const s of h.sessions) sessions[s.id] = s;
    set({ sessions, agents: h.agents, remoteConns: h.remoteConns ?? [] });
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
      applyEvents(m.id, (m.events ?? []) as HubEvent[], !!m.reset, !!m.older);
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
    case "remote_conns":
      if (Array.isArray(m.data)) set({ remoteConns: m.data });
      break;
    case "doctor":
      if (m.data?.checks) set({ doctor: m.data });
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
        toast(body, { id: "stepup", duration: 30000, action: { label: "去批准", onClick: open } }); // a newer request replaces the old one
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
  // 手里已有的日志报个序号：重连、切回看过的面板时不用整份重推（远程连着时一份几十 MB）
  const since: Record<string, number> = {};
  for (const id of ids) {
    const n = transcripts.get(id)?.lastSeq;
    if (n) since[id] = n;
  }
  ws.send(JSON.stringify({ t: "watch", ids, since }));
}

onLayoutChange(() => syncWatch());
