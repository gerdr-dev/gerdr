import { Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { reconnectNow } from "@/lib/sync";
import { toast } from "sonner";
import { useRoute } from "./AndroidSettings";
import { t } from "@/lib/i18n";

/**
 * The connection status while disconnected from the gerdr backend. Used to be a bare red "offline", which read as "broken" while
 * sync.ts was in fact retrying with backoff (a backend restart takes a few seconds): now it says it's reconnecting, counts down
 * to the next try, and offers to try now. Blips under a second (sleep / wake, a dropped stale socket) show nothing.
 */
export function ConnStatus() {
  const connected = useStore((s) => s.connected);
  const conn = useStore((s) => s.conn);
  const [shown, setShown] = useState(false);
  const [, tick] = useState(0);
  useEffect(() => {
    setShown(false);
    if (connected) return;
    const t = setTimeout(() => setShown(true), 1000);
    return () => clearTimeout(t);
  }, [connected]);
  useEffect(() => {
    if (connected || conn.state !== "waiting") return;
    const t = setInterval(() => tick((x) => x + 1), 250);
    return () => clearInterval(t);
  }, [connected, conn]);
  const ever = useStore((s) => s.everConnected);
  const retrying = useStore((s) => s.connRetrying);
  // The first attempt after opening: the page shows its own loading state, a "reconnecting" here would be wrong and redundant
  if (connected || !shown || (!ever && !retrying)) return null;
  const left = conn.state === "waiting" ? conn.at - Date.now() : 0;
  // A wait under a second would flash a "1 s" countdown between attempts: show it as still connecting
  if (left < 1000)
    return (
      <span
        className="flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground"
        title={ever ? t("connStatus.reconnectingHint") : t("connStatus.connectingHint")}
      >
        <Loader2 className="size-3 animate-spin" />
        {ever ? t("connStatus.reconnecting") : t("connStatus.connecting")}
      </span>
    );
  return (
    <span
      className="flex shrink-0 items-center gap-1 text-[11px] text-warn tabular-nums"
      title={t(ever ? "connStatus.lostRetryIn" : "connStatus.unreachableRetryIn", { n: Math.ceil(left / 1000) })}
    >
      {ever ? t("connStatus.disconnected") : t("connStatus.unreachable")} · {Math.ceil(left / 1000)}s
      <button className="rounded-sm px-1 text-foreground hover:bg-accent" style={{ WebkitAppRegion: "no-drag" } as any} onClick={reconnectNow}>
        {ever ? t("connStatus.reconnect") : t("common.retry")}
      </button>
    </span>
  );
}

/**
 * The connection status as a toast, on every screen: a phone's top bar has no room for ConnStatus (it got squeezed out by
 * the tab name) and Work mode covers it entirely; wider screens lost the bottom row it sat in to the rail (Rail.tsx).
 * Same timing as ConnStatus (blips under a second show nothing); the toast stays until the connection is back.
 */
export function ConnToast() {
  const connected = useStore((s) => s.connected);
  const conn = useStore((s) => s.conn);
  const [shown, setShown] = useState(false);
  const [, tick] = useState(0);
  useEffect(() => {
    setShown(false);
    if (connected) return;
    const t = setTimeout(() => setShown(true), 1000);
    return () => clearTimeout(t);
  }, [connected]);
  useEffect(() => {
    if (connected || conn.state !== "waiting") return;
    const t = setInterval(() => tick((x) => x + 1), 250);
    return () => clearInterval(t);
  });
  const ever = useStore((s) => s.everConnected);
  const retrying = useStore((s) => s.connRetrying);
  // Same as ConnStatus: nothing during the first attempt after opening (the page is showing it's loading)
  const on = shown && !connected && (ever || retrying);
  const left = conn.state === "waiting" ? Math.ceil((conn.at - Date.now()) / 1000) : 0;
  // A wait under a second would flash a "1 s" countdown between attempts: show it as still connecting
  const text = ever
    ? left > 1
      ? t("connStatus.toastLost", { n: left })
      : t("connStatus.toastReconnecting")
    : left > 1
      ? t("connStatus.toastUnreachable", { n: left })
      : t("connStatus.toastConnecting");
  useEffect(() => {
    if (!on) return;
    toast.loading(text, {
      id: "conn",
      duration: Infinity,
      dismissible: false,
      // preventDefault: sonner closes a toast on its action by default, which would flash it away and back
      action: left > 1 ? { label: ever ? t("connStatus.reconnect") : t("common.retry"), onClick: (e) => (e.preventDefault(), reconnectNow()) } : undefined,
    });
  }, [on, text, left > 1]);
  useEffect(() => {
    if (on) return;
    toast.dismiss("conn");
  }, [on]);
  useEffect(() => () => void toast.dismiss("conn"), []);
  return null;
}

export function RouteLabel() {
  const r = useRoute();
  return (
    <span title={r.detail} className="flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground">
      <span className={cn("size-1.5 rounded-full", r.via === "lan" ? "bg-ok" : "bg-brand")} />
      {r.via === "lan" ? t("connStatus.lan") : t("connStatus.relay")}
    </span>
  );
}
