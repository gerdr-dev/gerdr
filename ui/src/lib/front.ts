// Whether this window is in front (shown and focused), as a hook. Unread is shared by every device now (store.ts
// unreadSync), so a session counts as read only when someone actually looks: a focused pane in a window behind
// others, or on a phone in a pocket, must not clear what another device just marked unread.
import { useSyncExternalStore } from "react";

export const windowInFront = () => document.visibilityState === "visible" && document.hasFocus();

function subscribe(cb: () => void) {
  window.addEventListener("focus", cb);
  window.addEventListener("blur", cb);
  document.addEventListener("visibilitychange", cb);
  return () => {
    window.removeEventListener("focus", cb);
    window.removeEventListener("blur", cb);
    document.removeEventListener("visibilitychange", cb);
  };
}

export const useWindowInFront = () => useSyncExternalStore(subscribe, windowInFront);
