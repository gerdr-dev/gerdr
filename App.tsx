// Gerdr's Code view drawn by nsh: sidebar of conversations, tabs, panes split side by side, a chat in each pane.
// Spike: see README.md. Talks to the Gerdr running on this Mac. (Spike1.tsx is the first, one-page version.)
import { mount, signal, computed, effect, windowSize, keyboardFrame, Text, View, Scroll } from "nsh";
import { ResizablePanelGroup, ResizablePanel, Button } from "nsh/base";
import { start, sessions, conn, watch, api } from "./store";
import type { Session } from "./store";
import { Chat, StatusDot, sessionTitle } from "./Chat";
import { TermView, createTerm } from "./Term";
import { Overview, focusSession, overviewPages } from "./Overview";

// ---- layout: tabs, each a row of panes, each pane one session ----
type Tab = { id: number; panes: string[]; focus: number };
let nextTab = 1;
const tabs = signal<Tab[]>([]);
const activeTab = signal(0);
const tab = computed(() => tabs().find((t) => t.id === activeTab()));

function updateTab(id: number, f: (t: Tab) => Tab) {
  tabs.set(tabs().map((t) => (t.id === id ? f(t) : t)));
}

function newTab(sid: string) {
  const nt = { id: nextTab++, panes: [sid], focus: 0 };
  tabs.set([...tabs(), nt]);
  activeTab.set(nt.id);
}

function openSession(sid: string) {
  const t = tab();
  if (!t) return newTab(sid);
  // Already on screen in this tab: focus it; else it replaces the focused pane
  const at = t.panes.indexOf(sid);
  updateTab(t.id, (t) => (at >= 0 ? { ...t, focus: at } : { ...t, panes: t.panes.map((p, i) => (i === t.focus ? sid : p)) }));
}

function split(tid: number, at: number) {
  // The new pane starts on the same conversation; pick another in the sidebar (it goes to the focused pane)
  updateTab(tid, (t) => ({ ...t, panes: [...t.panes.slice(0, at + 1), t.panes[at], ...t.panes.slice(at + 1)], focus: at + 1 }));
}

// A pane is a session id, or "term:<id>" for a terminal
const isTerm = (p: string) => p.startsWith("term:");

async function splitTerminal(tid: number, at: number, cwd?: string) {
  const id = await createTerm(cwd);
  updateTab(tid, (t) => ({ ...t, panes: [...t.panes.slice(0, at + 1), `term:${id}`, ...t.panes.slice(at + 1)], focus: at + 1 }));
}

function closeTab(id: number) {
  const list = tabs().filter((t) => t.id !== id);
  tabs.set(list);
  if (activeTab() === id) activeTab.set(list[list.length - 1]?.id ?? 0);
}

function closePane(tid: number, at: number) {
  const t = tabs().find((t) => t.id === tid);
  if (!t) return;
  // A terminal's PTY goes with its pane: an open one holds Gerdr's self-update back (GET /api/busy)
  if (isTerm(t.panes[at]) && !t.panes.some((p, i) => i !== at && p === t.panes[at])) api("DELETE", `/api/terms/${t.panes[at].slice(5)}`).catch(() => {});
  if (t.panes.length === 1) return closeTab(tid);
  updateTab(tid, (t) => ({ ...t, panes: t.panes.filter((_, i) => i !== at), focus: Math.max(0, Math.min(t.focus, t.panes.length - 2)) }));
}

const byId = computed(() => new Map(sessions().map((s) => [s.id, s])));
const recent = computed(() =>
  sessions()
    .slice()
    .sort((a, b) => b.updatedAt - a.updatedAt)
    .slice(0, 60),
);

// ---- phone: below 700pt one pane at a time and the sidebar as a drawer, as Gerdr's phone UI (lib/phone.ts) ----
const phone = computed(() => windowSize().width < 700);
const drawer = signal(false);

// ---- sidebar ----
function SidebarRow(props: { s: Session }) {
  const on = computed(() => {
    const t = tab();
    return !!t && t.panes[t.focus] === props.s.id;
  });
  return (
    <View
      onPress={() => {
        // On a phone the overview shows it (it joins the pages); elsewhere it goes to the focused pane
        if (phone()) focusSession(props.s.id);
        else openSession(props.s.id);
        drawer.set(false);
      }}
      class="mx-2 px-2.5 py-1.5 rounded-md gap-0.5 active:opacity-70 hover:bg-accent"
      style={{ backgroundColor: on() ? "rgba(127,127,127,0.18)" : "transparent" }}
    >
      <View class="flex-row items-center gap-2">
        <StatusDot status={props.s.status} />
        <Text class="text-foreground text-[13px] truncate flex-1">{sessionTitle(props.s)}</Text>
      </View>
      <Text class="text-muted-foreground text-[11px] truncate" style={{ marginLeft: 15 }}>
        {(props.s.project ? props.s.project + " · " : "") + (props.s.cwd?.split("/").pop() ?? "")}
      </Text>
    </View>
  );
}

function Sidebar() {
  return (
    <View class="flex-1 bg-muted/40 border-r border-border">
      <View class="flex-row items-center px-4 pt-3 pb-2">
        <Text class="text-foreground text-[13px] font-semibold mr-3">Code</Text>
        <Text class="text-muted-foreground text-[11px] truncate flex-1 text-right">{conn()}</Text>
      </View>
      <Scroll class="flex-1">
        <View class="pb-3 gap-px">
          <Each of={recent()} key={(s) => s.id}>
            {(s) => <SidebarRow s={s()} />}
          </Each>
        </View>
      </Scroll>
    </View>
  );
}

// ---- tabs ----
function TabChip(props: { t: Tab }) {
  const on = computed(() => props.t.id === activeTab());
  const s = computed(() => byId().get(props.t.panes[props.t.focus]));
  return (
    <View
      onPress={() => activeTab.set(props.t.id)}
      class="flex-row items-center gap-2 rounded-md pl-2.5 pr-1 py-1 active:opacity-70 hover:bg-accent"
      style={{ backgroundColor: on() ? "rgba(127,127,127,0.18)" : "transparent", maxWidth: 230 }}
    >
      <StatusDot status={s()?.status ?? "idle"} />
      <Text class="text-foreground text-[12.5px] truncate" style={{ opacity: on() ? 1 : 0.6, flexShrink: 1 }}>
        {sessionTitle(s()) + (props.t.panes.length > 1 ? `  +${props.t.panes.length - 1}` : "")}
      </Text>
      <View onPress={() => closeTab(props.t.id)} class="rounded px-1 active:opacity-60 hover:bg-accent">
        <Text class="text-muted-foreground text-[11px]">✕</Text>
      </View>
    </View>
  );
}

function TabBar() {
  return (
    <View class="flex-row items-center border-b border-border px-2 gap-1" style={{ height: 38 }}>
      <If when={phone()}>
        <View onPress={() => drawer.set(true)} class="rounded-md px-2 py-1 active:opacity-60">
          <Text class="text-foreground text-[16px]">☰</Text>
        </View>
      </If>
      <Each of={tabs()} key={(t) => t.id}>
        {(t) => <TabChip t={t()} />}
      </Each>
      <View onPress={() => recent()[0] && newTab(recent()[0].id)} class="rounded-md px-2 py-1 active:opacity-60 hover:bg-accent">
        <Text class="text-muted-foreground text-[14px]">＋</Text>
      </View>
    </View>
  );
}

// ---- panes ----
function Pane(props: { tid: number; at: number; sid: string; count: number }) {
  const term = () => isTerm(props.sid);
  const s = computed(() => byId().get(props.sid));
  const focused = computed(() => tabs().find((t) => t.id === props.tid)?.focus === props.at);
  return (
    <View
      class="flex-1"
      onPressDown={() => !focused() && updateTab(props.tid, (t) => ({ ...t, focus: props.at }))}
      style={{ borderTopWidth: 2, borderTopColor: focused() && props.count > 1 ? "#3b82f6" : "transparent" }}
    >
      <View class="flex-row items-center gap-2 px-4 border-b border-border" style={{ height: 34 }}>
        <If when={!term()} else={<Text class="text-foreground text-[12.5px] font-medium flex-1">终端</Text>}>
          <StatusDot status={s()?.status ?? "idle"} />
          <Text class="text-foreground text-[12.5px] font-medium truncate flex-1">{sessionTitle(s())}</Text>
          <Text class="text-muted-foreground text-[11px]">{(s()?.agent ?? "") + (s()?.branch ? " · " + s()!.branch : "")}</Text>
        </If>
        <Button onPress={() => splitTerminal(props.tid, props.at, s()?.cwd)} class="rounded px-1.5 py-0.5 active:opacity-60 hover:bg-accent">
          <Text class="text-muted-foreground text-[12px]">终端</Text>
        </Button>
        <Button onPress={() => split(props.tid, props.at)} class="rounded px-1.5 py-0.5 active:opacity-60 hover:bg-accent">
          <Text class="text-muted-foreground text-[12px]">分屏</Text>
        </Button>
        <Button onPress={() => closePane(props.tid, props.at)} class="rounded px-1.5 py-0.5 active:opacity-60 hover:bg-accent">
          <Text class="text-muted-foreground text-[11px]">✕</Text>
        </Button>
      </View>
      {/* keyed on the pane's content: a pane switched to another conversation gets a fresh chat */}
      <Each of={[props.sid]} key={(x) => x}>
        {(id) => (
          <If when={isTerm(id())} else={<Chat id={id()} />}>
            <TermView id={id().slice(5)} />
          </If>
        )}
      </Each>
    </View>
  );
}

function Workspace() {
  const panes = computed(() => {
    const t = tab();
    const all = t ? t.panes.map((sid, at) => ({ key: `${t.id}:${at}:${t.panes.length}`, tid: t.id, at, sid, count: t.panes.length })) : [];
    return phone() && t ? all.filter((p) => p.at === t.focus) : all;
  });
  // The group is made again when the number of panes changes (sizes start even), keyed on the tab and the count
  const groupKey = computed(() => (tab() ? [`${tab()!.id}:${tab()!.panes.length}:${phone() ? tab()!.focus : "all"}`] : []));
  return (
    <If
      when={tab()}
      else={
        <View class="flex-1 items-center justify-center">
          <Text class="text-muted-foreground text-[13px]">在左边选一个对话</Text>
        </View>
      }
    >
      <Each of={groupKey()} key={(k) => k}>
        {() => (
          <ResizablePanelGroup class="flex-1" handleClass="bg-border w-px">
            <Each of={panes()} key={(p) => p.key}>
              {(p) => (
                <ResizablePanel minSize={320}>
                  <Pane tid={p().tid} at={p().at} sid={p().sid} count={p().count} />
                </ResizablePanel>
              )}
            </Each>
          </ResizablePanelGroup>
        )}
      </Each>
    </If>
  );
}

function App() {
  start().catch((e) => conn.set(`error: ${e?.message ?? e}`));
  // Every session on screen in any tab is watched (its log streamed), as the web UI's syncWatch does
  effect(() => watch(phone() ? overviewPages() : tabs().flatMap((t) => t.panes.filter((p) => !isTerm(p)))));
  // Open the most recent conversation, so there is something to look at
  effect(() => {
    if (!tabs().length && recent().length) newTab(recent()[0].id);
  });
  return (
    <If
      when={phone()}
      else={
        <ResizablePanelGroup class="flex-1 bg-background" handleClass="bg-border w-px">
          <ResizablePanel size={270} minSize={200} maxSize={420} fixed>
            <Sidebar />
          </ResizablePanel>
          <ResizablePanel>
            <View class="flex-1">
              <TabBar />
              <Workspace />
            </View>
          </ResizablePanel>
        </ResizablePanelGroup>
      }
    >
      {/* The software keyboard's height lifts the composer above it; the bottom safe area only while it is down
          (the keyboard's height already covers the navigation bar: both together left a gap) */}
      <View class="flex-1 bg-background" style={{ paddingTop: "env(safe-area-inset-top)", paddingBottom: keyboardFrame().height > 0 ? 0 : "env(safe-area-inset-bottom)" } as any}>
        <View class="flex-1" style={{ paddingBottom: "env(keyboard-inset-height)" } as any}>
          {/* The phone's overview: the active agents as pages, swiped sideways (Overview.tsx) */}
          <Overview onMenu={() => drawer.set(true)} />
        </View>
        <If when={drawer()}>
          <View class="absolute inset-0 flex-row">
            <View class="bg-background" style={{ width: "84%", paddingTop: "env(safe-area-inset-top)" } as any}>
              <Sidebar />
            </View>
            <View class="flex-1" style={{ backgroundColor: "rgba(0,0,0,0.5)" }} onPress={() => drawer.set(false)} />
          </View>
        </If>
      </View>
    </If>
  );
}

mount(App);
