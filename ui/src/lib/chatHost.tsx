import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ChatProvider, type ChatHost, type ChatSession, type ComposerContext, type DraftImage, type DraftStore } from "@gerdr/chat";
import { api, type Holder } from "./api";
import { closeAgentInPane, focusSession, openPath, openSubagent, refocusAfterDialog, withdrawSteer } from "./actions";
import { defaultsOf, setDefault, setDefaults } from "./defaults";
import { isNative, nativeOpenPath } from "./native";
import { get, loadOlder, sendShown, set, setConfig, subTranscript, transcript, useStore, type DraftImg, type SharedDraft } from "./store";
import { pushDraft, sameDraft } from "./sync";
import { UsagePanel } from "@/components/UsageMeter";
import { HeldDialog } from "@/components/chat/HeldDialog";
import { RunButton } from "@/components/chat/RunButton";

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

const fileURL = (path: string, cwd?: string) => `/api/fs/raw?path=${encodeURIComponent(path)}&cwd=${encodeURIComponent(cwd ?? "")}`;

// /usage only means something for claude / codex (hub.Usage)
const localCommands = (session: ChatSession) =>
  [
    {
      name: "resume",
      description: "换到这个目录下之前的一段对话",
      run: (paneId: string) => set({ dialog: { kind: "resume", sessionId: session.id, paneId } }),
    },
    { name: "usage", description: "看账号的用量额度（不发给 agent）", run: () => setUsageFor(session.id) },
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
      send: (id, content, o) => sendShown(id, content, o?.onRetry, o?.now).then(() => {}),
      stop: (id) => api.escape(id),
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
      fileStat: (p, cwd) => api.fileStat(p, cwd),
      fileExists: (names, cwd) => api.fileExists(names, cwd),
      searchFiles: (id, q) => api.searchFiles(id, q).then((r) => r.files),
      openPath: (p, cwd) => void openPath(p, cwd),
      openNative: isNative() ? (p, cwd, reveal) => nativeOpenPath(p, cwd, reveal) : undefined,
      openSubagent,
      focusSession,
      refocus: refocusAfterDialog,
      codeActions: (code, lang, session) => (lang === "bang" ? <RunButton cmd={code} session={session} /> : null),
      composerOverlay: (session) => <Overlay session={session} />,
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
        sendKey: config.sendKey,
        showThinking: config.showThinking,
        virtualList: config.virtualList,
        expandEdits: config.expandEdits,
        confirmLinks: config.confirmLinks,
        pinnedCommands: config.pinnedCommands,
      },
      setPinnedCommands: (names) => setConfig({ pinnedCommands: names }),
    }),
    [config, agents],
  );
  return <ChatProvider host={host}>{children}</ChatProvider>;
}
