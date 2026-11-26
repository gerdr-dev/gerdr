import { currentHost, currentSpace, DEFAULT_SPACE, newWorkspace, placeOwned, viewWorkspaces, type Layout } from "./layout";
import { get, setLayout, setMode, setWorkPanel, useStore } from "./store";

type State = ReturnType<typeof get>;
import { panelOfKey, spaceOfKey } from "./work";

// Code mode and each panel drawing its own Code list (core CodeView list="replace") have a layout of their own: the workspaces whose `host` is
// theirs (layout.ts Workspace.host). Both are in the one stored layout, so everything that walks all of it (where a
// conversation is, which terminals still have a pane) keeps working, but each view lists, numbers and shows only its
// own. Before (2026-11-26) a panel only drew its own list over Code's workspaces: whatever Code had in front stayed in
// front in the panel, a worker's tab landed in Code's workspaces (hidden there by a filter), and a split could hold
// both. docs/dev/HOSTED-CODE.md
//
// Two things keep it so, whatever changed the state:
// - the view on screen and the active workspace agree: switching to a panel (or back to Code) brings back the
//   workspace that view was left on; focusing a pane of the other view (a notification, ⌘K) switches to that view;
// - a conversation's pane is in its owner's view: a panel's conversation (tags.panel) in that panel's, the rest in
//   Code's. Conversations are never shared: importing one into a panel starts a new one that carries it on.

/** Whose view a conversation's pane belongs in: "" = Code's, a panel key = that panel's (it hosts Code); undefined = not known yet. */
export function ownerHost(session: string, s: Pick<State, "sessions" | "codeHosts"> = get()): string | undefined {
  const ses = s.sessions[session];
  if (!ses) return undefined;
  const k = ses.tags?.panel;
  if (!k) return "";
  // Whether a panel has a Code of its own is only known on a device where its CodeView has mounted (codeHosts). Not
  // known here = leave its panes where they are: taking it for Code's moved every pane of the panel into Code's view on
  // a device that hadn't opened the panel (a phone, another window), the device showing the panel moved them back, and
  // the two kept overwriting each other's layout, so a conversation just started from the panel lost its pane (user
  // report: 开始 did nothing, the task was made each time).
  const host = s.codeHosts[k];
  if (!host) return undefined;
  // A panel that adds its part over Code's list (list="add") shows its conversations in Code ("open in Code"), as before
  return host.list === "replace" ? k : "";
}

/** The view on screen: "" = Code mode, a panel key = that panel's Code; null = a panel page that isn't Code. */
function shownHost(s: Pick<State, "mode" | "layout" | "workPanels" | "codeHosts">): string | null {
  if (s.mode !== "work") return "";
  const space = currentSpace(s.layout);
  const panel = s.workPanels[space];
  const key = panel ? `${space}/${panel}` : space;
  if (!s.codeHosts[key]) return null;
  return ownLayout(s, key) ? key : "";
}

/**
 * Whether panel `key` has a layout of its own: one drawing the whole list (CodeView list="replace"). One that adds its
 * part above Code's own list (list="add") shows Code's workspaces, so it shows Code's layout.
 */
const ownLayout = (s: Pick<State, "codeHosts">, key: string) => s.codeHosts[key]?.list === "replace";

/** Show view `host` of the current project: Code mode, or that panel. */
function showView(host: string) {
  if (!host) return void (get().mode !== "agents" && setMode("agents"));
  setWorkPanel(spaceOfKey(host), panelOfKey(host));
  if (get().mode !== "work") setMode("work");
}

// The workspace each view was left on, on this device (as layout.View keeps where one looks): "<project>\n<host>"
const LEFT_KEY = "gerdr.hostActive";
let left: Record<string, string> = (() => {
  try {
    return JSON.parse(localStorage.getItem(LEFT_KEY) || "{}") ?? {};
  } catch {
    return {};
  }
})();
function remember(l: Layout) {
  const k = `${currentSpace(l)}\n${currentHost(l)}`;
  if (left[k] === l.active) return;
  left = { ...left, [k]: l.active };
  try {
    localStorage.setItem(LEFT_KEY, JSON.stringify(left));
  } catch {}
}

/** Bring view `host` of the current project on screen: the workspace it was left on, else its first, else a new one. */
function enterView(l: Layout, host: string): Layout {
  const space = currentSpace(l);
  const list = viewWorkspaces(l, { space, host });
  const back = list.find((w) => w.id === left[`${space}\n${host}`]) ?? list[0];
  if (back) return { ...l, active: back.id };
  const w = { ...newWorkspace(host ? panelOfKey(host) || "default" : "default") };
  if (space !== DEFAULT_SPACE) w.space = space;
  if (host) w.host = host;
  return { ...l, workspaces: [...l.workspaces, w], active: w.id };
}

let busy = false;
function sync(s: State, prev: State) {
  if (busy) return;
  busy = true;
  try {
    // Every conversation in its owner's view. Unknown ones (not loaded yet) wait
    if (s.layout !== prev.layout || s.sessions !== prev.sessions || s.codeHosts !== prev.codeHosts) {
      const st = get();
      setLayout((l) =>
        placeOwned(l, (id) => {
          const host = ownerHost(id, st);
          return host === undefined ? undefined : { host, cwd: st.sessions[id]?.cwd };
        }),
      );
    }
    const now = get();
    const want = shownHost(now);
    if (want === null) return;
    const l = now.layout;
    const cur = currentHost(l);
    if (cur === want) return void remember(l);
    // Which of the two moved: the view on screen (the rail, a mode switch, another project) brings back its own
    // workspace; a pane of the other view brought to the front (by an action, in the same project) takes the screen there
    const viewMoved =
      now.mode !== prev.mode || now.workPanels !== prev.workPanels || now.codeHosts !== prev.codeHosts || currentSpace(l) !== currentSpace(prev.layout);
    if (!viewMoved && l.active !== prev.layout.active && (cur === "" || ownLayout(now, cur))) {
      showView(cur);
      if (shownHost(get()) === cur) return void remember(get().layout);
    }
    setLayout((x) => enterView(x, want));
    remember(get().layout);
  } finally {
    busy = false;
  }
}

/** Start keeping the views apart (once, at start-up). */
export function startHostViews() {
  useStore.subscribe(sync);
  const s = get();
  sync(s, s);
}
