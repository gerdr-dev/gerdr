import { LifeBuoy } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { api, type UIStatus } from "@/lib/api";
import { closeDialog } from "@/lib/store";

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
        <div className="text-[13px] text-warn">{g.merging ? "新版界面合到一半" : "新版界面没合上"}</div>
        <div className="text-[12px] text-muted-foreground">
          {g.merging
            ? "界面目录里还有没解决的冲突。"
            : `gerdr 的新版界面${g.pending_version ? `（${g.pending_version}）` : ""}和你对界面的改动冲突，升级时跳过了，现在用的还是旧的。`}
          {files.length > 0 && <div className="break-all">冲突的文件：{files.join(", ")}</div>}
        </div>
      </div>
      <Button size="sm" variant="outline" className="shrink-0 gap-1.5" onClick={openRescue}>
        <LifeBuoy className="size-3.5" />让 agent 合并
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
