import { useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { feedOf, isFeed, subscribeFeed, type FeedFile } from "./feedChat";
import { ToolCard, cardOf } from "@/components/chat/ToolCard";
import { ChatProvider, type ChatHost, type ChatSession, type ComposerContext, type DraftImage, type DraftStore } from "@gerdr/chat";
import { toast } from "sonner";
import { api, type Holder } from "./api";
import { closeAgentInPane, focusSession, openPath, openSubagent, refocusAfterDialog, withdrawSteer } from "./actions";
import { defaultsOf, setDefault, setDefaults } from "./defaults";
import { hasNativeFilePath, isNative, nativeFilePath, nativeOpenPath } from "./native";
import {
  get,
  loadOlder,
  prefOf,
  replayLastTurn,
  sendShown,
  set,
  setConfig,
  subTranscript,
  transcript,
  useStore,
  type DraftImg,
  type SharedDraft,
} from "./store";
import { pushDraft, sameDraft } from "./sync";
import { AddonComposerActions, AddonLoader } from "@/components/AddonSlot";
import { useAddonTurnStat } from "./addons";
import { MessageActions } from "@/components/chat/MessageActions";
import { locale, t } from "./i18n";
import { UsagePanel } from "@/components/UsageMeter";
import { HeldDialog } from "@/components/chat/HeldDialog";
import { RunButton } from "@/components/chat/RunButton";
import { timedEscape } from "./escTiming";

/**
 * gerdr's side of the chat package (@gerdr/chat, ui/packages/chat): where its data comes from (the store, fed over
 * the WebSocket) and what its buttons do (lib/api, lib/actions), plus the composer features only gerdr has —
 * drafts shared across devices, /resume and /usage, "session open elsewhere", making options the default for new
 * sessions, `!command` runs, and Ctrl+C twice to quit an agent.
 */

const NO_IMGS: DraftImage[] = [];

// Drafts live in the store, per pane (they outlive the composer: tabs that aren't kept unmount it)
const drafts: DraftStore = {
  useText: (id) => useStore((s) => s.drafts[id] ?? ""),
  getText: (id) => get().drafts[id] ?? "",
  setText: (id, t) => set((s) => ({ drafts: { ...s.drafts, [id]: t } })),
  useImages: (id) => useStore((s) => s.draftImgs[id] ?? NO_IMGS),
  getImages: (id) => get().draftImgs[id] ?? NO_IMGS,
  setImages: (id, f) => set((s) => ({ draftImgs: { ...s.draftImgs, [id]: typeof f === "function" ? f(s.draftImgs[id] ?? NO_IMGS) : f } })),
};

// "Session open elsewhere" (HeldDialog): which session's composer is waiting on the choice, and how to resend after it
type Held = { sessionId: string; holders: Holder[]; retry: () => void };
let held: Held | null = null;
const heldSubs = new Set<() => void>();
const setHeld = (h: Held | null) => ((held = h), heldSubs.forEach((f) => f()));
function useHeld() {
  const [, bump] = useState(0);
  useEffect(() => {
    const f = () => bump((n) => n + 1);
    heldSubs.add(f);
    return () => void heldSubs.delete(f);
  }, []);
  return held;
}

// /usage's panel, per session
let usageFor: string | null = null;
const usageSubs = new Set<() => void>();
const setUsageFor = (id: string | null) => ((usageFor = id), usageSubs.forEach((f) => f()));
function useUsageFor() {
  const [, bump] = useState(0);
  useEffect(() => {
    const f = () => bump((n) => n + 1);
    usageSubs.add(f);
    return () => void usageSubs.delete(f);
  }, []);
  return usageFor;
}

function Overlay({ session }: { session: ChatSession }) {
  const h = useHeld();
  const usage = useUsageFor();
  return (
    <>
      {usage === session.id && <UsagePanel agent={session.base ?? session.agent} onClose={() => setUsageFor(null)} />}
      <HeldDialog
        sessionId={session.id}
        holders={h?.sessionId === session.id ? h.holders : null}
        onClose={() => setHeld(null)}
        onDone={() => {
          const retry = h?.retry;
          setHeld(null);
          if (retry) setTimeout(retry, 50);
        }}
      />
    </>
  );
}

// ---- drafts shared across devices (the same composer on the phone and the computer) ----

// What other devices get of the attachments: references only. Ones still uploading are left out until they're done.
const shareable = (xs: DraftImage[]): DraftImg[] =>
  xs.flatMap((im): DraftImg[] =>
    im.path ? [{ mimeType: im.mimeType, path: im.path, name: im.name }] : im.blob ? [{ mimeType: im.mimeType, blob: im.blob }] : [],
  );
const fromShared = (r: DraftImg): DraftImage =>
  r.path
    ? { data: "", mimeType: r.mimeType, url: fileURL(r.path), path: r.path, name: r.name }
    : { data: "", mimeType: r.mimeType, url: r.blob!, blob: r.blob };
const imgsKey = (xs: DraftImg[]) => xs.map((x) => x.blob ?? x.path).join("\n");
// Images being stored on the gerdr machine (by data URL), so a remount doesn't upload the same one twice
const savingBlobs = new Set<string>();

/**
 * Same composer on every device looking at this session: text, caret and attachments. Whatever ends up in this pane's
 * draft (typing, sending clears it, ⌘S stash…) is pushed out, and changes from elsewhere are copied in. On mount the
 * shared draft wins over this pane's local copy, which may be stale from before the tab was switched away. Caret moves
 * alone are pushed from onSelect.
 */
function useSharedDraft({ session, textarea: ta, text, images: imgs, setText, setImages: setImgs, get: latest }: ComposerContext) {
  const shared = useStore((s) => s.sharedDrafts[session.id]);
  const seen = useRef<{ id: string; d: SharedDraft | undefined } | null>(null);
  const applying = useRef(false); // putting a remote draft in: the caret jumps around meanwhile, don't echo that back
  const sharedImgs = shareable(imgs);
  const localDraft = (): SharedDraft => {
    const el = ta.current;
    const sel: [number, number] | undefined = el && document.activeElement === el ? [el.selectionStart, el.selectionEnd] : undefined;
    const d = latest();
    return { text: d.text, sel, imgs: shareable(d.images) };
  };
  const takeRemote = (d: SharedDraft) => {
    const el = ta.current;
    const cur = el?.value ?? text;
    const focused = !!el && document.activeElement === el;
    const old = el ? [el.selectionStart, el.selectionEnd] : null;
    applying.current = true;
    setText(d.text);
    // Keep the local copies (they have the bytes); images still uploading here stay at the end
    setImgs((xs) => {
      const have = new Map(xs.filter((x) => x.blob || x.path).map((x) => [x.blob ?? x.path, x]));
      return [...d.imgs.map((r) => have.get(r.blob ?? r.path) ?? fromShared(r)), ...xs.filter((x) => !x.blob && !x.path)];
    });
    let sel: number[] | null = null;
    // Not on an unfocused touch screen: there the tap that focuses the box puts the caret anyway, and moving the
    // selection of an unfocused field can pop the keyboard up on iOS
    const coarse = window.matchMedia("(pointer: coarse)").matches;
    if (d.sel && (focused || !coarse)) sel = d.sel.map((i) => Math.min(i, d.text.length));
    else if (focused && old) {
      // No caret from the other side: keep ours where it was relative to the text around it. The edit sits between
      // the unchanged head and tail, so a caret in the head stays put and one in the tail moves with it; one inside
      // the replaced part goes to the end of what replaced it.
      const next = d.text;
      let head = 0;
      while (head < cur.length && head < next.length && cur[head] === next[head]) head++;
      let tail = 0;
      while (tail < cur.length - head && tail < next.length - head && cur[cur.length - 1 - tail] === next[next.length - 1 - tail]) tail++;
      const map = (i: number) => (i <= head ? i : i >= cur.length - tail ? i + next.length - cur.length : next.length - tail);
      sel = old.map(map);
    }
    requestAnimationFrame(() => {
      if (sel && el) el.setSelectionRange(sel[0], sel[1]);
      // The select events from the line above fire after this frame
      setTimeout(() => (applying.current = false), 0);
    });
  };
  useEffect(() => {
    const prev = seen.current?.id === session.id ? seen.current.d : undefined;
    seen.current = { id: session.id, d: shared };
    const mine = localDraft();
    if (shared && shared !== prev) {
      // Something changed elsewhere (or this box just mounted): take it unless we already look exactly like that
      const el = ta.current;
      if (!sameDraft({ ...mine, sel: shared.sel && el ? [el.selectionStart, el.selectionEnd] : undefined }, shared)) takeRemote(shared);
      return;
    }
    // Only text / attachments decide here: the caret is pushed by onSelect, and right after takeRemote it is not where
    // it's going yet (that happens a frame later)
    if (shared ? shared.text !== mine.text || imgsKey(shared.imgs) !== imgsKey(mine.imgs) : mine.text || mine.imgs.length) pushDraft(session.id, mine);
  }, [text, imgsKey(sharedImgs), shared, session.id]);
  // Pasted images go to the gerdr machine too, so the draft can point other devices at them
  useEffect(() => {
    for (const im of imgs) {
      if (im.blob || im.path || !im.data || im.mimeType.startsWith("video/") || savingBlobs.has(im.url)) continue;
      savingBlobs.add(im.url);
      api
        .saveBlob(im.data)
        .then(({ url }) => setImgs((xs) => xs.map((x) => (x.url === im.url ? { ...x, blob: url } : x))))
        .catch(() => {}) // stays on this device only
        .finally(() => savingBlobs.delete(im.url));
    }
  }, [imgs]);
  return {
    onSelect: () => {
      if (applying.current) return;
      const mine = localDraft();
      if (!mine.sel || (!shared && !mine.text)) return;
      if (!sameDraft(mine, shared)) pushDraft(session.id, mine);
    },
  };
}

// Sessions whose every message goes in right away, interrupting what the agent is on (a panel's SessionChat with
// sendNow: a task board wants its workers to take a new message while something runs in the background, not hold it
// until the step ends). Counted per mount: two chats of one session may be on screen.
const nowMounts = new Map<string, number>();
export const sendsNow = (id: string) => (nowMounts.get(id) ?? 0) > 0;
export function holdSendNow(id: string) {
  nowMounts.set(id, (nowMounts.get(id) ?? 0) + 1);
  return () => {
    const n = (nowMounts.get(id) ?? 1) - 1;
    if (n > 0) nowMounts.set(id, n);
    else nowMounts.delete(id);
  };
}

const fileURL = (path: string, cwd?: string, host?: string) =>
  `${host ? `/h/${encodeURIComponent(host)}` : ""}/api/fs/raw?path=${encodeURIComponent(path)}&cwd=${encodeURIComponent(cwd ?? "")}`;

/**
 * A session on another computer (id "<host>:<id>", docs/dev/MULTIHOST.md): its paths are that computer's files, so the
 * chat's file functions go there (server/hosts.go proxies ?host= and /h/<host>/). A file picked here has no path there,
 * so attachments are uploaded to it instead (no localPath), and there is no "open with the system app" for it.
 */
function hostSession(sessionId: string): Partial<ChatHost> | undefined {
  if (isFeed(sessionId)) return feedHost;
  const i = sessionId.indexOf(":");
  if (i <= 0) return undefined;
  const host = sessionId.slice(0, i);
  return {
    fileURL: (p, cwd) => fileURL(p, cwd, host),
    fileStat: (p, cwd) => api.fileStat(p, cwd, host),
    fileExists: (names, cwd) => api.fileExists(names, cwd, host),
    openPath: (p, cwd, opts) => void openPath(p, cwd, { ...opts, host }),
    upload: (f) => api.upload(f, host),
    localPath: undefined,
    openNative: undefined,
  };
}

/**
 * A page's FeedChat (core/FeedChat.tsx): not a session, so everything that talks to one is off (stop, rewind, options,
 * `!` commands, slash commands, shared drafts, add-on slots) and the rest comes from the page (lib/feedChat.ts).
 */
const feedHost: Partial<ChatHost> = {
  useTranscript: (id) => {
    const c = useSyncExternalStore(
      (f) => subscribeFeed(id, f),
      () => feedOf(id),
    );
    return { all: c?.messages ?? [], running: false, loaded: !!c };
  },
  // Pasted images come as bytes: the page gets files by path, like everything else it sends on
  send: async (id, content) => {
    const c = feedOf(id);
    if (!c) throw new Error("gone");
    const files: FeedFile[] = [];
    let text = "";
    for (const b of content) {
      if (b.type === "text") text += b.text;
      else if (b.type === "resource_link") files.push({ path: b.uri.replace(/^file:\/\//, ""), name: b.name });
      else if (b.type === "image") {
        const bytes = Uint8Array.from(atob(b.data), (ch) => ch.charCodeAt(0));
        const ext = b.mimeType.split("/")[1] ?? "png";
        const { path } = await api.upload(new File([bytes], `image-${Date.now()}.${ext}`, { type: b.mimeType }));
        files.push({ path, mime: b.mimeType });
      }
    }
    await c.send({ text: text.trim(), files });
  },
  stop: async () => ({ content: [] }),
  withdrawLast: undefined,
  withdraw: undefined,
  rewind: undefined,
  warm: undefined,
  shell: undefined,
  setMode: undefined,
  setConfig: undefined,
  backgroundTool: undefined,
  stopTask: undefined,
  openSubagent: undefined,
  codeActions: undefined,
  composerOverlay: undefined,
  useTurnStat: undefined,
  messageActions: undefined,
  renderTool: (part, ctx) => feedOf(ctx.sessionId)?.render(part),
  searchFiles: async (id, q) => (feedOf(id)?.mentions ?? []).filter((n) => n.toLowerCase().includes(q.toLowerCase())),
  composerBar: (session, { paneId }) => <FeedHint id={session.id} paneId={paneId} />,
  placeholder: (session) => feedOf(session.id)?.placeholder,
  composer: { drafts },
};

function FeedHint({ id, paneId }: { id: string; paneId: string }) {
  const text = drafts.useText(paneId);
  const c = useSyncExternalStore(
    (f) => subscribeFeed(id, f),
    () => feedOf(id),
  );
  return <>{c?.hint?.(text)}</>;
}

// /usage only means something for claude / codex (hub.Usage)
const localCommands = (session: ChatSession) =>
  [
    {
      name: "resume",
      description: t("chatHost.resume"),
      run: (paneId: string) => set({ dialog: { kind: "resume", sessionId: session.id, paneId } }),
    },
    { name: "usage", description: t("chatHost.usage"), run: () => setUsageFor(session.id) },
    {
      name: "replay",
      description: t("chatHost.replay"),
      run: () => {
        if (session.status === "running") return void toast(t("chatHost.replayRunning"));
        if (!replayLastTurn(session.id)) toast(t("chatHost.replayNothing"));
      },
    },
  ].filter((c) => c.name !== "usage" || ["claude", "codex"].includes(session.base ?? session.agent));

function useTranscript(sessionId: string, sub?: string) {
  useStore((s) => s.tv[sessionId]); // re-render when the log changes
  const loaded = useStore((s) => s.tv[sessionId] !== undefined);
  const older = useStore((s) => s.older[sessionId]);
  const t = sub ? subTranscript(sessionId, sub) : transcript(sessionId);
  return { all: t.all, running: t.running, loaded, older, loadOlder: () => loadOlder(sessionId) };
}

export function GerdrChatProvider({ children }: { children: ReactNode }) {
  const config = useStore((s) => s.config);
  const device = useStore((s) => s.device);
  const agents = useStore((s) => s.agents);
  const host = useMemo<ChatHost>(
    () => ({
      useTranscript,
      agentName: (id) => agents.find((a) => a.id === id)?.name ?? id,
      useSessionTitle: (id) =>
        useStore((st) => {
          const s = st.sessions[id];
          return s ? s.title || s.cwd.split("/").pop() || s.agent : undefined;
        }),
      // A tool result carrying a card (Work tools: a step put on the user's board) is drawn as that card
      renderTool: (part, ctx) => {
        const card = cardOf(part);
        return card ? <ToolCard card={card} session={ctx.sessionId} /> : undefined;
      },
      send: (id, content, o) => sendShown(id, content, o?.onRetry, o?.now || sendsNow(id)).then(() => {}),
      stop: (id) => timedEscape(id),
      withdrawLast: (id) => api.withdrawLast(id),
      withdraw: (id, msg, now) => withdrawSteer(id, msg, !!now),
      rewind: (id, msg) => api.rewind(id, msg),
      answer: (id, req, resp) => api.answer(id, req, resp).then(() => {}),
      stopTask: (id, task) => api.stopTask(id, task).then(() => {}),
      backgroundTool: (id, tc) => api.backgroundTool(id, tc).then((r: any) => !!r?.backgrounded),
      allowRules: (id, rules) => api.allowRules(id, rules).then(() => {}),
      setMode: (id, mode) => api.setMode(id, mode).then(() => {}),
      setConfig: (id, cid, v) => api.setConfig(id, cid, v).then(() => {}),
      warm: (id) => api.warm(id).then(() => {}),
      shell: { run: (id, cmd) => api.shell(id, cmd), stop: (id, run) => api.stopShell(id, run) },
      fileURL,
      upload: (f) => api.upload(f),
      // Desktop App: attach files and folders by their path on this machine (the backend runs here too)
      localPath: hasNativeFilePath() ? nativeFilePath : undefined,
      fileStat: (p, cwd) => api.fileStat(p, cwd),
      fileExists: (names, cwd) => api.fileExists(names, cwd),
      searchFiles: (id, q) => api.searchFiles(id, q).then((r) => r.files),
      openPath: (p, cwd, opts) => void openPath(p, cwd, opts),
      forSession: hostSession,
      openNative: isNative() ? (p, cwd, reveal) => nativeOpenPath(p, cwd, reveal) : undefined,
      openSubagent,
      focusSession,
      refocus: refocusAfterDialog,
      codeActions: (code, lang, session) => (lang === "bang" ? <RunButton cmd={code} session={session} /> : null),
      composerOverlay: (session) => <Overlay session={session} />,
      useTurnStat: useAddonTurnStat,
      composerBar: (session, { paneId }) => <AddonComposerActions sessionId={session.id} paneId={paneId} />,
      renderLoader: (p) => <AddonLoader {...p} />,
      // Add-ons' buttons on a message (g.messageAction, lib/messageActions.ts)
      messageActions: (m) => <MessageActions info={m} />,
      composer: {
        drafts,
        useExtras: useSharedDraft,
        localCommands,
        onSendError: (session, e: any, retry) => {
          if (e?.code !== "held_elsewhere") return false;
          setHeld({ sessionId: session.id, holders: e.holders ?? [], retry });
          return true;
        },
        onExit: closeAgentInPane,
        useDefaults: (agent) => {
          // subscribe to what defaultsOf reads, so "already the default" shows right after setting it
          useStore((s) => s.config.defaultModes);
          useStore((s) => s.config.defaultConfig);
          useStore((s) => s.config.llm);
          return { values: defaultsOf(agent), set: (id, v) => setDefault(agent, id, v), setAll: (vs) => setDefaults(agent, vs) };
        },
      },
      settings: {
        sendKey: prefOf({ config, device }, "sendKey"),
        showThinking: config.showThinking,
        virtualList: prefOf({ config, device }, "virtualList"),
        expandEdits: config.expandEdits,
        confirmLinks: config.confirmLinks,
        pinnedCommands: config.pinnedCommands,
      },
      setPinnedCommands: (names) => setConfig({ pinnedCommands: names }),
    }),
    [config, device, agents],
  );
  // The chat has its own zh / en texts (packages/chat/src/messages.ts): same language as the rest of the interface
  return (
    <ChatProvider host={host} locale={locale()}>
      {children}
    </ChatProvider>
  );
}
