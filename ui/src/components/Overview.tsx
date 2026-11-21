import { useEffect } from "react";
import { overviewFocus, setOverview, useOverviewIds } from "@/lib/overview";
import { usePhone } from "@/lib/phone";
import { useStore } from "@/lib/store";
import { PhoneOverview } from "./OverviewPhone";

/**
 * The phone-only all-agents overview (PhoneOverview). Desktop drops the multi-tile grid and uses the sidebar's
 * Active section directly; `shown` false keeps PhoneOverview mounted so scroll positions survive switching back.
 */
export function OverviewView({ shown }: { shown: boolean }) {
  const phone = usePhone();
  const ids = useOverviewIds().split(",").filter(Boolean);
  const cur = useStore((s) => overviewFocus(s));
  // Remember the order the tiles are shown in (overviewIds keeps it), so newcomers don't push the others around
  const idsKey = ids.join(",");
  useEffect(() => {
    if (phone && shown && useStore.getState().overview.ids.join(",") !== idsKey) setOverview((o) => ({ ...o, ids }));
  }, [phone, shown, idsKey]);
  if (!phone) return null;
  return <PhoneOverview ids={ids} focus={cur} shown={shown} />;
}
