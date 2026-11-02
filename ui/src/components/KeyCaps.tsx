import { comboCaps, formatCombo, type KeyAction } from "@/lib/keys";
import { get, keyFor, useStore, type Config } from "@/lib/store";
import { useHwKeyboard } from "@/lib/hwKeyboard";
import { cn } from "@/lib/utils";

/**
 * A shortcut drawn as keycaps (⌘ D), for the right edge of a menu item. Each key is its own little cap; the two presses
 * of a prefix binding (⌃B then V) sit a bit further apart.
 */
export function KeyCaps({ combo, className }: { combo: string; className?: string }) {
  const prefix = useStore((s) => s.config.prefix);
  return (
    <span className={cn("ml-auto flex shrink-0 items-center gap-1.5 pl-4", className)}>
      {comboCaps(combo, prefix).map((press, i) => (
        <span key={i} className="flex items-center gap-0.5">
          {press.map((k, j) => (
            <kbd
              key={j}
              className="inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-[4px] border border-b-2 bg-background px-1 font-sans text-[10.5px] leading-none font-medium text-muted-foreground"
            >
              {k}
            </kbd>
          ))}
        </span>
      ))}
    </span>
  );
}

/**
 * The key of an action as the user has it set (Settings → Shortcuts), one only: the shortest as drawn. Off the mac
 * modifiers are words, so "Ctrl B C" is shorter than "Ctrl Shift T" though both are three keys (counting keys picked the
 * latter, reported). On a tie a single press (⇧⌘D) beats a prefix one (⌃B V). undefined when the action is unbound.
 */
export function shortestKey(cfg: Config, action: KeyAction): string | undefined {
  const cost = (k: string) => {
    const presses = comboCaps(k, cfg.prefix);
    // Width: the text plus each cap's padding (about two characters); presses only break ties
    const width = presses.flat().reduce((n, c) => n + [...c].length + 2, 0);
    return width * 10 + presses.length;
  };
  return [...keyFor(cfg, action)].sort((a, b) => cost(a) - cost(b))[0];
}

/** An action's key in plain text, for a tooltip: "还原（⌃B Z）". Empty when unbound. */
export function keyText(action: KeyAction): string {
  const k = shortestKey(get().config, action);
  return k ? formatCombo(k) : "";
}

/**
 * An action's key as keycaps (shortestKey), for the right edge of a menu item. Nothing when the action is unbound, or
 * on a touch device with no hardware keyboard to press it on (lib/hwKeyboard.ts): there it only takes room.
 */
export function ActionKeys({ action, className }: { action: KeyAction; className?: string }) {
  // Select the config, not keyFor's result: off the mac that's a fresh array on every call, which would re-render forever
  const k = shortestKey(
    useStore((s) => s.config),
    action,
  );
  const kb = useHwKeyboard();
  return k && kb ? <KeyCaps combo={k} className={className} /> : null;
}
