import { Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, type Busy } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { askConfirm } from "@/lib/confirm";
import { t } from "@/lib/i18n";

// Settings → Versions → "Restart Gerdr" (POST /api/restart). Before it there was no way to restart the backend from
// the UI: the App only swaps in a new one by itself once nothing is running, and quitting the App leaves the
// background service up. Local port only (the API answers 403 remotely): restarting from the phone could leave the
// phone with nothing to reconnect to, so the row isn't drawn there.

/** What a restart would cut off, as one sentence; "" = nothing. */
function busyText(b: Busy): string {
  const parts: string[] = [];
  if (b.turns) parts.push(t("restartSetting.turns", { n: b.turns }));
  if (b.waiting) parts.push(t("restartSetting.waiting", { n: b.waiting }));
  if (b.tasks) parts.push(t("restartSetting.tasks", { n: b.tasks }));
  if (b.terms) parts.push(t("restartSetting.terms", { n: b.terms }));
  return parts.join(t("restartSetting.sep"));
}

export function RestartSetting() {
  const [can, setCan] = useState<boolean | null>(null);
  const [restarting, setRestarting] = useState(false);

  useEffect(() => {
    api
      .restartInfo()
      .then((r) => setCan(r.can))
      .catch(() => setCan(null));
  }, []);

  if (can === null) return null;

  const restart = async () => {
    // Asked again at the click: what was running when Settings opened is long out of date
    let what = "";
    try {
      what = busyText((await api.restartInfo()).busy);
    } catch {}
    if (what) {
      const ok = await askConfirm({
        title: t("restartSetting.confirmTitle"),
        description: t("restartSetting.confirmBody", { what }),
        action: t("restartSetting.anyway"),
        destructive: true,
      });
      if (!ok) return;
    }
    setRestarting(true);
    try {
      await api.restart();
    } catch (e) {
      toast.error((e as Error).message);
      setRestarting(false);
      return;
    }
    // The old process answers for another ~0.3s, and launchd may hold the new one back up to 10s (ThrottleInterval):
    // wait until the backend has been unreachable once, then until it answers again. The page's WebSocket
    // reconnects by itself.
    const deadline = Date.now() + 30_000;
    let down = false;
    while (Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 500));
      const up = await api.restartInfo().then(
        () => true,
        () => false,
      );
      if (!up) down = true;
      else if (down) {
        setRestarting(false);
        toast.success(t("restartSetting.done"));
        return;
      }
    }
    setRestarting(false);
    toast.error(t("restartSetting.timeout"));
  };

  return (
    <div className="flex items-center justify-between gap-4">
      <div className="min-w-0">
        <div className="text-[13px]">{t("restartSetting.title")}</div>
        <div className="text-[12px] text-muted-foreground">{can ? t("restartSetting.hint") : t("restartSetting.terminal")}</div>
      </div>
      <Button size="sm" variant="outline" disabled={!can || restarting} onClick={restart}>
        {restarting && <Loader2 className="size-3.5 animate-spin" />}
        {restarting ? t("restartSetting.restarting") : t("restartSetting.button")}
      </Button>
    </div>
  );
}
