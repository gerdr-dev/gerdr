import { defaultFilter } from "cmdk";
import type { usePanelSearches } from "@/core/search";

type PanelGroups = ReturnType<typeof usePanelSearches>;

export function rankSearch<T>(items: T[], query: string, text: (item: T) => string): T[] {
  const term = query.trim();
  if (!term) return items;
  return items
    .map((item) => ({ item, score: defaultFilter(text(item), term) }))
    .filter(({ score }) => score > 0)
    .sort((a, b) => b.score - a.score)
    .map(({ item }) => item);
}

/** Prefer a matching panel entry to the underlying conversation or pane. A pane-only match can still be opened. */
export function searchWorkspace<T>(query: string, groups: PanelGroups, panes: T[], searchablePane: (item: T) => { sessionId?: string; text: string }) {
  const panels = groups
    .map(([key, group]): PanelGroups[number] => [
      key,
      { ...group, items: rankSearch(group.items, query, (x) => [x.label, x.detail, x.keywords, x.id].join(" ")) },
    ])
    .filter(([, group]) => group.items.length > 0);
  const represented = new Set(panels.flatMap(([, group]) => group.items.map((item) => item.sessionId)));
  const candidates = panes.map((item) => ({ item, ...searchablePane(item) })).filter((x) => !x.sessionId || !represented.has(x.sessionId));
  return { panels, panes: rankSearch(candidates, query, (x) => x.text).map((x) => x.item) };
}
