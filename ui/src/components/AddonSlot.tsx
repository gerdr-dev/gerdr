import { Component, type ReactNode } from "react";
import { composerActions, fail, slots, type SlotName } from "@/lib/addons";
import { get, set, useStore } from "@/lib/store";
import { Button } from "@/components/ui/button";

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

class Guard extends Component<{ addon: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  componentDidCatch(e: unknown) {
    fail(this.props.addon, e);
  }
  render() {
    return this.state.failed ? null : this.props.children;
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
