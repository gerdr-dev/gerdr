import { LifeBuoy } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { api, type UIStatus } from "@/lib/api";
import { closeDialog } from "@/lib/store";
import { t } from "@/lib/i18n";

// Settings → 通用 →「版本与后台」: a newer gerdr UI that the upgrade didn't merge because it conflicts with edits made
// to this UI (webui's upgradeGit aborts rather than leave conflict markers that break the page). Without this the
// only trace is a log line, and the user sees an App that "updated" yet looks the same. The fix is an agent running
// `gerdr ui merge`, so the button opens the rescue panel (built into gerdr, outside this directory), which starts
// that conversation with the conflicting files filled in.

export function UIUpgradeSetting() {
  const [st, setSt] = useState<UIStatus | null>(null);
  useEffect(() => {
    api
      .uiStatus()
      .then(setSt)
      .catch(() => {});
  }, []);
  const g = st?.git;
  if (!g?.pending_upstream && !g?.merging) return null;
  const files = (g.merging ? g.conflicts : g.pending_conflicts) ?? [];
  return (
    <div className="flex items-center justify-between gap-4">
      <div className="min-w-0">
        <div className="text-[13px] text-warn">{g.merging ? t("uiUpgradeSetting.merging") : t("uiUpgradeSetting.pending")}</div>
        <div className="text-[12px] text-muted-foreground">
          {g.merging
            ? t("uiUpgradeSetting.mergingHint")
            : t("uiUpgradeSetting.pendingHint", { version: g.pending_version ? t("uiUpgradeSetting.pendingVersion", { version: g.pending_version }) : "" })}
          {files.length > 0 && <div className="break-all">{t("uiUpgradeSetting.files", { files: files.join(", ") })}</div>}
        </div>
      </div>
      <Button size="sm" variant="outline" className="shrink-0 gap-1.5" onClick={openRescue}>
        <LifeBuoy className="size-3.5" />
        {t("uiUpgradeSetting.merge")}
      </Button>
    </div>
  );
}

function openRescue() {
  const r = (window as any).__gerdrRescue;
  closeDialog(); // the panel opens beside the page; the dialog would sit on top of it
  if (r) r.open();
  else location.href = "/rescue";
}
