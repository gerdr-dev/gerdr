// g.messageAction: add-ons' buttons on a message (drawn by components/chat/MessageActions.tsx through
// ChatHost.messageActions). Gerdr owns where they sit and how a run goes; what a run does is the add-on's (read it aloud
// with g.audio, translate it, send it somewhere). So a new thing to do with a message is a new add-on, never a new
// primitive here. A run:
// - spinner while run() is pending; a second tap aborts ctx.signal (the add-on stops) and the button is free again;
// - when the run plays sound with g.audio.play(…, { signal: ctx.signal }), the button shows "stop" while it plays;
// - a throw is a toast naming the action.
import { useSyncExternalStore } from "react";
import { toast } from "sonner";
import { fail, messageActions, type MessageAction, type MessageCtx } from "./addons";
import { unlockAudio, watchSignal } from "./audio";
import { locale, t } from "./i18n";

/** What a run's button shows: working (spinner) or playing sound (stop). */
export type RunPhase = "busy" | "playing";

const runs = new Map<string, { phase: RunPhase; ctrl: AbortController }>();
const subs = new Set<() => void>();
const changed = () => subs.forEach((f) => f());
const subscribe = (f: () => void) => (subs.add(f), () => void subs.delete(f));

/** The message a run is about: what an add-on's run() / when() get as `msg`. */
export type Msg = { sessionId: string; role: "user" | "assistant"; id: string; readonly markdown: string; readonly text: string };

/** A Msg whose markdown / text are worked out on first read: when() runs for every message on every render. */
export function lazyMsg(m: { sessionId: string; role: "user" | "assistant"; id: string; text(): string }): Msg {
  let md: string | undefined;
  let plain: string | undefined;
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
  };
}

export const runKey = (msg: { sessionId: string; id: string }, a: MessageAction) => `${msg.sessionId}:${msg.id}:${a.addon}:${a.id}`;

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

/** A tap on an action: start it, or stop it when it runs. Call from the tap itself (audio unlocks inside it). */
export function toggleRun(a: MessageAction, msg: Msg) {
  const key = runKey(msg, a);
  const on = runs.get(key);
  if (on) {
    on.ctrl.abort();
    runs.delete(key);
    return changed();
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
  const ctx: MessageCtx = { signal: ctrl.signal, locale: locale() };
  Promise.resolve()
    .then(() => a.run(msg, ctx))
    .catch((e) => {
      if (ctrl.signal.aborted) return;
      fail(a.addon, e);
      toast.error(t("messageActions.failed", { action: a.title, error: e instanceof Error ? e.message : String(e) }));
    })
    .finally(() => {
      if (runs.get(key) !== run) return;
      runs.delete(key);
      changed();
    });
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
