// An add-on's g.socket / g.fetch made by the Android App itself, straight from the phone to the service
// (Bridge.kt NativeNet), instead of through the computer's backend (addonsocket.go / addonfetch.go). Remotely the
// backend route is phone → relay → computer → service and back, which made streaming speech-to-text lag behind
// the voice; the page can't connect by itself (CSP `connect-src 'self'`, a browser WebSocket can't set the
// headers these APIs authenticate with, and their HTTP APIs send no CORS headers). The add-on's code already runs
// here and already holds its keys (settings reach the page), so nothing new leaves the phone.
//
// Only when the App's hello says `net` (an older App doesn't know these messages); everywhere else the backend
// route stays. Bytes cross the bridge as base64 inside the JSON string, like copyImage.
//
// Some services only the computer can reach: its localhost, the LAN, a company host behind its VPN. Local and
// private addresses go through the backend from the start (useLocal); for the rest, when the phone can't reach the
// host at all (no DNS answer, refused, connect timeout, before any byte of the request went out: the shell's
// `unreachable`), the same request goes through the backend instead. Anything the service answered (an error
// status, a refused handshake) is the answer, never retried.

import { androidCan, isAndroid, onAndroid, postAndroid, type AndroidMsg } from "./android";
import { api } from "./api";

/** The App can make add-ons' connections itself. */
const appNet = () => isAndroid() && androidCan("net");

/** A host only the computer is likely to reach: loopback, private / link-local / tailnet IPs, mDNS, a bare name. */
function useLocal(url: string): boolean {
  let h: string;
  try {
    h = new URL(url).hostname.toLowerCase().replace(/^\[|\]$/g, "");
  } catch {
    return true; // the backend says what's wrong with it
  }
  if (h === "localhost" || h.endsWith(".localhost") || h.endsWith(".local") || h.endsWith(".internal") || h.endsWith(".lan")) return true;
  if (h.includes(":")) return h === "::1" || /^f[cd]/.test(h) || h.startsWith("fe80");
  const p = h.split(".").map(Number);
  if (p.length === 4 && p.every((n) => Number.isInteger(n))) {
    return (
      p[0] === 127 ||
      p[0] === 10 ||
      (p[0] === 172 && p[1] >= 16 && p[1] <= 31) ||
      (p[0] === 192 && p[1] === 168) ||
      (p[0] === 169 && p[1] === 254) ||
      (p[0] === 100 && p[1] >= 64 && p[1] <= 127)
    );
  }
  return !h.includes(".");
}

/** Make this add-on connection from the phone? */
const direct = (url: string) => appNet() && !useLocal(url);

const sockets = new Map<string, AppSocket>();
const fetches = new Map<string, { ok: (m: AndroidMsg) => void; no: (e: Error) => void }>();
let wired = false;

function wire() {
  if (wired) return;
  wired = true;
  // replay = false: a socket event or a fetch answer is for one id, replaying the last one would deliver it twice
  onAndroid("ws", (m) => sockets.get(String(m.id))?.onShell(m), false);
  onAndroid(
    "http",
    (m) => {
      const f = fetches.get(String(m.id));
      if (!f) return;
      fetches.delete(String(m.id));
      if (typeof m.error === "string") f.no(Object.assign(new Error(m.error), { unreachable: m.unreachable === true }));
      else f.ok(m);
    },
    false,
  );
}

function toB64(b: Uint8Array): string {
  let s = "";
  for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode(...b.subarray(i, i + 0x8000));
  return btoa(s);
}

const fromB64 = (s: string) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0));

/**
 * What an add-on gets from g.socket in the App: a WebSocket as far as add-ons use one (readyState, binaryType,
 * send, close, the on* handlers and addEventListener, the same events and order), its other end a WebSocket the
 * App opened with the add-on's headers.
 */
class AppSocket extends EventTarget {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  readonly CONNECTING = 0;
  readonly OPEN = 1;
  readonly CLOSING = 2;
  readonly CLOSED = 3;
  readyState = 0;
  binaryType: BinaryType = "blob";
  protocol = "";
  extensions = "";
  bufferedAmount = 0;
  onopen: ((e: Event) => void) | null = null;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onclose: ((e: CloseEvent) => void) | null = null;
  onerror: ((e: Event) => void) | null = null;

  private readonly id: string;
  readonly url: string;
  /** The same socket through the backend, for when the phone can't reach the host */
  private readonly fallback: () => Promise<WebSocket>;
  /** The backend's socket once fallen back to: everything goes through it from then on */
  private via: WebSocket | null = null;
  private fallingBack = false;

  constructor(id: string, url: string, fallback: () => Promise<WebSocket>) {
    super();
    this.id = id;
    this.url = url;
    this.fallback = fallback;
  }

  private fire(e: Event) {
    this.dispatchEvent(e);
    const h = (this as unknown as Record<string, ((e: Event) => void) | null>)["on" + e.type];
    h?.call(this, e);
  }

  onShell(m: AndroidMsg) {
    if (this.fallingBack) return; // the shell's close after `unreachable`: the backend's socket speaks now
    if (m.ev === "error" && m.unreachable === true && this.readyState === 0) return this.fallBack(String(m.msg ?? ""));
    switch (m.ev) {
      case "open":
        this.readyState = 1;
        this.protocol = String(m.protocol ?? "");
        this.fire(new Event("open"));
        break;
      case "message": {
        let data: string | ArrayBuffer | Blob;
        if (typeof m.b64 === "string") {
          const b = fromB64(m.b64);
          data = this.binaryType === "arraybuffer" ? b.buffer : new Blob([b]);
        } else data = String(m.text ?? "");
        this.fire(new MessageEvent("message", { data }));
        break;
      }
      case "error":
        // A browser WebSocket's error carries no detail either; the shell's reason goes to the console
        if (m.msg) console.warn("[g.socket]", this.url, m.msg);
        this.fire(new Event("error"));
        break;
      case "close":
        if (this.readyState === 3) return;
        this.readyState = 3;
        sockets.delete(this.id);
        this.fire(new CloseEvent("close", { code: Number(m.code ?? 1006), reason: String(m.reason ?? ""), wasClean: m.code === 1000 }));
        break;
    }
  }

  private fallBack(why: string) {
    this.fallingBack = true;
    sockets.delete(this.id);
    console.info("[g.socket] the phone can't reach", this.url, `(${why}): through the computer`);
    this.fallback().then(
      (ws) => {
        if (this.readyState >= 2) return ws.close(); // closed by the add-on while waiting
        this.via = ws;
        ws.binaryType = "arraybuffer";
        ws.onopen = () => {
          this.readyState = 1;
          this.protocol = ws.protocol;
          this.fire(new Event("open"));
        };
        ws.onmessage = (e) => {
          const d = e.data;
          this.fire(new MessageEvent("message", { data: typeof d !== "string" && this.binaryType === "blob" ? new Blob([d]) : d }));
        };
        ws.onerror = () => this.fire(new Event("error"));
        ws.onclose = (e) => {
          this.readyState = 3;
          this.fire(new CloseEvent("close", { code: e.code, reason: e.reason, wasClean: e.wasClean }));
        };
      },
      () => {
        this.readyState = 3;
        this.fire(new Event("error"));
        this.fire(new CloseEvent("close", { code: 1006 }));
      },
    );
  }

  send(data: string | ArrayBufferLike | ArrayBufferView | Blob) {
    if (this.readyState === 0) throw new DOMException("Still in CONNECTING state.", "InvalidStateError");
    if (this.readyState !== 1) return;
    if (this.via) return this.via.send(data);
    if (typeof data === "string") return postAndroid({ t: "ws", op: "send", id: this.id, text: data });
    if (data instanceof Blob) {
      // Blob reads are async; a later send could overtake it. Add-ons send ArrayBuffers / typed arrays.
      void data.arrayBuffer().then((b) => this.send(b));
      return;
    }
    const b = ArrayBuffer.isView(data) ? new Uint8Array(data.buffer, data.byteOffset, data.byteLength) : new Uint8Array(data);
    postAndroid({ t: "ws", op: "send", id: this.id, b64: toB64(b) });
  }

  close(code = 1000, reason = "") {
    if (this.readyState >= 2) return;
    this.readyState = 2;
    if (this.via) return this.via.close(code, reason);
    if (this.fallingBack) return void ((this.readyState = 3), this.fire(new CloseEvent("close", { code, reason, wasClean: true })));
    postAndroid({ t: "ws", op: "close", id: this.id, code, reason });
  }
}

/** The backend's socket (internal/server/addonsocket.go): a plain WebSocket to our own /api, joined to the service's. */
async function backendSocket(addon: string, url: string, init?: { headers?: Record<string, string>; protocols?: string[] }) {
  const { token } = await api.addonSocket(addon, { url, headers: init?.headers, protocols: init?.protocols });
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  return new WebSocket(`${proto}//${location.host}/api/addons/${encodeURIComponent(addon)}/socket/${token}`);
}

/** An add-on's g.socket: from the phone in the App (resolves at once, CONNECTING, like `new WebSocket()`), else the backend's. */
export async function addonSocket(addon: string, url: string, init?: { headers?: Record<string, string>; protocols?: string[] }): Promise<WebSocket> {
  if (!direct(url)) return backendSocket(addon, url, init);
  wire();
  const id = crypto.randomUUID();
  const s = new AppSocket(id, url, () => backendSocket(addon, url, init));
  sockets.set(id, s);
  postAndroid({ t: "ws", op: "open", id, url, headers: init?.headers ?? {}, protocols: init?.protocols ?? [] });
  return s as unknown as WebSocket;
}

export type AddonFetchReq = { url: string; method?: string; headers?: Record<string, string>; body?: string; bodyBase64?: boolean; binary?: boolean };
export type AddonFetchResp = { status: number; statusText: string; headers: Record<string, string>; body: string; base64?: boolean };

function appFetch(b: AddonFetchReq): Promise<AddonFetchResp> {
  wire();
  const id = crypto.randomUUID();
  return new Promise((ok, no) => {
    // The shell gives up at 30s like the backend; this only catches an answer that never comes (page reloaded under it)
    const timer = setTimeout(() => fetches.delete(id) && no(new Error("no answer from the App")), 60000);
    fetches.set(id, {
      ok: (m) => (clearTimeout(timer), ok(m as unknown as AddonFetchResp)),
      no: (e) => (clearTimeout(timer), no(e)),
    });
    postAndroid({ t: "http", id, ...b });
  });
}

/** An add-on's g.fetch request: from the phone in the App, else (or when the phone can't reach the host) the backend's /api/addons/{id}/fetch. */
export async function addonFetch(addon: string, b: AddonFetchReq): Promise<AddonFetchResp> {
  if (!direct(b.url)) return api.addonFetch(addon, b);
  try {
    return await appFetch(b);
  } catch (e) {
    if (!(e as { unreachable?: boolean }).unreachable) throw e;
    console.info("[g.fetch] the phone can't reach", b.url, `(${(e as Error).message}): through the computer`);
    return api.addonFetch(addon, b);
  }
}
