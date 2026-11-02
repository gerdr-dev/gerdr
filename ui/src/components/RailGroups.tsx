import { Gauge } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { slots, type SlotReg } from "@/lib/addons";
import { autoFace, lucideIcon, resolveGroups, type ResolvedGroup } from "@/lib/rail";
import type { RailFace } from "@/lib/store/config";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { AgentGlyph } from "./AgentGlyph";
import { AddonParts } from "./AddonSlot";

/**
 * A button's face (lib/rail.ts): a built-in icon, typed characters, or what an add-on draws ("rail" slot). An add-on's
 * face gets a box 36px wide and at most 28px tall, clipped: it can draw anything, but can't push the rail around.
 * `blocks`: the group's, for the automatic face when none was picked (an add-on face of one of them, else the first
 * block's declared icon, else a gauge). `small`: the phone drawer's bottom row, a size down.
 */
export function FaceView({ face, blocks, small }: { face?: RailFace; blocks: SlotReg[]; small?: boolean }) {
  const f = face ?? autoFace(blocks);
  if (f && "text" in f && f.text) return <span className={cn("leading-none font-semibold", small ? "text-[13px]" : "text-[15px]")}>{f.text}</span>;
  if (f && "glyph" in f) return <AgentGlyph name={f.glyph} color={f.color} className={small ? "size-4" : "size-[18px]"} />;
  if (f && "addon" in f) {
    const reg = slots.rail.find((r) => r.key === f.addon);
    if (reg)
      return (
        <span className={cn("flex items-center justify-center overflow-hidden", small ? "max-h-6 w-8" : "max-h-7 w-9")}>
          <AddonParts regs={[reg]} />
        </span>
      );
  }
  const Icon = lucideIcon(blocks[0]?.icon) ?? Gauge;
  return <Icon className={small ? "size-3.5" : "size-4"} />;
}

/** The rail's add-on buttons, one per group that has a loaded block. */
export function RailGroupButtons() {
  useStore((s) => s.addonVersion);
  useStore((s) => s.config.rail);
  return (
    <>
      {resolveGroups()
        .filter((g) => g.blocks.length)
        .map((g) => (
          <GroupButton key={g.group.id} g={g} />
        ))}
    </>
  );
}

/**
 * One group's button: hovering it (mouse) or tapping it opens its blocks beside the rail. The panel stays mounted while
 * closed, as the blocks did in the old sidebar, so they keep their state and timers instead of reloading on every open.
 */
function GroupButton({ g }: { g: ResolvedGroup }) {
  const [open, setOpen] = useState(false);
  const btn = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const timer = useRef(0);
  const [pos, setPos] = useState({ left: 0, bottom: 0 });
  // Mouse only: a tap on a touch screen sends pointerenter too, which would open it and the click would close it again
  const hover = (v: boolean) => (e: React.PointerEvent) => {
    if (e.pointerType !== "mouse") return;
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
        aria-label={g.group.name}
        aria-expanded={open}
        title={g.group.name}
        className={cn(
          "flex min-h-9 w-9 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground",
          open && "bg-accent text-foreground",
        )}
        onPointerEnter={hover(true)}
        onPointerLeave={hover(false)}
        onClick={() => (clearTimeout(timer.current), setOpen(!open))}
      >
        <FaceView face={g.group.face} blocks={g.blocks} />
      </button>
      {createPortal(
        // The vertical padding is given once here, not by each block: two blocks each padding themselves left a wide gap
        <div
          ref={panel}
          className={cn("fixed z-50 w-72 rounded-lg border bg-popover py-1 text-popover-foreground shadow-md", !open && "hidden")}
          style={pos}
          onPointerEnter={hover(true)}
          onPointerLeave={hover(false)}
        >
          <AddonParts regs={g.blocks} />
        </div>,
        document.body,
      )}
    </>
  );
}

/**
 * The phone drawer's add-on buttons, the same groups as the rail's: a tap opens the group's blocks in a sheet from the
 * bottom. Only mounted while the drawer is open, so there's nothing to keep alive while closed.
 */
export function PhoneGroupButtons() {
  useStore((s) => s.addonVersion);
  useStore((s) => s.config.rail);
  return (
    <>
      {resolveGroups()
        .filter((g) => g.blocks.length)
        .map((g) => (
          <Sheet key={g.group.id}>
            <SheetTrigger asChild>
              <Button variant="ghost" className="h-7 min-w-7 px-1 text-muted-foreground" aria-label={g.group.name}>
                <FaceView face={g.group.face} blocks={g.blocks} small />
              </Button>
            </SheetTrigger>
            {/* No auto-focus: it put a focus ring on the close button of a sheet opened by a tap */}
            <SheetContent side="bottom" className="gap-0 rounded-t-xl pb-[max(0.5rem,env(safe-area-inset-bottom))]" onOpenAutoFocus={(e) => e.preventDefault()}>
              <SheetTitle className="px-4 pt-4 pb-1 text-[13px]">{g.group.name}</SheetTitle>
              <div className="py-1">
                <AddonParts regs={g.blocks} />
              </div>
            </SheetContent>
          </Sheet>
        ))}
    </>
  );
}
