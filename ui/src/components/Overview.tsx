import { PanelLeft } from "lucide-react";
import { useEffect } from "react";
import { Button } from "@/components/ui/button";
import { focusTile, toggleTileZoom, overviewFocus, setOverview, useOverviewIds } from "@/lib/overview";
import { usePhone } from "@/lib/phone";
import { toggleSidebar, useStore } from "@/lib/store";
import { TabActive } from "./Panes";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Grid } from "./OverviewGrid";
import { PhoneOverview } from "./OverviewPhone";

/**
 * The all-agents overview (lib/overview.ts): every open agent of every project in the list on the left, the ones put in
 * auto-arranged on the right. Covers the sidebar and the tabs; they stay mounted underneath (TabActive off) so closing
 * this lands back exactly where things were. `shown` false = kept mounted but hidden, for the same reason.
 */
/** On the desktop just the grid: the sidebar beside it lists the agents (its 进行中 section picks the tiles). */
export function OverviewView({ shown }: { shown: boolean }) {
  const phone = usePhone();
  const ids = useOverviewIds().split(",").filter(Boolean);
  const zoom = useStore((s) => s.overview.zoom);
  const sidebar = useStore((s) => s.sidebar);
  const cur = useStore((s) => overviewFocus(s));
  // A tile taken out while zoomed: back to the grid
  const zoomed = zoom && ids.includes(zoom) ? zoom : undefined;
  // Remember the order the tiles are shown in (overviewIds keeps it), so newcomers don't push the others around
  const idsKey = ids.join(",");
  useEffect(() => {
    if (shown && useStore.getState().overview.ids.join(",") !== idsKey) setOverview((o) => ({ ...o, ids }));
  }, [shown, idsKey]);
  if (phone) return <PhoneOverview ids={ids} focus={cur} shown={shown} />;
  return (
    <TabActive.Provider value={shown}>
      <div className="flex h-full bg-background">
        {/* Hidden with the Code sidebar (one setting, asked for: as in Code mode); a thin rail keeps the way back where the
            hide button was, at the bottom (the top-left corner is the window's traffic lights in the desktop App) */}
        {!sidebar && (
          <div className="flex w-10 shrink-0 flex-col justify-end border-r bg-sidebar">
            <div className="flex h-10 shrink-0 items-center justify-center border-t">
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button variant="ghost" size="icon" className="size-7 text-muted-foreground" onClick={toggleSidebar}>
                    <PanelLeft className="size-3.5" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent side="right">显示侧栏</TooltipContent>
              </Tooltip>
            </div>
          </div>
        )}
        <Grid ids={ids} focus={cur} zoom={zoomed} shown={shown} onFocus={focusTile} onZoom={toggleTileZoom} />
      </div>
    </TabActive.Provider>
  );
}
