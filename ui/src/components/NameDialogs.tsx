import { FolderOpen } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { addSpace, addWorkspace, basename, editWorkspace, renameSpace, renameTab } from "@/lib/actions";
import { panelsChanged } from "@/lib/panels";
import { api } from "@/lib/api";
import { closeDialog, useStore } from "@/lib/store";
import { DirPicker, ensureDir } from "./NewSession";
import { t } from "@/lib/i18n";

// The small forms that ask for a name: renaming a tab / workspace / conversation / session, a new session, a new workspace.

const RENAME_TITLE = {
  tab: "nameDialogs.renameTab",
  session: "nameDialogs.renameSession",
  space: "nameDialogs.renameSpace",
  newSpace: "nameDialogs.newSpace",
  panel: "panelSettings.renameTitle",
} as const;

export function Rename() {
  const d = useStore((s) => s.dialog);
  const r = d?.kind === "rename" ? d : null;
  const [v, setV] = useState("");
  const [err, setErr] = useState("");
  useEffect(() => {
    if (r) setV(r.value);
    setErr("");
  }, [r?.id, r?.target]);
  const save = async () => {
    if (!r) return;
    if (r.target === "tab") renameTab(r.id, v);
    else if (r.target === "newSpace") {
      // A project's name is its id (and its Work directory's name), so a bad / taken one is refused here, not fixed up
      const e = await addSpace(v);
      if (e) return setErr(e);
    } else if (r.target === "space") {
      const e = renameSpace(r.id, v);
      if (e) return setErr(e);
    } else if (r.target === "panel") {
      // The name shown for it (its ui.json title); its directory keeps its name
      if (!v.trim()) return;
      try {
        await api.renamePanel(r.id, v.trim());
        panelsChanged();
      } catch (e: any) {
        return setErr(e.message ?? String(e));
      }
    } else api.rename(r.id, v).catch((e) => toast.error(e.message));
    closeDialog();
  };
  return (
    <Dialog open={!!r} onOpenChange={(o) => !o && closeDialog()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>{r ? t(RENAME_TITLE[r.target]) : ""}</DialogTitle>
          {r?.target === "newSpace" && (
            <DialogDescription>
              {t("nameDialogs.newSpaceHint1")} {t("nameDialogs.newSpaceHint2")}
            </DialogDescription>
          )}
          {r?.target === "space" && <DialogDescription>{t("nameDialogs.renameSpaceHint", { id: r.id })}</DialogDescription>}
        </DialogHeader>
        <Input
          autoFocus
          value={v}
          onChange={(e) => (setV(e.target.value), setErr(""))}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.nativeEvent.isComposing && e.keyCode !== 229) save();
          }}
          placeholder={r?.target === "tab" ? t("nameDialogs.tabPlaceholder") : r?.target === "newSpace" ? t("nameDialogs.name") : ""}
        />
        {err && <div className="text-[12px] text-bad">{err}</div>}
        <DialogFooter>
          <Button onClick={save}>{t("common.ok")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// editId: change an existing workspace's directory / name instead of creating one (a mistyped path had no other way to be fixed)
export function NewWorkspace({ open, editId }: { open: boolean; editId?: string }) {
  const home = useStore((s) => s.home);
  const ws = useStore((s) => (editId ? s.layout.workspaces.find((w) => w.id === editId) : undefined));
  const [name, setName] = useState("");
  const [cwd, setCwd] = useState("");
  useEffect(() => {
    if (!open) return;
    if (ws) {
      setName(ws.name);
      setCwd(ws.cwd ? (home && ws.cwd.startsWith(home + "/") ? "~" + ws.cwd.slice(home.length) : ws.cwd) : "~/");
    } else {
      setName("");
      setCwd("~/");
    }
  }, [open, editId]);
  // dir: a path the picker just chose, before this render's cwd has it
  const create = async (dir = cwd) => {
    const abs = dir.startsWith("~") ? home + dir.slice(1) : dir;
    // A name that was just the old directory's name follows the new directory
    const typed = ws?.cwd && name.trim() === basename(ws.cwd) ? "" : name.trim();
    const n = typed || (dir.replace(/\/+$/, "") && dir !== "~" ? basename(abs) : "");
    const p = abs.replace(/\/+$/, "");
    try {
      if (p) await ensureDir(p); // a path that doesn't exist yet is created (the picker says so)
    } catch (e: any) {
      return toast.error(e.message ?? String(e));
    }
    if (ws) editWorkspace(ws.id, n, p || undefined);
    else addWorkspace(n, p || undefined);
    closeDialog();
  };
  return (
    <Dialog open={open} onOpenChange={(o) => !o && closeDialog()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{ws ? t("nameDialogs.editWorkspace") : t("nameDialogs.newWorkspace")}</DialogTitle>
          <DialogDescription>{ws ? t("nameDialogs.editWorkspaceHint") : t("nameDialogs.newWorkspaceHint")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          {/* Name first and focused: the directory's suggestions open on focus, downward, and would cover it */}
          <div className="space-y-1.5">
            <Label>{t("nameDialogs.name")}</Label>
            <Input
              autoFocus
              value={name}
              placeholder={t("nameDialogs.namePlaceholder")}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && !e.nativeEvent.isComposing && create()}
            />
          </div>
          <div className="space-y-1.5">
            <Label>{t("nameDialogs.dir")}</Label>
            <DirPicker value={cwd} onChange={setCwd} onEnter={create} />
          </div>
        </div>
        <DialogFooter>
          <Button onClick={() => create()}>
            <FolderOpen className="size-4" /> {ws ? t("common.save") : t("nameDialogs.create")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
