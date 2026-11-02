import { ArrowLeft, ClipboardCheck } from "lucide-react";
import { useLayoutEffect, useRef } from "react";
import { Button } from "@/components/ui/button";
import { compareSessions } from "@/lib/order";
import { focusTile, toggleOverview } from "@/lib/overview";
import { useCaughtUp } from "@/lib/catchup";
import { useStore } from "@/lib/store";
import { TabActive } from "./Panes";
import { AddMenu, PickButton } from "./OverviewPick";
import { Tile, TileChip } from "./OverviewTile";

/**
 * The overview on a phone: no room for a grid, so one tile at a time. The top bar is the tile switcher, one chip per
 * tile tinted with its status (waiting / running / unseen readable at a glance, asked for); the list of agents is the
 * sidebar drawer (UnifiedSidebar), whose 进行中 rows pick the tile here. Only the shown tile is mounted (a phone can't afford a dozen live chats); its transcript stays cached.
 */
export function PhoneOverview({ ids, focus, shown }: { ids: string[]; focus?: string; shown: boolean }) {
  const chips = useRef<HTMLDivElement>(null);
  const sessions = useStore((s) => s.sessions);
  const unread = useStore((s) => s.unread);
  // Chips in the Code sidebar's agent-list order (asked for: what needs you first), not the order they were put in;
  // ties keep that order (sort is stable).
  const sorted = ids.filter((id) => sessions[id]).sort((a, b) => compareSessions(sessions[a], sessions[b], unread));
  // Only the shown tile is watched, so the one switched to may not be loaded or caught up: keep the current tile up
  // until it is (lib/catchup) instead of drawing "loading" / the old state and then flashing to the new.
  const tile = useCaughtUp(focus, focus ? [focus] : [], (id) => sorted.includes(id));
  // Keep the current chip in view (on opening, switching from the list, a removal, a reorder). Scrolls the strip itself after layout:
  // scrollIntoView also moves ancestors, and run before the chips were laid out it left the current one off screen.
  const idsKey = sorted.join(",");
  useLayoutEffect(() => {
    const strip = chips.current;
    const chip = strip?.querySelector<HTMLElement>("[data-current]");
    if (!strip || !chip) return;
    const left = chip.offsetLeft - strip.offsetLeft;
    if (left < strip.scrollLeft) strip.scrollLeft = left;
    else if (left + chip.offsetWidth > strip.scrollLeft + strip.clientWidth) strip.scrollLeft = left + chip.offsetWidth - strip.clientWidth;
  }, [focus, idsKey, shown]);
  return (
    <TabActive.Provider value={shown}>
      <div className="flex h-full flex-col bg-background">
        {/* No text selection: a long-press on PhoneBar's overview button opens this with the finger still down on this
        bar, and Android then turned that long-press into selecting a chip's title */}
        <div className="flex h-11 shrink-0 items-center gap-1 border-b bg-sidebar px-1.5 select-none [-webkit-touch-callout:none]">
          {/* size-9 like PhoneBar's ☰ (PhoneBar.tsx): the two bars swap in the same spot, so their first icons line up */}
          <Button variant="ghost" size="icon" className="size-9 shrink-0" aria-label="返回" onClick={() => toggleOverview(false)}>
            <ArrowLeft className="size-[18px]" />
          </Button>
          {/* Both scrollbar hiders: Android's WebView ignores scrollbar-width and drew a bar under the chips */}
          <div ref={chips} className="flex min-w-0 flex-1 gap-1 overflow-x-auto [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
            {sorted.map((id) => (
              <TileChip key={id} id={id} current={id === focus} className="h-8 max-w-36" onClick={() => focusTile(id)} />
            ))}
          </div>
          <AddMenu focus={tile} />
        </div>
        <div className="min-h-0 flex-1">
          {tile ? (
            <Tile key={tile} id={tile} focused={shown} main={false} zoomed={false} target={false} phone onFocus={() => {}} onZoom={() => {}} />
          ) : (
            <div className="flex h-full flex-col items-center justify-center gap-3 px-8 text-center text-[13px] text-muted-foreground">
              <ClipboardCheck className="size-6" />
              进行中是空的
              <PickButton />
            </div>
          )}
        </div>
      </div>
    </TabActive.Provider>
  );
}
