// g.messageAction: add-ons' buttons on a message (drawn by components/chat/MessageActions.tsx through
// ChatHost.messageActions). Gerdr owns where they sit and how a run goes; what a run does is the add-on's (read it aloud
// with g.audio, translate it, send it somewhere). So a new thing to do with a message is a new add-on, never a new
// primitive here. A run:
// - spinner while run() is pending; a second tap aborts ctx.signal (the add-on stops) and the button is free again;
// - when the run plays sound with g.audio.play(…, { signal: ctx.signal }), the button shows "stop" while it plays;
// - a throw is a toast naming the action (not an add-on error in Settings: the next run may well work);
// - ctx.highlight(text) marks that stretch of the message on screen (lib/readingHighlight.ts) until the run ends, and
//   ctx.onPick(stretches, pick) makes those stretches clickable meanwhile (jump reading to the paragraph clicked).
import { useEffect, useSyncExternalStore } from "react";
import { toast } from "sonner";
import { fail, messageActions, type MessageAction, type MessageCtx } from "./addons";
import { unlockAudio, watchSignal } from "./audio";
import { locale, t } from "./i18n";
import { endHighlight, highlightMessage, pickableMessage } from "./readingHighlight";

/** What a run's button shows: working (spinner) or playing sound (stop). */
export type RunPhase = "busy" | "playing";

const runs = new Map<string, { phase: RunPhase; ctrl: AbortController }>();
const subs = new Set<() => void>();
const changed = () => subs.forEach((f) => f());
const subscribe = (f: () => void) => (subs.add(f), () => void subs.delete(f));

/**
 * The message a run is about: what an add-on's run() / when() get as `msg`. final: a reply's answer, its last stretch of
 * text (after its last tool call), without what it said along the way; a user's message is all final.
 */
export type Msg = {
  sessionId: string;
  role: "user" | "assistant";
  id: string;
  readonly markdown: string;
  readonly text: string;
  readonly final: { readonly markdown: string; readonly text: string };
};

/** A Msg whose markdown / text are worked out on first read: when() runs for every message on every render. */
export function lazyMsg(m: { sessionId: string; role: "user" | "assistant"; id: string; text(): string; blocks(): string[] }): Msg {
  let md: string | undefined;
  let plain: string | undefined;
  let finalMd: string | undefined;
  let finalPlain: string | undefined;
  return {
    sessionId: m.sessionId,
    role: m.role,
    id: m.id,
    get markdown() {
      return (md ??= m.text());
    },
    get text() {
      return (plain ??= plainText(this.markdown));
    },
    final: {
      get markdown() {
        return (finalMd ??= m.blocks().at(-1) ?? "");
      },
      get text() {
        return (finalPlain ??= plainText(this.markdown));
      },
    },
  };
}

export const runKey = (msg: { sessionId: string; id: string }, a: MessageAction) => `${msg.sessionId}:${msg.id}:${a.addon}:${a.id}`;

// The messages drawn now (their action buttons mounted), so an add-on can run its own action on one without the tap:
// 🎧 "read the latest reply" in the voice add-on, which then marks the paragraph being read like the message's own
// speaker does. Only what's on screen: a reply scrolled out of the virtual list has no rows to mark anyway.
const shown = new Map<string, Msg>();

/** Keep a drawn message findable while its buttons are mounted (MessageActions). */
export function useShown(msg: Msg) {
  const key = `${msg.sessionId}:${msg.id}`;
  useEffect(() => {
    shown.set(key, msg);
    return () => void shown.delete(key);
  }, [key, msg]);
}

/**
 * Run an add-on's own message action on a conversation's latest reply on screen, as a tap would (a second call
 * stops it). The latest is the reply drawn lowest on the page ([data-message] rows). Resolves true once the run ends;
 * false at once when there's none on screen, or no such action for it (when() said no).
 */
export async function runOnLatestReply(addon: string, actionId: string, sessionId: string): Promise<boolean> {
  let best: { msg: Msg; el: Element } | null = null;
  for (const msg of shown.values()) {
    if (msg.sessionId !== sessionId || msg.role !== "assistant") continue;
    const els = document.querySelectorAll(`[data-message="${CSS.escape(msg.id)}"]`);
    const el = els[els.length - 1];
    if (el && (!best || best.el.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING)) best = { msg, el };
  }
  if (!best) return false;
  const a = actionsFor(best.msg).find((x) => x.addon === addon && x.id === actionId);
  if (!a) return false;
  await toggleRun(a, best.msg);
  return true;
}

export function useRunPhase(key: string): RunPhase | undefined {
  return useSyncExternalStore(subscribe, () => runs.get(key)?.phase);
}

/** Is anything of this message running (its buttons stay in sight then). */
export function useAnyRun(prefix: string): boolean {
  return useSyncExternalStore(subscribe, () => [...runs.keys()].some((k) => k.startsWith(prefix)));
}

/** The actions that apply to a message, in order (when() defaults to: it says something). */
export function actionsFor(msg: Msg): MessageAction[] {
  return messageActions.filter((a) => {
    try {
      return a.when ? a.when(msg) : !!msg.markdown.trim();
    } catch (e) {
      fail(a.addon, e);
      return false;
    }
  });
}

/**
 * A tap on an action: start it, or stop it when it runs. Call from the tap itself (audio unlocks inside it). Resolves
 * when the run it started ends (finished, failed or stopped); at once when it stopped one.
 */
export function toggleRun(a: MessageAction, msg: Msg): Promise<void> {
  const key = runKey(msg, a);
  const on = runs.get(key);
  if (on) {
    on.ctrl.abort();
    runs.delete(key);
    changed();
    return Promise.resolve();
  }
  unlockAudio();
  const ctrl = new AbortController();
  const run = { phase: "busy" as RunPhase, ctrl };
  runs.set(key, run);
  watchSignal(ctrl.signal, (playing) => {
    if (runs.get(key) !== run) return;
    run.phase = playing ? "playing" : "busy";
    changed();
  });
  changed();
  const ctx: MessageCtx = {
    signal: ctrl.signal,
    locale: locale(),
    // Owned by this run: when it ends its mark goes, and only its own (a newer run may have marked since)
    highlight: (text) => !ctrl.signal.aborted && highlightMessage(msg.id, text, run),
    onPick: (stretches, pick) => !ctrl.signal.aborted && pickableMessage(msg.id, stretches, pick, run),
  };
  return Promise.resolve()
    .then(() => a.run(msg, ctx))
    .catch((e) => {
      if (ctrl.signal.aborted) return;
      // A run's failure is that run's (no key yet, the service down), told once in the toast. Not fail(): that marks
      // the add-on itself broken in Settings ("加载出错") until it reloads, long after the cause is fixed
      console.error(`[gerdr addon ${a.addon}] ${a.title}`, e);
      toast.error(t("messageActions.failed", { action: a.title, error: e instanceof Error ? e.message : String(e) }));
    })
    .finally(() => {
      endHighlight(run);
      if (runs.get(key) !== run) return;
      runs.delete(key);
      changed();
    })
    .then(() => {});
}

/**
 * Markdown as it reads in words: the marks gone, code blocks shortened to "(code)" (read out or translated character
 * by character they are noise, and a message's point is in the prose around them), links as their label, images and
 * HTML tags dropped. What msg.text is; msg.markdown keeps the original.
 */
export function plainText(md: string): string {
  return md
    .replace(/```[\s\S]*?(```|$)/g, ` ${t("messageActions.code")} `)
    .replace(/!\[[^\]]*\]\([^)]*\)/g, "")
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")
    .replace(/<[^>\n]+>/g, "")
    .replace(/`([^`]*)`/g, "$1")
    .replace(/^\s{0,3}(#{1,6}\s+|>\s?|[-*+]\s+(\[[ xX]\]\s+)?|\d+[.)]\s+)/gm, "")
    .replace(/^\s*\|?\s*:?-{3,}.*$/gm, "")
    .replace(/\s*\|\s*/g, " ")
    .replace(/(\*\*|__|~~)(.+?)\1/g, "$2")
    .replace(/(^|\W)[*_](\S[^*_]*?)[*_](?=\W|$)/g, "$1$2")
    .replace(/^\s*([-*_]\s*){3,}$/gm, "")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}
