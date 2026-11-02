import { Component, type ReactNode } from "react";
import { composerActions, fail, slots, type SlotName, type SlotReg } from "@/lib/addons";
import { get, set, useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";
import type { LoaderProps } from "@gerdr/chat";

// Where add-ons draw (g.slot, g.composerAction; lib/addons.ts). Each add-on's component sits in its own error
// boundary: one that throws while rendering is dropped and its error shown in Settings → Add-ons, the rest draw on.
export function AddonSlot({ name, props, className }: { name: SlotName; props?: Record<string, unknown>; className?: string }) {
  useStore((s) => s.addonVersion);
  const list = slots[name];
  if (!list.length) return null;
  const items = list.map(({ key, addon, Component: C }) => (
    <Guard key={key} addon={addon}>
      <C {...props} />
    </Guard>
  ));
  return className ? <div className={className}>{items}</div> : <>{items}</>;
}

/** Some entries of a slot, each in its own error boundary like AddonSlot's: a rail group's blocks, a face (lib/rail.ts). */
export function AddonParts({ regs }: { regs: SlotReg[] }) {
  return (
    <>
      {regs.map(({ key, addon, Component: C }) => (
        <Guard key={key} addon={addon}>
          <C />
        </Guard>
      ))}
    </>
  );
}

// The working indicator (ChatHost.renderLoader): the first "loader" add-on draws it, else the chat's own dots. One
// that throws falls back to the dots rather than to nothing, so a running turn still shows it runs.
export function AddonLoader({ paused, className, fallback }: LoaderProps) {
  useStore((s) => s.addonVersion);
  const first = slots.loader[0];
  if (!first) return <>{fallback}</>;
  const C = first.Component;
  return (
    <Guard key={first.key} addon={first.addon} fallback={fallback}>
      <C paused={paused} className={className} fallback={fallback} />
    </Guard>
  );
}

class Guard extends Component<{ addon: string; children: ReactNode; fallback?: ReactNode }, { failed: boolean }> {
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
