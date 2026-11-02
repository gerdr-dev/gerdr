import { useEffect } from "react";
import { focusTile, toggleTileZoom, overviewFocus, setOverview, useOverviewIds } from "@/lib/overview";
import { usePhone } from "@/lib/phone";
import { useStore } from "@/lib/store";
import { TabActive } from "./Panes";
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
        {/* With the list hidden the way back is the rail's Code button (Rail.tsx), right beside the grid */}
        <Grid ids={ids} focus={cur} zoom={zoomed} shown={shown} onFocus={focusTile} onZoom={toggleTileZoom} />
      </div>
    </TabActive.Provider>
  );
}
