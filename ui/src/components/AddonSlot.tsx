import { Component, createContext, useContext, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { composerActions, fail, slots, type SlotName, type SlotReg } from "@/lib/addons";
import { get, set, useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import type { LoaderProps } from "@gerdr/chat";
import type { LoaderChoice } from "@/lib/store/config";
import { cn } from "@/lib/utils";
import { BUILTIN_LOADERS, builtinLoader } from "./Loaders";

// Where add-ons draw (g.slot, g.composerAction; lib/addons.ts). Each add-on's component sits in its own error
// boundary: one that throws while rendering is dropped and its error shown in Settings → Add-ons, the rest draw on.
export function AddonSlot({ name, props, className }: { name: SlotName; props?: Record<string, unknown>; className?: string }) {
  useStore((s) => s.addonVersion);
  const list = slots[name];
  if (!list.length) return null;
  const items = list.map(({ key, addon, Component: C }) => (
    <AddonGuard key={key} addon={addon}>
      <C {...props} />
    </AddonGuard>
  ));
  return className ? <div className={className}>{items}</div> : <>{items}</>;
}

/** Some entries of a slot, each in its own error boundary like AddonSlot's: an add-on's rail panel (lib/rail.ts). */
export function AddonParts({ regs }: { regs: SlotReg[] }) {
  return (
    <>
      {regs.map(({ key, addon, Component: C }) => (
        <AddonGuard key={key} addon={addon}>
          <C />
        </AddonGuard>
      ))}
    </>
  );
}

export type SpriteState = "idle" | "working" | "waiting";

/**
 * One sprite (g.slot("sprite")) in a box of width × height px, centred. The sprite is told the box so it can pick its
 * scale; `clip` for places that must not be pushed around (an agent's icon in a list), off where a hat may stick out (the loader).
 * className goes on the box only (the loader's text-brand / text-warn): the sprite inherits it as currentColor, and a
 * size class in it ("size-4") loses to the box's own width / height.
 */
export function SpriteView({
  reg,
  state,
  width,
  height,
  className,
  clip,
  fallback,
}: {
  reg: SlotReg;
  state: SpriteState;
  width: number;
  height: number;
  className?: string;
  clip?: boolean;
  fallback?: ReactNode;
}) {
  const C = reg.Component;
  return (
    <span aria-hidden className={cn("flex shrink-0 items-center justify-center", clip && "overflow-hidden", className)} style={{ width, height }}>
      <AddonGuard key={reg.key} addon={reg.addon} fallback={fallback}>
        <C state={state} width={width} height={height} />
      </AddonGuard>
    </span>
  );
}

/**
 * A sprite filling a box sized by classes ("size-4"), not numbers: the box is measured, then the sprite is told it.
 * For places that take a sprite in place of an icon (an agent's icon, components/AgentIcon.tsx), drawn still.
 */
export function FitSprite({ reg, className, state = "idle" }: { reg: SlotReg; className?: string; state?: "idle" | "working" | "waiting" }) {
  const ref = useRef<HTMLSpanElement>(null);
  const [box, setBox] = useState<{ width: number; height: number }>();
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const measure = () => {
      const width = el.clientWidth;
      const height = el.clientHeight;
      setBox((b) => (b && b.width === width && b.height === height ? b : { width, height }));
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return (
    <span ref={ref} aria-hidden className={cn("inline-flex shrink-0 items-center justify-center overflow-hidden", className)}>
      {box && box.width > 0 && <SpriteView reg={reg} state={state} clip {...box} />}
    </span>
  );
}

/** The working indicator's box for a sprite: the dots' row is 20px tall, a sprite gets most of it. */
export const LOADER_BOX = { width: 24, height: 16 };

/** What config.loader resolves to now: a built-in, an add-on's loader, a sprite, or nothing (gerdr's dots). */
export function loaderPick(
  choice: LoaderChoice | undefined,
): { builtin: (typeof BUILTIN_LOADERS)[number] } | { loader: SlotReg } | { sprite: SlotReg } | undefined {
  if (choice === "dots") return undefined;
  if (choice?.startsWith("builtin:")) {
    const b = builtinLoader(choice.slice(8));
    return b && { builtin: b };
  }
  if (choice?.startsWith("sprite:")) {
    const r = slots.sprite.find((x) => x.key === choice.slice(7));
    return r && { sprite: r };
  }
  if (choice?.startsWith("loader:")) {
    const r = slots.loader.find((x) => x.key === choice.slice(7));
    return r && { loader: r };
  }
  // Not chosen: a "loader" add-on draws as soon as it's installed, that's all it's for; sprites only when picked
  return slots.loader[0] && { loader: slots.loader[0] };
}

/** The config.loader value of a pick (what the settings show as chosen). */
export function pickChoice(pick: ReturnType<typeof loaderPick>): LoaderChoice {
  if (!pick) return "dots";
  if ("builtin" in pick) return `builtin:${pick.builtin.id}`;
  return "sprite" in pick ? `sprite:${pick.sprite.key}` : `loader:${pick.loader.key}`;
}

/** Draws the working indicators below with this choice instead of config.loader: the previews in Settings → General (LoaderSelect.tsx). */
export const LoaderPreview = createContext<LoaderChoice | undefined>(undefined);

// The working indicator (ChatHost.renderLoader): what the user picked (a built-in, an add-on's, a sprite) in Settings → General → Loading 动画 (config.loader), else
// the first "loader" add-on, else the chat's own dots. A pick whose add-on is gone, or one that throws, falls back to
// the dots rather than to nothing, so a running turn still shows it runs.
export function AddonLoader({ paused, className, fallback }: LoaderProps) {
  useStore((s) => s.addonVersion);
  const preview = useContext(LoaderPreview);
  const chosen = useStore((s) => s.config.loader);
  const pick = loaderPick(preview ?? chosen);
  if (!pick) return <>{fallback}</>;
  if ("builtin" in pick) {
    const B = pick.builtin.C;
    return <B paused={paused} className={className} />;
  }
  if ("sprite" in pick)
    return <SpriteView reg={pick.sprite} state={paused ? "waiting" : "working"} className={className} fallback={fallback} {...LOADER_BOX} />;
  const C = pick.loader.Component;
  return (
    <AddonGuard key={pick.loader.key} addon={pick.loader.addon} fallback={fallback}>
      <C paused={paused} className={className} fallback={fallback} />
    </AddonGuard>
  );
}

/** An add-on's component that throws is dropped (or shows `fallback`) and its error kept for Settings → Add-ons. */
export class AddonGuard extends Component<{ addon: string; children: ReactNode; fallback?: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  componentDidCatch(e: unknown) {
    fail(this.props.addon, e);
  }
  render() {
    return this.state.failed ? (this.props.fallback ?? null) : this.props.children;
  }
}

// The composer's bottom bar: one small button per g.composerAction.
export function AddonComposerActions({ sessionId, paneId }: { sessionId: string; paneId: string }) {
  useStore((s) => s.addonVersion);
  if (!composerActions.length) return null;
  const ctx = {
    sessionId,
    get text() {
      return get().drafts[paneId] ?? "";
    },
    // Drafts are kept per pane (what is typed in this composer), not per session
    setText: (t: string) => set((s) => ({ drafts: { ...s.drafts, [paneId]: t } })),
    insert: (t: string) => set((s) => ({ drafts: { ...s.drafts, [paneId]: (s.drafts[paneId] ?? "") + t } })),
  };
  return (
    <>
      {composerActions.map((a) => (
        // title, not a Tooltip: Radix's opens on focus too and stays over whatever the button opened (a popover)
        <Button
          key={a.addon + a.id}
          variant="ghost"
          size="sm"
          title={a.title}
          className="h-7 shrink-0 gap-1 px-1.5 text-[12px] text-muted-foreground"
          onClick={() => Promise.resolve(a.run(ctx)).catch((e) => fail(a.addon, e))}
        >
          {a.icon ?? a.title}
        </Button>
      ))}
    </>
  );
}
