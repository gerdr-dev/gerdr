// 和服务端保持同步：开机拉一次 /api/hello，之后靠 WebSocket 推。断了自己重连（重连后
// 重新拉 hello、重新订阅）。订阅时带上手里每个会话日志的最后序号，服务端接得上就只补后面的，
// 接不上（重启过、日志被改写过）整份重推 —— 对账在服务端（hub.eventsAfter），这边照单全收。
import { api, ApiError, clientId, onAuthLost } from "./api";
import { toast } from "sonner";
import { currentSpace, validLayout, withView } from "./layout";
import { isNative, nativeNotify, runRelayedUpdateOp } from "./native";
import { emitSessionChange } from "./addons";
import { setPortDomain } from "./portlink";
import {
  applyEvents,
  applyRemoteLayout,
  enterSpaceMode,
  get,
  gotUnread,
  loadView,
  mergeConfig,
  onLayoutChange,
  removeSession,
  restartingNow,
  set,
  setConnProbe,
  setLayout,
  transcripts,
  unreadSync,
  upsertSession,
  watchedSessions,
} from "./store";
import type { DraftImg, SharedDraft } from "./store";
import type { HostInfo, HubEvent, SessionSummary } from "./types";
import { t } from "./i18n";
import { panelDataChanged, panelDataResync } from "@/core/panelData";

// The project this window was opened on (the address /<project id>; ?space= from older App shells), read before anything
// renders: App.tsx rewrites the address to the project being shown, and it renders the not-yet-loaded default layout first
const wantSpace = decodeURIComponent(location.pathname.split("/")[1] ?? "") || new URLSearchParams(location.search).get("space");

let ws: WebSocket | null = null;
// A second connection opening to take over from ws (refresh): ws keeps serving until this one is open
let pending: WebSocket | null = null;
let retry = 0;
let watched = "";
const draftOutbox = new Map<string, SharedDraft>(); // session id → draft waiting for the throttle (or for a connection)
const draftsInFlight = new Map<string, number>(); // session id → writes sent, echo not back yet
const draftsUnconfirmed = new Map<string, SharedDraft>(); // session id → the last of those writes, resent on reconnect
let draftTimer: ReturnType<typeof setTimeout> | null = null;
let stopped = false;

// A request mid-session found the credential gone / due for re-verification: same as the reconnect check below
// (App.tsx reloads into the pair page on the remote path)
onAuthLost(() => set({ authError: true }));

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
  pending?.close();
  pending = null;
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
  import("./mounts").then((m) => m.watchMountCards()); // the Work UIs' cards, for conversations anywhere
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
      installedAt: h.installedAt ?? 0,
      home: h.home,
      addonsDir: h.addonsDir,
      agents: h.agents,
      sessions,
      config: mergeConfig(h.config),
      remoteConns: h.remoteConns ?? [],
      hosts: h.hosts ?? [],
    });
    unreadSync.server = !!h.unread;
    gotUnread(h.unread);
    setPortDomain(h.portDomain);
    // 打开页面：服务端那份布局 + 这个端上次自己看的位置（不然一刷新就跳到别的端最后看的地方）
    // …and the mode that project was left in on this device, not the last mode of any window (they share localStorage)
    if (validLayout(h.layout)) {
      const l = withView(h.layout, loadView());
      set({ layout: l, ...enterSpaceMode(currentSpace(l)) });
    } else setLayout((l) => l); // 第一次：把默认布局存下去
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
        // A host session missing from the list usually means the host is off right now (server/hosts.go): keep it
        if (n.sessionId && !sessions[n.sessionId] && !n.sessionId.includes(":")) {
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
// When the oldest ping still unanswered went out (0 = none), and when the heartbeat last ran: see startHeartbeat
let pingAt = 0;
let lastTick = 0;
let heartbeat: ReturnType<typeof setInterval> | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

/**
 * Open the push connection. With `replace`, it opens beside the current one, which keeps serving until this one is
 * open and is closed then (refresh): the page never shows itself disconnected.
 */
function connect(replace = false) {
  if (stopped) return;
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  if (!replace) set({ conn: { state: "connecting" } });
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const sock = new WebSocket(`${proto}//${location.host}/ws`);
  if (replace) pending = sock;
  else ws = sock;
  sock.onopen = () => {
    if (replace) {
      if (pending !== sock) return sock.close();
      pending = null;
      const old = ws;
      ws = sock; // first: the old one's onclose sees it isn't current and stays quiet
      old?.close();
    }
    retry = 0;
    lastHeard = Date.now();
    pingAt = 0;
    const reconnect = get().everConnected;
    const restarted = get().restarting;
    set({ connected: true, everConnected: true, restarting: null });
    watched = "";
    // Echoes of drafts sent on the old connection never come back: stop waiting for them. A write sent there whose echo
    // never came may not have reached the server either (a phone's connection dies quietly and takes what was sent
    // with it), so it is sent again with what was typed while offline. After the watch, so the server's (older) draft
    // pushed on watch arrives while ours is in flight and gets ignored. Without the resend that older draft won:
    // voice input's words already in the composer vanished on a reconnect mid-dictation.
    for (const [id, d] of draftsUnconfirmed) if (!draftOutbox.has(id)) draftOutbox.set(id, d);
    draftsUnconfirmed.clear();
    draftsInFlight.clear();
    syncWatch();
    flushDrafts();
    // 断开期间错过的会话变化：重新拉一遍（订阅的日志服务端会补上）。
    // Back from an update: "updated to <version>", in every window and device (the App's windows also hear it from
    // the shell; noteUpdated says it once)
    refreshHello().then(() => {
      const v = get().version;
      if (restarted?.reason === "update") import("./native").then((n) => n.noteUpdated(v, restarted.version));
    });
    // Panel data changes missed meanwhile can't be replayed: every panel's page refreshes once (core usePanelChange)
    if (reconnect) panelDataResync();
    // Add-ons whose list failed to load while the network was down (lib/addons.ts)
    if (reconnect) import("./addons").then((p) => p.reloadIfFailed());
  };
  sock.onmessage = (ev) => {
    // A connection replaced by a refresh: what it still delivers, the new one's watch brings again
    if (ws !== sock) return;
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
    if (pending === sock) {
      // The refresh didn't get through: the old connection stays; if it died meanwhile, reconnect the usual way
      pending = null;
      if (!ws) scheduleReconnect(true);
      return;
    }
    if (ws !== sock) return;
    set({ connected: false, catchingUp: {} });
    ws = null;
    scheduleReconnect();
  };
}

// A reconnect's hello is in flight. Without this, wake() firing twice (focus, visibilitychange and online come
// together) in that window started a second connection and orphaned the first, still open
let dialing = false;

function scheduleReconnect(now = false) {
  if (stopped || reconnectTimer || ws || dialing || pending) return;
  // A restart we were told about takes a few seconds: try every half second instead of backing off, so the new
  // backend is picked up as soon as it answers
  const delay = now ? 0 : restartingNow(get()) ? 500 : Math.min(8000, 300 * 2 ** retry++);
  set({ connRetrying: true, conn: delay ? { state: "waiting", at: Date.now() + delay } : { state: "connecting" } });
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    set({ conn: { state: "connecting" } });
    // 重连前确认凭据还在（401 就别再连了）。
    dialing = true;
    api.hello().then(
      () => {
        dialing = false;
        connect();
      },
      (e) => {
        dialing = false;
        if (e instanceof ApiError && e.status === 401) set({ authError: true });
        else scheduleReconnect();
      },
    );
  }, delay);
}

/** Reconnect right away instead of waiting out the backoff (the "retry now" button next to the connection status). */
export function reconnectNow() {
  if (stopped || ws || dialing || pending) return;
  if (reconnectTimer) clearTimeout(reconnectTimer);
  reconnectTimer = null;
  scheduleReconnect(true);
}

/** 把一条「看着连着、其实早断了」的连接收掉（睡眠唤醒、换网络之后常见），走重连。 */
function dropStale() {
  const sock = ws;
  if (!sock) return;
  ws = null;
  set({ connected: false, catchingUp: {} });
  try {
    sock.close();
  } catch {}
  scheduleReconnect(true);
}

/**
 * Replace the push connection with a fresh one without dropping it first (connect(true)). A connection that sat
 * through a while in the background, or a network change, may be open on the phone and dead on the way (a NAT or the
 * relay forgot it, the Mac's relay tunnel came back): the page then shows the chat frozen while the computer goes on,
 * until the heartbeat notices. Probing it first cost up to 8s more of that. A new connection is one handshake; its
 * watch sends only what the page doesn't have (since), and the old one serves until it opens.
 */
function refresh() {
  if (stopped || !ws || pending || dialing) return;
  connect(true);
  const sock = pending as WebSocket | null; // set by connect(true)
  // Never opened (a handshake hanging on a dead route): give up on it, the old one is judged by the heartbeat
  setTimeout(() => {
    if (pending === sock && sock?.readyState !== WebSocket.OPEN) sock?.close();
  }, 10000);
}

// Heartbeat: a ping every 10s; one left unanswered for 15s while the page is in front means the connection is dead.
// Judged on a ping we sent, not on "nothing heard for 45s": a page in the background (the Android App, a hidden
// tab) has its timers slowed to once a minute or stopped altogether, so the old rule fired on the first tick after
// every long stay in the background and dropped a connection that was fine. A tick that comes late (timers were held
// up) judges nothing and starts over; coming back to the front, wake() refreshes instead. 15s and not less: on a slow
// remote line a pong queues behind a big log frame being written (the 3s probe once dropped good connections).
const BEAT = 10000;
function startHeartbeat() {
  if (heartbeat) return;
  lastTick = Date.now();
  heartbeat = setInterval(() => {
    const now = Date.now();
    const late = now - lastTick > 2 * BEAT;
    lastTick = now;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    if (late || lastHeard >= pingAt) pingAt = 0;
    if (pingAt && document.visibilityState === "visible" && now - pingAt > 15000) return dropStale();
    try {
      ws.send(JSON.stringify({ t: "ping" }));
      pingAt ||= now;
    } catch {
      dropStale();
    }
  }, BEAT);
  document.addEventListener("visibilitychange", wake);
  window.addEventListener("online", wake);
  window.addEventListener("focus", wake);
}

let hiddenAt = 0;

function wake(e: Event) {
  if (document.visibilityState === "hidden") {
    hiddenAt ||= Date.now();
    return;
  }
  const away = hiddenAt ? Date.now() - hiddenAt : 0;
  hiddenAt = 0;
  // No connection: try now, also when a backoff wait is pending (it grew while the page sat in the background)
  if (!ws) return reconnectNow();
  // Back after a while in the background, or on another network: don't trust the connection, replace it (refresh).
  // Unless it has been delivering all along (Labs "stay connected in the background" in the App): then just ask
  if ((away > 20000 && Date.now() - lastHeard > 20000) || e.type === "online") return refresh();
  // A short look away / a focus: just ask, 8s without an answer reconnects.
  // 只收问的那一条：focus / visibilitychange / online 常常一起来，3 秒的版本在远程慢网上会把刚连上的新连接也当死的收掉，
  // 一直重连（服务端「正在连接」里一台手机列好几条）。8 秒是给远程留的：pong 会排在正在写的一大帧日志后面。
  void probe();
}

/**
 * Ping the push connection; resolves true once anything comes back, false (and the connection dropped for a
 * reconnect) when nothing does within 8s. False too while there is no open connection: one is already on its way.
 */
function probe(): Promise<boolean> {
  const sock = ws;
  if (!sock || sock.readyState !== WebSocket.OPEN) return Promise.resolve(false);
  const asked = Date.now();
  try {
    sock.send(JSON.stringify({ t: "ping" }));
  } catch {
    dropStale();
    return Promise.resolve(false);
  }
  return new Promise((resolve) => {
    const poll = setInterval(() => {
      if (ws !== sock) {
        clearInterval(poll);
        resolve(false);
      } else if (lastHeard >= asked) {
        clearInterval(poll);
        resolve(true);
      } else if (Date.now() - asked > 8000) {
        clearInterval(poll);
        dropStale();
        resolve(false);
      }
    }, 200);
  });
}
setConnProbe(probe);

async function refreshHello() {
  try {
    const h = await api.hello();
    const sessions: Record<string, SessionSummary> = {};
    for (const s of h.sessions) sessions[s.id] = s;
    // version / installedAt too: a reconnect is usually the backend restarting onto a newly installed binary
    set({ sessions, agents: h.agents, remoteConns: h.remoteConns ?? [], hosts: h.hosts ?? [], version: h.version, installedAt: h.installedAt ?? 0 });
    // Changed on another device meanwhile (config holds who is in 进行中 too); the backend may be a new one now
    set({ config: mergeConfig(h.config) });
    unreadSync.server = !!h.unread;
    gotUnread(h.unread);
    setPortDomain(h.portDomain);
    if (validLayout(h.layout)) applyRemoteLayout(h.layout);
  } catch {}
}

/**
 * The computers this one drives changed (Settings → Computers). The backend links each /ws to every host when it
 * opens (server/hosts.go), so a computer added or removed since needs a new connection; a host going on / offline doesn't.
 */
function gotHosts(hosts: HostInfo[]) {
  const ids = (l: HostInfo[]) =>
    l
      .map((h) => h.id)
      .sort()
      .join(",");
  const before = ids(get().hosts);
  set({ hosts });
  if (ids(hosts) !== before && ws) {
    ws.close();
    // Its sessions come in on the new connection; a removed one's sessions go
    if (ids(hosts).length < before.length) refreshHello();
  }
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
      if (get().catchingUp[m.id]) set((s) => ({ catchingUp: omit(s.catchingUp, m.id) }));
      applyEvents(m.id, (m.events ?? []) as HubEvent[], !!m.reset, !!m.older);
      break;
    case "draft":
      gotDraft(m.id, m.data ?? {});
      break;
    case "layout":
      if (m.data?.client !== clientId) applyRemoteLayout(m.data?.data);
      break;
    case "config":
      if (m.data?.client !== clientId) set({ config: mergeConfig(m.data?.data) });
      break;
    case "unread":
      // Our own changes too: the backend sends the whole set in the order it applied the changes, so the last frame is right
      gotUnread(m.data?.data);
      break;
    case "agents":
      if (Array.isArray(m.data)) set({ agents: m.data });
      break;
    case "remote_conns":
      if (Array.isArray(m.data)) set({ remoteConns: m.data });
      break;
    case "hosts":
      if (Array.isArray(m.data)) gotHosts(m.data);
      break;
    case "doctor":
      if (m.data?.checks) set({ doctor: m.data });
      break;
    case "pong":
      break;
    // The desktop App's update status relayed by its page (server/appupdate.go): the App's own pages have it first-hand
    case "app_update":
      if (!isNative()) set({ appUpdate: m.data?.status ?? null });
      break;
    case "app_update_op":
      if (isNative() && m.data?.client === clientId) runRelayedUpdateOp(m.data.op, !!m.data.on, !!m.data.force);
      break;
    case "open":
      import("./actions").then((a) => a.openSessionNear(m.data?.id, m.data?.near, !!m.data?.focus));
      break;
    // Browser panes (actions/browser.ts): the pages open now, and an agent opening one
    case "browser_tabs":
      import("./actions").then((a) => a.setBrowserTabs(Array.isArray(m.data) ? m.data : []));
      break;
    case "browser_open":
      if (m.data?.tab) import("./actions").then((a) => a.browserOpened(m.data));
      break;
    case "browser_close":
      if (m.data?.tab) import("./actions").then((a) => a.browserClosed(m.data));
      break;
    case "close":
      import("./actions").then((a) => a.closeSessionPanes(m.data?.id));
      break;
    case "addons":
      import("./addons").then((p) => p.reloadAddons());
      break;
    // A panel's function changed its files: that panel's page refreshes (core usePanelChange)
    case "panel_data":
      panelDataChanged(m.data?.panel, m.data?.path);
      break;
    case "turn":
      import("./addons").then((p) => p.emit("turn", m.data));
      break;
    case "event":
      import("./addons").then((p) => p.emit("event", m.data));
      break;
    case "waits":
      import("./waits").then((p) => p.refreshWaits());
      break;
    case "badges":
      import("./badges").then((p) => p.setBadges(m.data));
      break;
    // The backend is about to restart on purpose (server/serviceapi.go announceRestart)
    case "restarting":
      set({ restarting: { reason: m.data?.reason === "update" ? "update" : "restart", version: m.data?.version ?? "", at: Date.now() } });
      break;
    case "inbox":
      import("./inbox").then((p) => p.syncPanelInbox(m.data ?? []));
      break;
    // Inbox cards clicked or closed in some window / device: the same cards go here too (lib/inbox.ts)
    case "cards_closed":
      import("./inbox").then((p) => p.closeCards(m.data ?? []));
      break;
    case "ext":
      set((s) => ({ extVersion: s.extVersion + 1 }));
      break;
    case "addon_stores":
      set((s) => ({ addonStoresVersion: s.addonStoresVersion + 1 }));
      break;
    case "addon_service":
      import("./addons").then((p) => p.serviceStatusChanged(m.data?.id, m.data?.service));
      break;
    case "addon_message":
      import("./addons").then((p) => p.serviceMessage(m.data?.id, m.data?.data));
      break;
    case "stepup":
      // 远程设备请求加 passkey，要坐在电脑前的人批准（RemoteSettings）。只在本机这边提醒：远程那头批不了
      if (!location.protocol.startsWith("https")) {
        const body = t("sync.passkeyRequest", { label: m.data?.label, code: m.data?.code });
        const open = () => set({ dialog: { kind: "settings", tab: "remote" } });
        nativeNotify(t("sync.approvalNeeded"), body);
        toast(body, { id: "stepup", duration: 30000, action: { label: t("sync.goApprove"), onClick: open } }); // a newer request replaces the old one
      }
      break;
  }
}

/** 订阅屏幕上看得见的会话。 */
export function syncWatch() {
  const ids = watchedSessions(get().layout, get().keptTabs).sort();
  const key = ids.join(",");
  if (key === watched || !ws || ws.readyState !== WebSocket.OPEN) return;
  const before = new Set(watched ? watched.split(",") : []);
  watched = key;
  // Sessions no longer watched won't echo our drafts back (hub.SetDraft only tells watchers)
  for (const id of draftsInFlight.keys()) if (!ids.includes(id)) (draftsInFlight.delete(id), draftsUnconfirmed.delete(id));
  // 手里已有的日志报个序号：重连、切回看过的面板时不用整份重推（远程连着时一份几十 MB）
  const since: Record<string, number> = {};
  for (const id of ids) {
    const n = transcripts.get(id)?.lastSeq;
    if (n) since[id] = n;
  }
  // The server answers every newly watched session with one "events" frame, even an empty one (hub.Watch). Until it
  // comes, the cached transcript of one we stopped watching may be behind: drawing it and then the catch-up flashes.
  // Same with nothing cached yet: until it comes the chat shows its empty "loading" state. Only when the session list
  // says the log has more than we hold, though: waiting out the round trip for a finished agent with nothing new just
  // made switching feel slow (reported).
  const catching: Record<string, true> = {};
  const sessions = get().sessions;
  for (const id of ids) {
    const behind = (since[id] ?? 0) < (sessions[id]?.lastSeq ?? 0);
    if (behind && (!before.has(id) || get().catchingUp[id])) catching[id] = true;
  }
  set({ catchingUp: catching });
  ws.send(JSON.stringify({ t: "watch", ids, since }));
}

function omit<T>(r: Record<string, T>, k: string): Record<string, T> {
  const out = { ...r };
  delete out[k];
  return out;
}

onLayoutChange(() => syncWatch());

/** Inbox cards the user clicked or closed here: every window and device drops the same ones ("cards_closed"). */
export function sendCardsClosed(ids: string[]) {
  if (ids.length && ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ t: "cards", ids }));
}

// Composer text mirrored across devices (hub/draft.go). Last writer wins on the whole text; what keeps devices from
// drifting apart is the server's order: every write, ours included, comes back in the order the server took them. So
// while a write of ours is on its way (unsent, or sent and not echoed yet) anything older from elsewhere is dropped:
// ours lands after it on the server and on every other device. Once nothing of ours is in flight, the next foreign
// write is the latest and is taken.

/** Two drafts say the same thing (text, selection and the same attachments in the same order). */
export function sameDraft(a: SharedDraft | undefined, b: SharedDraft | undefined) {
  if (!a || !b) return a === b;
  if (a.text !== b.text || a.sel?.[0] !== b.sel?.[0] || a.sel?.[1] !== b.sel?.[1] || a.imgs.length !== b.imgs.length) return false;
  return a.imgs.every((x, i) => x.blob === b.imgs[i].blob && x.path === b.imgs[i].path);
}

/** The composer changed here (text, caret, attachments): share it with the other devices (throttled; the latest wins). */
export function pushDraft(id: string, d: SharedDraft) {
  if (!sameDraft(get().sharedDrafts[id], d)) set((s) => ({ sharedDrafts: { ...s.sharedDrafts, [id]: d } }));
  draftOutbox.set(id, d);
  draftTimer ??= setTimeout(flushDrafts, 120);
}

function flushDrafts() {
  if (draftTimer) clearTimeout(draftTimer);
  draftTimer = null;
  if (!ws || ws.readyState !== WebSocket.OPEN) return; // kept for the reconnect (onopen)
  const watching = new Set(watched.split(","));
  for (const [id, d] of draftOutbox) {
    ws.send(JSON.stringify({ t: "draft", id, draft: { ...d, from: clientId } }));
    // The echo only comes back to tabs watching the session; don't wait for one that won't
    if (watching.has(id)) {
      draftsInFlight.set(id, (draftsInFlight.get(id) ?? 0) + 1);
      draftsUnconfirmed.set(id, d);
    }
  }
  draftOutbox.clear();
}

function gotDraft(id: string, d: { text?: string; sel?: [number, number]; imgs?: DraftImg[]; from?: string; init?: boolean }) {
  const n = draftsInFlight.get(id) ?? 0;
  // Our own echo. The one pushed on watch (init) is not an echo even if we wrote it: it was sent once, not per write.
  if (d.from === clientId && !d.init) {
    if (n > 1) draftsInFlight.set(id, n - 1);
    else (draftsInFlight.delete(id), draftsUnconfirmed.delete(id));
    return;
  }
  if (n > 0 || draftOutbox.has(id)) return; // older than what we're sending: ours will win everywhere
  const next: SharedDraft = { text: d.text ?? "", sel: d.sel, imgs: Array.isArray(d.imgs) ? d.imgs : [] };
  if (!sameDraft(get().sharedDrafts[id], next)) set((s) => ({ sharedDrafts: { ...s.sharedDrafts, [id]: next } }));
}
