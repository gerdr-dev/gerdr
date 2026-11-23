import { useContext, useEffect, useSyncExternalStore } from "react";
import { mountName } from "@/lib/mounts";
import { get as getApp, useStore } from "@/lib/store";
import { workSessionOf } from "@/lib/work";
import { t } from "@/lib/i18n";
import { PanelKeyContext, isPanelShown } from "./visible";

/**
 * What the user has on screen in a panel, handed to that panel's conversations with the message they send, so "this
 * paragraph" / "the file I have open" reaches the agent without anyone pasting a path (docs/dev/ARCHITECTURE.md,
 * "Screen context"). Three sources, per panel:
 *  - the page's own words (useScreenContext): which content / task / item is open;
 *  - the artifacts folder view (Folder.tsx): the file open in its preview;
 *  - the text selected anywhere in the panel's page (tracked here from `selectionchange`).
 * chatHost's send puts it in front of a message as a `<gerdr-screen>` block when it changed since the last one that
 * conversation got (the chat draws the block as a notice line); the composer shows a chip saying what will go along.
 */

export type ScreenContext = {
  /** Short, for the person: what the chip and the notice above the message show ("「Gerdr 正式发布…」"). */
  title: string;
  /** For the agent: what it is and where (ids, folders, which tab). Plain lines. */
  text?: string;
};

type PanelScreen = { page: ScreenContext[]; file?: string; selection?: string };

const screens = new Map<string, PanelScreen>();
const skipped = new Map<string, string>(); // session → the block the person dismissed (the chip's ×)
const lastSent = new Map<string, string>(); // session → the last block it got
const listeners = new Set<() => void>();
let version = 0;
const emit = () => {
  version++;
  for (const f of listeners) f();
};
const subscribe = (f: () => void) => (listeners.add(f), () => void listeners.delete(f));
const of = (key: string) => {
  let s = screens.get(key);
  if (!s) screens.set(key, (s = { page: [] }));
  return s;
};

/**
 * The page says what's on screen (null: nothing in particular). The latest mounted caller wins, so a detail page
 * overrides the list behind it; it's dropped on unmount. Outside a panel it does nothing.
 */
export function useScreenContext(ctx: ScreenContext | null) {
  const key = useContext(PanelKeyContext);
  const json = ctx ? JSON.stringify(ctx) : "";
  useEffect(() => {
    if (!key || !json) return;
    const entry = JSON.parse(json) as ScreenContext;
    of(key).page.push(entry);
    emit();
    return () => {
      const s = of(key);
      s.page = s.page.filter((e) => e !== entry);
      emit();
    };
  }, [key, json]);
}

/** Folder.tsx: the file open in a panel's folder view (null: none / a folder). */
export function setScreenFile(key: string, path: string | null) {
  const s = of(key);
  if ((s.file ?? null) === path) return;
  s.file = path ?? undefined;
  emit();
}

const MAX_SELECTION = 4000;

// The selection inside a panel's page, kept after focus moves to the composer (which collapses it): cleared only by a
// new collapsed selection inside that page (a click elsewhere in it), replaced by a new one.
let tracking = false;
function track() {
  if (tracking) return;
  tracking = true;
  document.addEventListener("selectionchange", () => {
    const sel = document.getSelection();
    const node = sel?.anchorNode;
    const el = node && (node.nodeType === 1 ? (node as Element) : node.parentElement);
    const mount = el?.closest("[data-gerdr-mount]")?.getAttribute("data-gerdr-mount");
    if (!mount) return;
    const key = [...screens.keys()].find((k) => mountName(k) === mount);
    if (!key) return;
    const text = sel && !sel.isCollapsed ? sel.toString().trim() : "";
    const s = of(key);
    if ((s.selection ?? "") === text) return;
    s.selection = text || undefined;
    emit();
  });
}

/** Which shown panel a conversation belongs to: one it started (tags.panel), or the one whose agent column shows it. */
function panelOf(session: string): string | undefined {
  const tag = getApp().sessions[session]?.tags?.panel;
  for (const key of screens.keys()) {
    if (!isPanelShown(key)) continue;
    if (tag === key || workSessionOf(key) === session) return key;
  }
  return undefined;
}

const attr = (s: string) =>
  s
    .replace(/[\n\r]+/g, " ")
    .replace(/"/g, "'")
    .slice(0, 120);
const base = (p: string) => p.slice(p.lastIndexOf("/") + 1);

/** The block for what `session`'s panel shows now ("" when nothing), and its one-line summary. */
function build(session: string): { block: string; summary: string } {
  const key = panelOf(session);
  const s = key && screens.get(key);
  if (!s) return { block: "", summary: "" };
  const page = s.page[s.page.length - 1];
  if (!page && !s.file && !s.selection) return { block: "", summary: "" };
  const sel = s.selection && s.selection.length > MAX_SELECTION ? s.selection.slice(0, MAX_SELECTION) + "…" : s.selection;
  const summary = [page?.title, s.file && base(s.file), sel && t("screen.selected", { n: s.selection!.length })].filter(Boolean).join(" · ");
  const lines = [
    `<gerdr-screen summary="${attr(summary)}">`,
    'What the user had on screen in this panel when sending the message below. It is what they mean by "this", "here", "this file", "this paragraph":',
    ...(page ? [`- Page: ${page.title}`, ...(page.text ? page.text.split("\n").map((l) => `  ${l}`) : [])] : []),
    ...(s.file ? [`- File open in the folder view: ${s.file}`] : []),
    ...(sel ? ["- Text they selected:", "```", sel, "```"] : []),
    "</gerdr-screen>",
  ];
  return { block: lines.join("\n"), summary };
}

/** chatHost's send: the block to put in front of this message, if what's on screen changed since the last one. */
export function screenBlockFor(session: string): string {
  const { block } = build(session);
  if (!block || lastSent.get(session) === block || skipped.get(session) === block) return "";
  lastSent.set(session, block);
  emit(); // the chip goes: it's been sent
  return block;
}

/** The composer chip: what goes along with the next message to `session` ("" = nothing), and × to leave it out. */
export function useScreenChip(session: string): { summary: string; skip: () => void } {
  track();
  useSyncExternalStore(subscribe, () => version);
  // Which panel shows which conversation can change too
  useStore((st) => st.config.workSessions);
  useStore((st) => st.sessions[session]?.tags?.panel);
  const { block, summary } = build(session);
  const on = !!block && lastSent.get(session) !== block && skipped.get(session) !== block;
  return {
    summary: on ? summary : "",
    skip: () => {
      skipped.set(session, block);
      emit();
    },
  };
}
