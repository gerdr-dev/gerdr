// One conversation: the transcript (user bubbles, Markdown replies, tool calls folded into groups) and the composer
// under it. Modeled on the web chat (ui/packages/chat: Rows.tsx summarize / buildRows, StatusLine.tsx, MessageView.tsx).
import { signal, computed, effect, cleanup, keymap, colorScheme, Text, View, Scroll, Image, List } from "nsh";
import type { ScrollRef } from "nsh";
import { TextView, Select, Button, RichEditor, createRichEditor, starterKit, suggestion, replaceSuggestion } from "nsh/base";
import { transcript, sessions, options, send, setConfig, setMode, cancel, api, loadOlder } from "./store";
import type { Part, Session } from "./store";
import { PluginSlot, plugins, pluginsHosted, loadPlugins, pluginSettings, onComposerEdit, pluginError } from "./Plugins";

const STATUS_COLOR: Record<string, string> = { running: "#22c55e", waiting: "#f59e0b", error: "#ef4444", starting: "#60a5fa", idle: "#71717a" };
const MUTED = "#a1a1aa";
const BRAND = "#60a5fa";

export function StatusDot(props: { status: string }) {
  return <View style={{ width: 7, height: 7, borderRadius: 4, backgroundColor: STATUS_COLOR[props.status] ?? "#71717a" }} />;
}

// ---- icons: lucide's paths as SVG images (the web UI uses lucide-react) ----
const ICONS: Record<string, string> = {
  wrench:
    '<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>',
  terminal: '<path d="m7 11 2-2-2-2"/><path d="M11 13h4"/><rect width="18" height="18" x="3" y="3" rx="2" ry="2"/>',
  file: '<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M10 9H8"/><path d="M16 13H8"/><path d="M16 17H8"/>',
  search: '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>',
  globe: '<circle cx="12" cy="12" r="10"/><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"/><path d="M2 12h20"/>',
  pencil: '<path d="M21.17 6.81a1 1 0 0 0-3.99-3.99L3.84 16.17a2 2 0 0 0-.5.83l-1.32 4.35a.5.5 0 0 0 .62.62l4.35-1.32a2 2 0 0 0 .83-.5z"/>',
  chevron: '<path d="m6 9 6 6 6-6"/>',
  steer: '<polyline points="15 10 20 15 15 20"/><path d="M4 4v7a4 4 0 0 0 4 4h12"/>',
};
const iconCache = new Map<string, string>();
function iconSrc(name: string, color: string) {
  const k = name + color;
  let v = iconCache.get(k);
  if (!v) {
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="${color}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${ICONS[name]}</svg>`;
    iconCache.set(k, (v = `data:image/svg+xml;base64,${Buffer.from(svg).toString("base64")}`));
  }
  return v;
}
function Icon(props: { name: string; size?: number; color?: string; turn?: number }) {
  const s = props.size ?? 14;
  return <Image src={iconSrc(props.name, props.color ?? MUTED)} style={{ width: s, height: s, transform: props.turn ? [{ rotate: `${props.turn}deg` }] : undefined } as any} />;
}
const KIND_ICON: Record<string, string> = { execute: "terminal", read: "file", search: "search", fetch: "globe", edit: "pencil" };

// ---- time ----
export const now = signal(Date.now());
setInterval(() => now.set(Date.now()), 1000);
export function fmtElapsed(ms: number) {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}
export function fmtTokens(n: number) {
  if (n < 1000) return String(n);
  if (n < 100_000) return `${(n / 1000).toFixed(1)}k`;
  return `${Math.round(n / 1000)}k`;
}
const hhmm = (ts: number) => {
  const d = new Date(ts);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
};

// ---- rows: runs of tool calls / thoughts between texts fold into one group row ----
type Tool = Extract<Part, { kind: "tool" }>;
type Row = { key: string; kind: "part"; p: Part } | { key: string; kind: "group"; tools: Tool[]; thoughts: number };

const KIND_TEXT: [string, (n: number) => string][] = [
  ["execute", (n) => `跑了 ${n} 条命令`],
  ["read", (n) => `读了 ${n} 个文件`],
  ["search", (n) => `搜了 ${n} 次`],
  ["fetch", (n) => `抓了 ${n} 个网页`],
];
function summarize(tools: Tool[], thoughts: number) {
  const n: Record<string, number> = {};
  for (const t of tools) {
    const k = KIND_TEXT.some(([k]) => k === t.toolKind) ? t.toolKind! : "other";
    n[k] = (n[k] ?? 0) + 1;
  }
  const bits = KIND_TEXT.filter(([k]) => n[k]).map(([k, f]) => f(n[k]));
  if (n.other) bits.push(`调了 ${n.other} 次工具`);
  let s = bits.join("、");
  if (thoughts) s += (s ? "，" : "") + `思考 ${thoughts} 次`;
  return s;
}

function buildRows(parts: Part[]): Row[] {
  const rows: Row[] = [];
  let g: Extract<Row, { kind: "group" }> | null = null;
  for (const p of parts) {
    if (p.kind === "tool" || p.kind === "thought") {
      if (!g) rows.push((g = { key: `g${p.key}`, kind: "group", tools: [], thoughts: 0 }));
      if (p.kind === "tool") g.tools.push(p);
      else g.thoughts++;
      continue;
    }
    g = null;
    rows.push({ key: p.key, kind: "part", p });
  }
  return rows;
}

const openGroups = signal<Record<string, boolean>>({});

function ToolLine(props: { t: Tool; live: boolean }) {
  const running = () => !props.t.ended && props.live;
  return (
    <View class="flex-row items-center gap-2.5 py-1" style={{ paddingLeft: 2 }}>
      <Icon name={KIND_ICON[props.t.toolKind ?? ""] ?? "wrench"} color={props.t.status === "failed" ? "#ef4444" : MUTED} />
      <Text class="text-[13px] truncate flex-1" style={{ color: MUTED, fontFamily: props.t.toolKind === "execute" ? "Menlo" : undefined }}>
        {props.t.title}
      </Text>
      <If when={running()}>
        <Text class="text-[12px]" style={{ color: MUTED }}>
          {fmtElapsed(now() - props.t.seen)}
        </Text>
      </If>
    </View>
  );
}

function Group(props: { r: Extract<Row, { kind: "group" }>; live: boolean }) {
  const open = computed(() => !!openGroups()[props.r.key]);
  // Collapsed, the call still running peeks out under the header (as the web chat does after a second)
  const peek = computed(() => {
    if (open() || !props.live) return null;
    const last = props.r.tools[props.r.tools.length - 1];
    return last && !last.ended && now() - last.seen > 1000 ? last : null;
  });
  const done = computed(() => props.r.tools.filter((t) => !peek() || t !== peek()));
  return (
    <View class="px-5 py-1">
      <View
        onPress={() => openGroups.set({ ...openGroups(), [props.r.key]: !open() })}
        class="flex-row items-center gap-2.5 py-1 self-start active:opacity-60"
        style={{ cursor: "pointer" } as any}
      >
        <Icon name="wrench" />
        <Text class="text-[13.5px]" style={{ color: MUTED }}>
          {summarize(done(), props.r.thoughts) || "思考中"}
        </Text>
        <Icon name="chevron" size={13} turn={open() ? 0 : -90} />
      </View>
      <If when={open()}>
        <View class="pl-1 border-l border-border ml-1.5" style={{ paddingLeft: 12 }}>
          <Each of={props.r.tools} key={(t) => t.key}>
            {(t) => <ToolLine t={t()} live={props.live} />}
          </Each>
        </View>
      </If>
      <If when={peek()}>
        <ToolLine t={peek()!} live />
      </If>
    </View>
  );
}

// The web chat's user bubble (packages/chat/src/ai/message.tsx): bg-brand, and brand at 70% in the dark theme.
// --brand is oklch(0.58 0.15 255) light / oklch(0.7 0.13 250) dark (ui/src/index.css), here in sRGB.
const userBubble = () => (colorScheme() === "dark" ? "rgba(90,163,236,0.7)" : "rgb(51,123,208)");

function UserBubble(props: { p: Extract<Part, { kind: "user" }> }) {
  return (
    <View class="px-5 pt-3 pb-1 items-end gap-1.5">
      <View class="px-4 py-2.5 gap-2" style={{ maxWidth: "85%", borderRadius: 22, backgroundColor: userBubble() }}>
        <If when={props.p.images.length > 0}>
          <View class="flex-row gap-2 flex-wrap">
            <Each of={props.p.images}>
              {(src) => (
                <If when={src()} else={<View class="rounded-md bg-muted" style={{ width: 120, height: 90 }} />}>
                  <Image src={src()} class="rounded-md" style={{ width: 120, height: 90, objectFit: "cover" }} />
                </If>
              )}
            </Each>
          </View>
        </If>
        <If when={props.p.text}>
          <Text class="text-[14.5px] leading-[22px]" style={{ color: "#ffffff" }}>
            {props.p.text}
          </Text>
        </If>
      </View>
      <View class="flex-row items-center gap-1.5 pr-1">
        <If when={props.p.steer}>
          <Icon name="steer" size={12} />
          <Text class="text-[12px]" style={{ color: MUTED }}>
            中途插话 ·
          </Text>
        </If>
        <Text class="text-[12px]" style={{ color: MUTED }}>
          {hhmm(props.p.ts)}
        </Text>
      </View>
    </View>
  );
}

// Pixel orbit (the web UI's PixelLoader): a 3×3 grid, one cell lit going round the edge
const ORBIT = [0, 1, 2, 5, 8, 7, 6, 3];
const tickFast = signal(0);
setInterval(() => tickFast.set((tickFast() + 1) % ORBIT.length), 110);
function PixelLoader(props: { color: string }) {
  return (
    <View class="flex-row flex-wrap" style={{ width: 13, height: 13, gap: 1 }}>
      <Each of={[0, 1, 2, 3, 4, 5, 6, 7, 8]}>
        {(i) => {
          const d = computed(() => (ORBIT.indexOf(i()) - tickFast() + ORBIT.length) % ORBIT.length);
          return <View style={{ width: 3.6, height: 3.6, borderRadius: 0.6, backgroundColor: props.color, opacity: i() === 4 ? 0.15 : d() === 0 ? 1 : d() === 7 ? 0.6 : d() === 6 ? 0.35 : 0.15 }} />;
        }}
      </Each>
    </View>
  );
}

const VERBS = ["Pondering", "Thinking", "Working", "Cooking", "Brewing", "Crafting", "Musing", "Noodling", "Percolating", "Ruminating", "Simmering", "Tinkering", "Conjuring", "Computing", "Deliberating", "Forging", "Hatching", "Mulling", "Puzzling", "Scheming", "Spinning", "Synthesizing", "Untangling", "Whirring"];

function StatusLine(props: { s: Session }) {
  const started = () => (props.s as any).turn?.startedAt ?? now();
  const tokens = () => (props.s as any).turn?.outputTokens ?? 0;
  const waiting = () => props.s.status === "waiting";
  const color = () => (waiting() ? "#f59e0b" : BRAND);
  return (
    <View class="px-5 pt-2 pb-1 flex-row items-center gap-2" style={{ height: 28 }}>
      <PixelLoader color={color()} />
      <Text class="text-[13.5px]" style={{ color: color() }}>
        {waiting() ? "等你处理" : `${VERBS[Math.abs(Math.floor(started() / 1000)) % VERBS.length]}…`}
      </Text>
      <Text class="text-[12.5px]" style={{ color: MUTED, fontVariant: ["tabular-nums"] } as any}>
        {fmtElapsed(now() - started()) + (tokens() > 0 ? ` · ↓ ${fmtTokens(tokens())} tokens` : "") + " · esc 打断"}
      </Text>
    </View>
  );
}

function Transcript(props: { id: string }) {
  const parts = computed(() => transcript(props.id)());
  // Rows keep their object while what they show is the same: a new object re-sets that row's bindings (a TextView
  // gets its Markdown again and lays it out again). Rebuilding every row on each streamed chunk re-laid the whole
  // conversation many times a second: the content height wobbled by a row (~25pt) and reading above the end
  // flickered and jumped (seen on a phone: 44638 ↔ 44612 ↔ 44587 while the agent wrote).
  const kept = new Map<string, Row>();
  const same = (a: Row, b: Row) =>
    a.kind === "part" && b.kind === "part"
      ? a.p === b.p
      : a.kind === "group" && b.kind === "group" && a.thoughts === b.thoughts && a.tools.length === b.tools.length && a.tools.every((t, i) => t === b.tools[i]);
  const rows = computed(() => {
    return buildRows(parts()).map((r) => {
      const old = kept.get(r.key);
      if (old && same(old, r)) return old;
      kept.set(r.key, r);
      return r;
    });
  });
  const sess = computed(() => sessions().find((s) => s.id === props.id));
  const busy = computed(() => sess()?.status === "running" || sess()?.status === "waiting");
  // The last group is the live one while the turn runs: its running call peeks out, with a clock
  const liveKey = computed(() => {
    const r = rows();
    for (let i = r.length - 1; i >= 0; i--) if (r[i].kind === "group") return busy() ? r[i].key : "";
    return "";
  });
  // Newest first, for an inverted list: item 0 is at the bottom. The status line ("Forging… 21s") is item 0 while
  // the turn runs, so it sits under the last reply.
  type Item = Row | { key: "__status"; kind: "status" };
  const items = computed<Item[]>(() => {
    const r = rows().slice().reverse() as Item[];
    return busy() && sess() ? [{ key: "__status", kind: "status" }, ...r] : r;
  });
  // An inverted List, as nsh's docs build a chat: it keeps its distance from the end as items change (follows new
  // output while the reader is at the bottom), keeps what's on screen in place when rows above it change height, and
  // makes only the rows near the screen. A Scroll with every row in it was measured on a fling through a long
  // conversation: the content height changed 20 times in 4 s (-26 … +2642 pt) as rows above the screen were laid
  // out, nothing held the visible rows still, and they jumped up and down; frames went blank mid-fling (reported).
  return (
    <List
      each={items()}
      keyExtractor={(r) => r.key}
      inverted
      estimatedItemSize={120}
      // Inverted: the list's end is the top, the oldest rows; near it, the stretch before them (store.loadOlder)
      onLoadMore={() => loadOlder(props.id)}
      class="flex-1"
      contentStyle={{ paddingVertical: 12 } as any}
    >
      {(r) => (
        <View class="mx-auto w-full" style={{ maxWidth: 880 }}>
          <If when={r().kind === "status"}>
            <StatusLine s={sess()!} />
          </If>
          <If when={r().kind === "group"}>
            <Group r={r() as any} live={r().key === liveKey()} />
          </If>
          <If when={r().kind === "part" && (r() as any).p.kind === "user"}>
            <UserBubble p={(r() as any).p} />
          </If>
          <If when={r().kind === "part" && (r() as any).p.kind === "text"}>
            <View selectable class="px-5 py-1.5">
              <TextView markdown={(r() as any).p.text} />
            </View>
          </If>
          <If when={r().kind === "part" && (r() as any).p.kind === "stat"}>
            <Text class="px-5 pt-1 pb-2 text-[12px]" style={{ color: MUTED }}>
              {`用时 ${fmtElapsed((r() as any).p.ms)}` + ((r() as any).p.tokens ? ` · ↓ ${fmtTokens((r() as any).p.tokens)} tokens` : "") + ` · ${hhmm((r() as any).p.ts)}`}
            </Text>
          </If>
        </View>
      )}
    </List>
  );
}

type Pasted = { data: string; mimeType: string; url: string };

function Picker(props: { label: string; value: string; items: { value: string; title: string }[]; onChange: (v: string) => void }) {
  return (
    <Select
      items={props.items}
      value={props.value}
      onChange={(e) => e.value != null && props.onChange(e.value)}
      popupClass="rounded-lg border border-border bg-popover p-1 min-w-[150px]"
      itemClass="rounded-md px-2.5 py-1.5 text-foreground text-[13px] focus:bg-accent selected:bg-accent"
      trigger={
        <View class="flex-row items-center gap-1 rounded-md px-2 py-1 active:opacity-60 hover:bg-accent">
          <Text class="text-muted-foreground text-[12.5px]">{props.items.find((i) => i.value === props.value)?.title ?? props.label}</Text>
          <Text class="text-muted-foreground text-[10px]">▾</Text>
        </View>
      }
    />
  );
}

function Composer(props: { id: string }) {
  // The input is a RichEditor (nsh's ProseMirror port), as the web composer is a Tiptap editor: input methods,
  // undo, pasted images (onPasteFiles), and Tiptap's Suggestion for "@" file completion
  const editor = createRichEditor({ plugins: [starterKit(), suggestion({ key: "file", char: "@", captureKeys: ["ArrowUp", "ArrowDown", "Enter", "Tab"] })] });
  const rev = signal(0);
  editor.on("revision", ({ revision }) => rev.set(revision as any));
  const empty = computed(() => (rev(), editor.getText().trim() === ""));
  const images = signal<Pasted[]>([]);
  const busy = signal(false);
  const focused = signal(false);
  const sess = computed(() => sessions().find((s) => s.id === props.id));
  const opts = computed(() => options(sess()?.optsKey));
  const running = computed(() => sess()?.status === "running");

  // "@" completion: the session's files matching the query (GET /api/sessions/:id/files, the web's searchFiles)
  const sug = computed(() => (rev(), editor.suggestion("file")));
  const files = signal<string[]>([]);
  const pick = signal(0);
  let asked = "";
  effect(() => {
    const st = sug();
    const q = st?.active ? (st.query ?? "") : null;
    if (q === null) return files.set([]);
    if (q === asked) return;
    asked = q;
    api("GET", `/api/sessions/${props.id}/files?q=${encodeURIComponent(q)}`).then((r) => {
      if (asked !== q) return;
      files.set((r?.files ?? []).slice(0, 8));
      pick.set(0);
    });
  });
  // Keys the suggestion took while its list is open (captureKeys): move / choose, as the web composer's list does
  let seenKeys = 0;
  effect(() => {
    const st = sug() as any;
    if (!st?.active) {
      seenKeys = 0;
      return;
    }
    if (!st.keyCount || st.keyCount === seenKeys) return;
    seenKeys = st.keyCount;
    const n = files().length;
    if (!n) return;
    if (st.lastKey === "ArrowDown") pick.set((pick() + 1) % n);
    else if (st.lastKey === "ArrowUp") pick.set((pick() - 1 + n) % n);
    else if (st.lastKey === "Enter" || st.lastKey === "Tab") choose(files()[pick()]);
  });
  function choose(f: string | undefined) {
    if (!f) return;
    editor.run(replaceSuggestion(`@${f} `, "file"));
    files.set([]);
  }

  // Voice: the latest reply for the speaker; errors shown for a few seconds beside the buttons
  const lastReply = () => {
    const ps = transcript(props.id)();
    for (let i = ps.length - 1; i >= 0; i--) if (ps[i].kind === "text") return (ps[i] as any).text as string;
    return "";
  };
  // This composer's plugin slot: what plugins write into the composer comes back by it
  const slotId = `composer:${props.id}:${Math.random().toString(36).slice(2, 8)}`;
  loadPlugins();
  cleanup(onComposerEdit(slotId, (text) => editor.setContent(text)));

  async function submit() {
    const text = editor.getText().trim();
    if ((!text && !images().length) || busy()) return;
    busy.set(true);
    const imgs = images();
    editor.setContent("");
    images.set([]);
    try {
      await send(props.id, text, imgs.map(({ data, mimeType }) => ({ data, mimeType })));
    } catch (e) {
      editor.setContent(text);
      images.set(imgs);
    }
    busy.set(false);
  }
  // Enter sends, Shift+Enter is a new line; not while the "@" list is open or an input method composes
  let composing = false;
  effect(() => {
    if (!focused()) return;
    const off = keymap.bind("enter", () => {
      if (composing || (sug()?.active && files().length)) return;
      submit();
    }, { when: "always" });
    cleanup(off);
  });

  async function onPasteFiles(e: { paste: { files: { name: string; mime: string; data: Uint8Array }[] } }) {
    for (const f of e.paste.files) {
      if (!f.mime.startsWith("image/")) continue;
      const b64 = Buffer.from(f.data).toString("base64");
      images.set([...images(), { data: b64, mimeType: f.mime, url: `data:${f.mime};base64,${b64}` }]);
    }
  }

  return (
    <View class="px-4 pb-4 pt-1">
      <If when={files().length > 0}>
        <View class="rounded-lg border border-border bg-popover p-1 mb-1.5">
          <Each of={files()}>
            {(f, i) => (
              <View onPress={() => choose(f())} class="flex-row items-center gap-2 rounded-md px-2.5 py-1.5" style={{ backgroundColor: i() === pick() ? "rgba(127,127,127,0.2)" : "transparent" }}>
                <Icon name="file" size={13} />
                <Text class="text-foreground text-[13px] truncate">
                  {f()}
                </Text>
              </View>
            )}
          </Each>
        </View>
      </If>
      <View class="rounded-xl border border-border bg-card">
        <If when={images().length > 0}>
          <View class="flex-row gap-2 px-3 pt-3">
            <Each of={images()}>
              {(img, i) => (
                <View>
                  <Image src={img().url} class="rounded-md border border-border" style={{ width: 64, height: 64, objectFit: "cover" }} />
                  <View
                    onPress={() => images.set(images().filter((_, j) => j !== i()))}
                    class="absolute rounded-full bg-foreground items-center justify-center active:opacity-60"
                    style={{ top: -6, right: -6, width: 18, height: 18 }}
                  >
                    <Text class="text-background text-[11px]">✕</Text>
                  </View>
                </View>
              )}
            </Each>
          </View>
        </If>
        <View>
          <RichEditor
            editor={editor}
            onPasteFiles={onPasteFiles}
            onFocus={() => focused.set(true)}
            onBlur={() => focused.set(false)}
            onCompositionChange={({ composing: c }) => (composing = c)}
            class="text-foreground text-[14px] max-h-56"
            style={{ minHeight: 44 }}
            classes={{ content: "px-3.5 pt-3 pb-1", caret: "bg-foreground" }}
          />
          <If when={empty()}>
            <Text class="absolute text-[14px]" style={{ left: 14, top: 12, color: "#71717a", pointerEvents: "none" } as any}>
              给 agent 发消息，@ 引用文件，⌘V 粘贴图片
            </Text>
          </If>
        </View>
        <View class="flex-row items-center gap-0.5 px-2 pb-2">
          {/* The pickers take what's left and give way first: on a phone the row (pickers, plugin buttons, context,
              send) was wider than the screen and pushed the send button half off it (reported) */}
          <View class="flex-1 flex-row items-center overflow-hidden" style={{ minWidth: 0 }}>
          <If when={opts()}>
            <Picker
              label="Mode"
              value={sess()?.modeId ?? ""}
              items={(opts()?.modes ?? []).map((m) => ({ value: m.id, title: m.name }))}
              onChange={(v) => setMode(props.id, v)}
            />
            <Each of={opts()?.config ?? []} key={(c) => c.id}>
              {(c) => (
                <Picker
                  label={c().name}
                  value={sess()?.optValues?.[c().id] ?? ""}
                  items={c().options.map((o) => ({ value: o.value, title: o.name }))}
                  onChange={(v) => setConfig(props.id, c().id, v)}
                />
              )}
            </Each>
          </If>
          </View>
          <If when={pluginError()}>
            <Text class="text-[11.5px] mr-2 truncate" style={{ color: "#ef4444", flexShrink: 1 }}>
              {pluginError()}
            </Text>
          </If>
          {/* Plugins' controls (their own nsh apps, placed by the host; see Plugins.tsx) */}
          <If when={pluginsHosted()}>
            <Each of={plugins()} key={(p) => p.id}>
              {(p) => (
                <PluginSlot
                  plugin={p().id}
                  slot={slotId}
                  src={p().src}
                  ctx={{ session: props.id, text: (rev(), editor.getText()), lastReply: lastReply(), settings: pluginSettings(p().id), hints: [sessionTitle(sess()), sess()?.cwd?.split("/").pop() ?? ""] }}
                  style={{ width: 64, height: 30, flexShrink: 0 }}
                />
              )}
            </Each>
          </If>
          <If when={sess()?.context}>
            <Text class="text-muted-foreground text-[11.5px] mr-2" style={{ flexShrink: 0 }}>{Math.round(((sess()?.context?.tokens ?? 0) / (sess()?.context?.window || 1)) * 100)}% ctx</Text>
          </If>
          <If
            when={running()}
            else={
              <Button onPress={submit} disabled={busy()} class="rounded-full bg-foreground items-center justify-center active:opacity-60 disabled:opacity-40" style={{ width: 28, height: 28, flexShrink: 0 }}>
                <Text class="text-background text-[14px]">↑</Text>
              </Button>
            }
          >
            <Button onPress={() => cancel(props.id)} class="rounded-full bg-foreground items-center justify-center active:opacity-60" style={{ width: 28, height: 28, flexShrink: 0 }}>
              <View class="bg-background" style={{ width: 9, height: 9, borderRadius: 2 }} />
            </Button>
          </If>
        </View>
      </View>
    </View>
  );
}

export function Chat(props: { id: string }) {
  return (
    <View class="flex-1">
      <Transcript id={props.id} />
      <Composer id={props.id} />
    </View>
  );
}

export function sessionTitle(s: Session | undefined) {
  return s?.title || s?.cwd?.split("/").pop() || s?.id || "";
}
