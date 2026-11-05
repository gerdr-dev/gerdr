import { Puzzle } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { assetURL, slots, type SlotReg } from "@/lib/addons";
import { addonMeta, lucideIcon, railButtons, type RailButton } from "@/lib/rail";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { AddonGuard, AddonParts } from "./AddonSlot";

/**
 * An add-on button's icon (lib/rail.ts): what the add-on declared — a component drawn in a box, or a lucide icon name —
 * else its addon.json icon, else a puzzle piece. A component gets a box 32×20 (28×16 small), clipped: room for a few
 * small marks side by side, but it can't push the rail around. An add-on that asked for a tall icon gets a button two
 * cells tall on the rail (two 36px buttons and the 4px gap between them) and a 32×72 box in it, for marks that need room
 * (three quota rings, stacked); the phone's bottom row has no height to give, so there it's 24×24. `small`: the phone
 * drawer's bottom row.
 */
function ButtonIcon({ b, small }: { b: RailButton; small?: boolean }) {
  if (typeof b.icon === "function" || (typeof b.icon === "object" && b.icon)) {
    const C = b.icon;
    const box = b.tall ? (small ? TALL_SMALL : TALL) : small ? ICON_SMALL : ICON;
    return (
      <span aria-hidden className="flex shrink-0 items-center justify-center overflow-hidden" style={box}>
        <AddonGuard addon={b.addon} fallback={<Puzzle className="size-4" />}>
          <C {...box} />
        </AddonGuard>
      </span>
    );
  }
  const Icon = lucideIcon(typeof b.icon === "string" ? b.icon : undefined);
  if (Icon) return <Icon className={small ? "size-3.5" : "size-4"} />;
  const meta = addonMeta(b.addon);
  if (meta?.icon) return <img alt="" src={assetURL(meta.id, meta.icon, meta.hash)} className={cn("rounded-[4px]", small ? "size-4" : "size-[18px]")} />;
  return <Puzzle className={small ? "size-3.5" : "size-4"} />;
}

const ICON = { width: 32, height: 20 };
const ICON_SMALL = { width: 28, height: 16 };
const TALL = { width: 32, height: 72 };
const TALL_SMALL = { width: 24, height: 24 };

/**
 * The rail's add-on buttons: one per add-on with a "panel" block (Gerdr draws the button and opens the blocks), and
 * every "railButton" entry, which is the whole button drawn by its add-on — its look, its state, what a click or a
 * hover does, any popup. Gerdr only gives it its place in the column, in slot order among the others. Asked for
 * (Nov 2026) by a lid-awake switch: the "panel" button can only open a panel, and the add-on's icon box (32×20) is
 * too small to show the button's own on / off state, so its "on" background read as a pill.
 */
export function RailAddonButtons() {
  useStore((s) => s.addonVersion);
  return (
    <>
      {railEntries().map((e) =>
        "b" in e ? (
          <AddonButton key={e.b.addon} b={e.b} />
        ) : (
          <AddonGuard key={e.r.key} addon={e.r.addon} fallback={null}>
            <e.r.Component place="rail" className={RAIL_BTN} />
          </AddonGuard>
        ),
      )}
    </>
  );
}

/** Gerdr's own look for a rail button, handed to "railButton" entries as className so they can match it. */
const RAIL_BTN = "flex size-9 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground";
const PHONE_BTN = "inline-flex h-7 min-w-7 items-center justify-center rounded-md px-1 text-muted-foreground hover:bg-accent hover:text-foreground";

/** Panel buttons and railButton entries in one order: by the entry's order (a panel button: its smallest block's). */
function railEntries(): ({ b: RailButton; order: number; addon: string } | { r: SlotReg; order: number; addon: string })[] {
  const panel = railButtons().map((b) => ({ b, order: Math.min(...b.blocks.map((x) => x.order)), addon: b.addon }));
  const own = slots.railButton.map((r) => ({ r, order: r.order, addon: r.addon }));
  return [...panel, ...own].sort((x, y) => x.order - y.order || x.addon.localeCompare(y.addon));
}

/**
 * One add-on's button: hovering it (mouse; or clicking, if the add-on asked for that) or tapping it opens its blocks
 * beside the rail. The panel stays mounted while closed, as the blocks did in the old sidebar, so they keep their state
 * and timers instead of reloading on every open.
 */
function AddonButton({ b }: { b: RailButton }) {
  const [open, setOpen] = useState(false);
  const btn = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const timer = useRef(0);
  const [pos, setPos] = useState({ left: 0, bottom: 0 });
  const onHover = b.open !== "click";
  // Mouse only: a tap on a touch screen sends pointerenter too, which would open it and the click would close it again.
  // An add-on that opens on click (a panel you work in): hovering does nothing, it closes on a click elsewhere or Escape
  const hover = (v: boolean) => (e: React.PointerEvent) => {
    if (e.pointerType !== "mouse" || !onHover) return;
    clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setOpen(v), v ? 250 : 200);
  };
  useLayoutEffect(() => {
    if (!open || !btn.current) return;
    const r = btn.current.getBoundingClientRect();
    setPos({ left: r.right + 8, bottom: Math.max(8, window.innerHeight - r.bottom) });
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const down = (e: PointerEvent) => {
      const t = e.target as Node;
      if (!panel.current?.contains(t) && !btn.current?.contains(t)) setOpen(false);
    };
    const key = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("pointerdown", down, true);
    document.addEventListener("keydown", key);
    return () => (document.removeEventListener("pointerdown", down, true), document.removeEventListener("keydown", key));
  }, [open]);
  useEffect(() => () => clearTimeout(timer.current), []);
  return (
    <>
      <button
        ref={btn}
        aria-label={b.title}
        aria-expanded={open}
        title={b.title}
        className={cn(
          "flex min-h-9 w-9 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground",
          // Two cells of the rail: two size-9 buttons and their gap-1 (Rail.tsx)
          b.tall && "h-[76px]",
          open && "bg-accent text-foreground",
        )}
        onPointerEnter={hover(true)}
        onPointerLeave={hover(false)}
        onClick={() => (clearTimeout(timer.current), setOpen(!open))}
      >
        <ButtonIcon b={b} />
      </button>
      {createPortal(
        // The vertical padding is given once here, not by each block: two blocks each padding themselves left a wide gap.
        // py-1.5 matches the px-1.5 blocks give themselves (GUIDE.md), so a block's hover fill sits evenly in the panel
        <div
          ref={panel}
          className={cn("fixed z-50 w-72 rounded-lg border bg-popover py-1.5 text-popover-foreground shadow-md", !open && "hidden")}
          style={pos}
          onPointerEnter={hover(true)}
          onPointerLeave={hover(false)}
        >
          <AddonParts regs={b.blocks} />
        </div>,
        document.body,
      )}
    </>
  );
}

/**
 * The phone drawer's add-on buttons, the same as the rail's: a tap opens the add-on's blocks in a sheet from the
 * bottom. Only mounted while the drawer is open, so there's nothing to keep alive while closed.
 */
export function PhoneAddonButtons() {
  useStore((s) => s.addonVersion);
  return (
    <>
      {railEntries().map((e) => {
        if (!("b" in e))
          return (
            <AddonGuard key={e.r.key} addon={e.r.addon} fallback={null}>
              <e.r.Component place="phone" className={PHONE_BTN} />
            </AddonGuard>
          );
        const b = e.b;
        return (
          <Sheet key={b.addon}>
            <SheetTrigger asChild>
              <Button variant="ghost" className="h-7 min-w-7 px-1 text-muted-foreground" aria-label={b.title}>
                <ButtonIcon b={b} small />
              </Button>
            </SheetTrigger>
            {/* No auto-focus: it put a focus ring on the close button of a sheet opened by a tap */}
            <SheetContent side="bottom" className="gap-0 rounded-t-xl pb-[max(0.5rem,env(safe-area-inset-bottom))]" onOpenAutoFocus={(e) => e.preventDefault()}>
              <SheetTitle className="px-4 pt-4 pb-1 text-[13px]">{b.title}</SheetTitle>
              <div className="py-1">
                <AddonParts regs={b.blocks} />
              </div>
            </SheetContent>
          </Sheet>
        );
      })}
    </>
  );
}
