// Spike: a Gerdr chat page drawn by nsh's native views instead of the web UI.
// Questions it answers (see README.md): can it hold Gerdr's /ws, render a streaming transcript as Markdown with
// selection across messages, and does an edit to this file show up in a running window (agents editing the UI)?
import { mount, signal, computed, Text, View, Scroll } from "nsh";
import { Input } from "nsh/base";
import { TextView } from "nsh/base";
import { connect } from "websocket";
import { readFile } from "fs/promises";
import { homedir } from "os";

const BASE = "127.0.0.1:7870";

type Msg = { key: string; role: "user" | "assistant" | "tool"; text: string };
type Session = { id: string; title?: string; name?: string; cwd?: string; lastSeq?: number };

const token = signal("");
const sessions = signal<Session[]>([]);
const current = signal<string>("");
const messages = signal<Msg[]>([]);
const status = signal("starting");
const draft = signal("");

let sock: any = null;

async function api(method: string, path: string, body?: unknown) {
  const r = await fetch(`http://${BASE}${path}`, {
    method,
    headers: { Authorization: `Bearer ${token()}`, "X-Gerdr": "1", "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : undefined,
  });
  return r.json();
}

// The transcript, cut down to what the spike needs: user text, agent text (chunks appended), one line per tool call
const chunkAt = new Map<string, number>();
function apply(evs: any[], reset: boolean) {
  let list = reset ? [] : messages().slice();
  if (reset) chunkAt.clear();
  for (const e of evs) {
    if (e.kind === "user") {
      const t = (e.content ?? []).filter((c: any) => c.type === "text").map((c: any) => c.text).join("\n");
      if (t) list.push({ key: `u${e.seq}`, role: "user", text: t });
    } else if (e.kind === "update") {
      const u = e.update ?? {};
      if (u.sessionUpdate === "agent_message_chunk" && u.content?.type === "text") {
        const k = `t:${u.messageId ?? ""}`;
        const i = chunkAt.get(k);
        if (i !== undefined && i === list.length - 1) list[i] = { ...list[i], text: list[i].text + u.content.text };
        else {
          list.push({ key: `a${e.seq}`, role: "assistant", text: u.content.text });
          chunkAt.set(k, list.length - 1);
        }
      } else if (u.sessionUpdate === "tool_call") {
        list.push({ key: `c${e.seq}`, role: "tool", text: u.title ?? u.kind ?? "tool" });
      }
    }
  }
  messages.set(list);
}

async function openWS() {
  status.set("connecting /ws");
  sock = await connect(`ws://${BASE}/ws`, { headers: { Authorization: `Bearer ${token()}` } });
  status.set("connected");
  const first = sessions()[0];
  if (first) watch(first.id);
  for (let raw; (raw = await sock.read()) !== null; ) {
    let batch: any;
    const size = typeof raw === "string" ? raw.length : raw.byteLength;
    const t0 = Date.now();
    try {
      batch = JSON.parse(typeof raw === "string" ? raw : new TextDecoder().decode(raw));
    } catch {
      continue;
    }
    if (!Array.isArray(batch)) batch = [batch];
    for (const m of batch) {
      if (m.t === "events" && m.id === current()) {
        apply(m.events ?? [], !!m.reset);
        if ((m.events ?? []).length > 50) console.log(`events frame ${size} bytes, ${m.events.length} events, ${messages().length} msgs, parse+apply ${Date.now() - t0}ms`);
      }
    }
  }
  status.set(`closed ${sock.closeCode}`);
}

async function watch(id: string) {
  current.set(id);
  messages.set([]);
  await sock?.write(JSON.stringify({ t: "watch", ids: [id], since: {} }));
}

async function send() {
  const text = draft().trim();
  if (!text || !current()) return;
  draft.set("");
  await api("POST", `/api/sessions/${current()}/send`, { content: [{ type: "text", text }], id: `spike-${Date.now()}` });
}

async function start() {
  try {
    token.set((await readFile(`${homedir()}/.gerdr/token`, "utf8")).trim());
    const r = await api("GET", "/api/sessions");
    const list: Session[] = Array.isArray(r) ? r : (r.sessions ?? Object.values(r));
    sessions.set(list);
    openWS().catch((e) => status.set(`ws error: ${e?.code ?? ""} ${e?.message ?? e}`));
  } catch (e: any) {
    status.set(`error: ${e?.code ?? ""} ${e?.message ?? e}`);
  }
}

function SessionRow(props: { s: Session }) {
  const on = computed(() => current() === props.s.id);
  return (
    <View onPress={() => watch(props.s.id)} class="px-3 py-2 rounded-md active:opacity-60" style={{ backgroundColor: on() ? "#2563eb22" : "transparent" }}>
      <Text class="text-foreground text-sm truncate">
        {props.s.title || props.s.name || props.s.id}
      </Text>
    </View>
  );
}

function Bubble(props: { m: Msg }) {
  return (
    <If
      when={props.m.role === "tool"}
      else={
        <View class="px-4 py-2" style={{ alignItems: props.m.role === "user" ? "flex-end" : "stretch" }}>
          <View class="rounded-lg px-3 py-2" style={{ backgroundColor: props.m.role === "user" ? "#8881" : "transparent", maxWidth: "90%" }}>
            <TextView markdown={props.m.text} />
          </View>
        </View>
      }
    >
      <Text class="text-foreground px-4 py-1 text-xs opacity-60">⚙ {props.m.text}</Text>
    </If>
  );
}

function App() {
  start();
  return (
    <View class="flex-1 flex-row bg-background">
      <View style={{ width: 240 }} class="border-r border-border p-2 gap-1">
        <Text class="text-foreground text-xs opacity-60 px-3 py-1">{status()}</Text>
        <Scroll class="flex-1">
          <Each of={sessions()} key={(s) => s.id}>
            {(s) => <SessionRow s={s()} />}
          </Each>
        </Scroll>
      </View>
      <View class="flex-1">
        <Scroll class="flex-1">
          <View selectable class="py-2">
            <Each of={messages()} key={(m) => m.key}>
              {(m) => <Bubble m={m()} />}
            </Each>
          </View>
        </Scroll>
        <View class="border-t p-2">
          <Input value={draft()} onChange={({ value }) => draft.set(value)} onSubmit={send} placeholder="Send to this session" class="p-2" />
        </View>
      </View>
    </View>
  );
}

mount(App);
