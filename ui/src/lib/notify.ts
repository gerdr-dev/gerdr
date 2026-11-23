import { toast } from "sonner";
import { nativeNotify } from "./native";
import { locateSession, spaceOf } from "./layout";
import { get, prefOf } from "./store";

/** 系统通知：mac App 走原生通知，浏览器走 Notification API（要权限，第一次会问）。 */
export function systemNotify(title: string, body: string, sessionId?: string) {
  if (!prefOf(get(), "notifications")) return;
  const loc = sessionId ? locateSession(get().layout, sessionId) : undefined;
  if (nativeNotify(title, body, sessionId, loc && spaceOf(loc.ws))) return;
  if (!("Notification" in window)) return;
  const show = () => {
    const n = new Notification(title, { body, tag: sessionId });
    n.onclick = () => {
      window.focus();
      if (sessionId) import("./actions").then((a) => a.focusSession(sessionId));
    };
  };
  if (Notification.permission === "granted") show();
  else if (Notification.permission !== "denied") Notification.requestPermission().then((p) => p === "granted" && show());
}

export function notify(text: string, opts?: { level?: "info" | "error"; system?: boolean; sessionId?: string; sticky?: boolean; id?: string }) {
  if (opts?.system) return systemNotify("Gerdr", text, opts.sessionId);
  // sticky: stays until closed, for something to act on that a few seconds would hide; id: shown again, it replaces
  // the one on screen instead of stacking a copy
  const o = { id: opts?.id, ...(opts?.sticky ? { duration: Infinity, closeButton: true } : {}) };
  if (opts?.level === "error") toast.error(text, o);
  else toast(text, o);
}
