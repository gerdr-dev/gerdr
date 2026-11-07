import { Check, Code2, Menu } from "lucide-react";
import { useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Popover, PopoverAnchor, PopoverContent } from "@/components/ui/popover";
import { switchSpace } from "@/lib/actions";
import { currentSpace, spaceList, type Space } from "@/lib/layout";
import { usePanels } from "@/lib/panels";
import { set, setMode, setWorkPanel, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { t } from "@/lib/i18n";
import { PanelIcon } from "./PanelIcon";

/**
 * The phone's ☰ (Code's PhoneBar, a panel's bar, the overview's bar). Tap opens the drawer; long-press (or right-click)
 * opens a bubble with every project's Code and panels, grouped by project, and a tap there goes straight to it.
 * Switching project and view used to take the drawer twice: pick the project, reopen it, pick the panel (asked for).
 * Our own timer rather than contextmenu: iOS Safari never fires it on a long-press (as PhoneBar's OverviewButton).
 */
export function DrawerButton({ children }: { children?: React.ReactNode }) {
  const [open, setOpen] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  const held = useRef(false);
  const btn = useRef<HTMLButtonElement>(null);
  const hold = () => {
    held.current = true;
    (document.activeElement as HTMLElement | null)?.blur?.();
    setOpen(true);
  };
  const cancel = () => clearTimeout(timer.current);
  const openDrawer = () => {
    // Drop the keyboard first: with it up, the first tap in the drawer would only close the keyboard
    (document.activeElement as HTMLElement | null)?.blur?.();
    set({ drawer: true });
  };
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <Button
          ref={btn}
          variant="ghost"
          size="icon"
          // No text selection / iOS callout from the long-press
          className="relative size-9 shrink-0 select-none [-webkit-touch-callout:none]"
          aria-label={t("phoneBar.sidebar")}
          onPointerDown={() => {
            held.current = false;
            cancel();
            timer.current = window.setTimeout(hold, 450);
          }}
          onPointerUp={cancel}
          onPointerLeave={cancel}
          onPointerCancel={cancel}
          onContextMenu={(e) => {
            e.preventDefault();
            cancel();
            if (!held.current) hold();
          }}
          onClick={() => {
            // The click that ends a long-press already opened the bubble
            if (held.current) return void (held.current = false);
            openDrawer();
          }}
        >
          <Menu className="size-[18px]" />
          {children}
        </Button>
      </PopoverAnchor>
      <PopoverContent
        align="start"
        collisionPadding={8}
        className="max-h-[70dvh] w-48 overflow-y-auto p-1"
        // Lifting the finger that long-pressed gives the ☰ focus (and a click), which the popover took for "outside" and
        // closed itself at once on a phone. The ☰ isn't a PopoverTrigger (a tap opens the drawer), so exempt it here
        onInteractOutside={(e) => {
          if (btn.current?.contains(e.target as Node)) e.preventDefault();
        }}
      >
        {open && <AllViews done={() => setOpen(false)} />}
      </PopoverContent>
    </Popover>
  );
}

function AllViews({ done }: { done: () => void }) {
  const spaces = spaceList(useStore((s) => s.layout));
  return (
    <>
      {spaces.map((sp, i) => (
        <div key={sp.id} className={cn(i > 0 && "mt-1 border-t pt-1")}>
          <ProjectViews sp={sp} done={done} named />
        </div>
      ))}
    </>
  );
}

/** One project's Code and panels; `named` heads them with its name (the ☰ bubble groups every project, a chip's has only its own). */
export function ProjectViews({ sp, done, named }: { sp: Space; done: () => void; named?: boolean }) {
  const { panels } = usePanels(sp.id);
  const here = useStore((s) => currentSpace(s.layout) === sp.id);
  const mode = useStore((s) => s.mode);
  const chosen = useStore((s) => s.workPanels[sp.id] ?? "");
  // Only the project on screen marks what shows; another one's last view would be a guess
  const on = (panel: string | null) => here && (panel === null ? mode !== "work" : mode === "work" && panel === (chosen || panels[0]?.name));
  // switchSpace first: it restores the project's own last mode, which the pick then overrides. setMode closes the drawer
  const go = (panel: string | null) => {
    switchSpace(sp.id);
    if (panel === null) setMode("agents");
    else {
      setWorkPanel(sp.id, panel);
      setMode("work");
    }
    done();
  };
  const row = (key: string, panel: string | null, icon: React.ReactNode, label: string) => (
    <button
      key={key}
      className={cn(
        "flex h-8 w-full items-center gap-2 rounded-sm px-2 text-left text-[13px]",
        on(panel) ? "bg-accent text-foreground" : "text-foreground/90 active:bg-accent/60",
      )}
      onClick={() => go(panel)}
    >
      {icon}
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {on(panel) && <Check className="size-3 shrink-0 text-muted-foreground" />}
    </button>
  );
  return (
    <>
      {named && <div className="truncate px-2 pt-1 pb-0.5 text-[11px] font-medium text-muted-foreground">{sp.name}</div>}
      {row("code", null, <Code2 className="size-3.5 shrink-0" />, "Code")}
      {panels.map((p) => row(p.key, p.name, <PanelIcon icon={p.icon} color={p.color} title={p.title} className="size-3.5 shrink-0" />, p.title))}
    </>
  );
}
