import { Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { reconnectNow } from "@/lib/sync";
import { toast } from "sonner";
import { useRoute } from "./AndroidSettings";

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
        title={ever ? "和 Gerdr 后台的连接断了，正在重连" : "正在连接 Gerdr 后台"}
      >
        <Loader2 className="size-3 animate-spin" />
        {ever ? "重连中…" : "连接中…"}
      </span>
    );
  return (
    <span
      className="flex shrink-0 items-center gap-1 text-[11px] text-warn tabular-nums"
      title={`${ever ? "和 Gerdr 后台的连接断了" : "连不上 Gerdr 后台"}，${Math.ceil(left / 1000)} 秒后自动重试`}
    >
      {ever ? "断开" : "连不上"} · {Math.ceil(left / 1000)}s
      <button className="rounded-sm px-1 text-foreground hover:bg-accent" style={{ WebkitAppRegion: "no-drag" } as any} onClick={reconnectNow}>
        {ever ? "重连" : "重试"}
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
      ? `和 Gerdr 的连接断了，${left} 秒后重连`
      : "正在重连 Gerdr…"
    : left > 1
      ? `连不上 Gerdr，${left} 秒后重试`
      : "正在连接 Gerdr…";
  useEffect(() => {
    if (!on) return;
    toast.loading(text, {
      id: "conn",
      duration: Infinity,
      dismissible: false,
      // preventDefault: sonner closes a toast on its action by default, which would flash it away and back
      action: left > 1 ? { label: ever ? "重连" : "重试", onClick: (e) => (e.preventDefault(), reconnectNow()) } : undefined,
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
      {r.via === "lan" ? "局域网" : "中继"}
    </span>
  );
}
