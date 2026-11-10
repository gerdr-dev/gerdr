import { Hourglass } from "lucide-react";
import { useEffect, useMemo } from "react";
import { create } from "zustand";
import type { HostTask, HostTaskGroup } from "@gerdr/chat";
import { api } from "./api";
import { t } from "./i18n";
import type { WaitView } from "./types";

// What each session waits for (`gerdr wait`, docs/dev/EVENTS.md "Waiting for a state"), shown with its background
// tasks: the same chip under the composer, its own "waiting" group in the list (ChatHost.useTaskGroups). Without it a
// session waiting on an MR looked idle, and nobody could tell what it was waiting for or stop it. Loaded on first use
// and again on every "waits" broadcast (a wait added / removed, or a new answer about what it waits on).

const useWaits = create<{ bySession: Record<string, WaitView[]> }>(() => ({ bySession: {} }));

let loaded = false;
let timer: ReturnType<typeof setTimeout> | null = null;

/** Loads every session's waits again, soon (several broadcasts in a row are one load). */
export function refreshWaits() {
  if (timer) return;
  timer = setTimeout(async () => {
    timer = null;
    try {
      const { list } = await api.waits();
      const bySession: Record<string, WaitView[]> = {};
      for (const w of list ?? []) (bySession[w.session] ??= []).push(w);
      useWaits.setState({ bySession });
    } catch {
      // A backend without waits (503) or offline: nothing to show
    }
  }, 300);
}

/** gitlab.example.com/group/repo!34 → group/repo!34: the host is noise in a list of one person's MRs. */
function shortKey(key: string) {
  const i = key.indexOf("/");
  return i > 0 && key.slice(0, i).includes(".") ? key.slice(i + 1) : key;
}

function item(w: WaitView): HostTask {
  const problem = !!w.warning;
  return {
    id: w.id,
    title: `${w.source} · ${shortKey(w.key)}`,
    detail: problem ? w.reporter?.reason || w.error || w.warning : t("waits.until", { until: w.until }),
    problem,
    startedAt: w.created,
    stop: async () => {
      await api.deleteWait(w.id);
      refreshWaits();
    },
  };
}

/** ChatHost.useTaskGroups: the session's waits as one group. */
export function useWaitGroups(sessionId: string): HostTaskGroup[] {
  useEffect(() => {
    if (!loaded) {
      loaded = true;
      refreshWaits();
    }
  }, []);
  const list = useWaits((s) => s.bySession[sessionId]);
  return useMemo(() => (list?.length ? [{ id: "waits", label: (n: number) => t("waits.count", { n }), icon: Hourglass, items: list.map(item) }] : []), [list]);
}
