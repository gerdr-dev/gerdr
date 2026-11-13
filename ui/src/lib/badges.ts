import { useEffect } from "react";
import { create } from "zustand";
import { api } from "./api";
import { get as getApp, useStore } from "./store";

// Panels' badges on their rail buttons (server/badge.go; ctx.badge, `gerdr panel badge`): by panel key, >0 a number,
// 0 a plain dot. Loaded when the connection comes up and replaced by every "badges" broadcast. A badge goes when the
// user is looking at its panel: on screen, in a window that is visible and focused (a window left showing the panel
// in the background would otherwise eat every badge before anyone saw it).

export const useBadges = create<{ byPanel: Record<string, number> }>(() => ({ byPanel: {} }));

export function setBadges(byPanel: Record<string, number> | null | undefined) {
  useBadges.setState({ byPanel: byPanel ?? {} });
}

function load() {
  api.badges().then(setBadges, () => {});
}

/** Loads the badges now and again on every reconnect (a broadcast missed while offline). */
export function watchBadges() {
  load();
  let was = getApp().connected;
  return useStore.subscribe((s) => {
    if (s.connected === was) return;
    was = s.connected;
    if (was) load();
  });
}

/** Clears panel `key`'s badge while it is the panel on screen and the window has the user's attention. */
export function useClearBadge(key: string, shown: boolean) {
  const has = useBadges((s) => s.byPanel[key] !== undefined);
  useEffect(() => {
    if (!shown || !has) return;
    const check = () => {
      if (document.visibilityState !== "visible" || !document.hasFocus()) return;
      const { [key]: _, ...rest } = useBadges.getState().byPanel;
      useBadges.setState({ byPanel: rest }); // at once; the broadcast that follows says the same
      api.setBadge(key, -1).catch(() => {});
    };
    check();
    window.addEventListener("focus", check);
    document.addEventListener("visibilitychange", check);
    return () => {
      window.removeEventListener("focus", check);
      document.removeEventListener("visibilitychange", check);
    };
  }, [key, shown, has]);
}
