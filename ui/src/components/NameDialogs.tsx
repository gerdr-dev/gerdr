import { FolderOpen } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { addSpace, addWorkspace, basename, renameTab, renameWorkspace } from "@/lib/actions";
import { api } from "@/lib/api";
import { closeDialog, useStore } from "@/lib/store";
import { DirPicker } from "./NewSession";

// The small forms that ask for a name: renaming a tab / workspace / conversation / session, a new session, a new workspace.

const RENAME_TITLE = { tab: "重命名 tab", workspace: "重命名工作空间", session: "重命名会话", space: "项目", newSpace: "新项目" };

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
    else if (r.target === "workspace") renameWorkspace(r.id, v);
    else if (r.target === "newSpace") {
      // A project's name is its id (and its Work directory's name), so a bad / taken one is refused here, not fixed up
      const e = await addSpace(v);
      if (e) return setErr(e);
    } else if (r.target !== "space") api.rename(r.id, v).catch((e) => toast.error(e.message));
    closeDialog();
  };
  return (
    <Dialog open={!!r} onOpenChange={(o) => !o && closeDialog()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>{r ? RENAME_TITLE[r.target] : ""}</DialogTitle>
          {r?.target === "newSpace" && (
            <DialogDescription>
              一个独立的项目：有自己的工作空间和 agent（Code），也有自己的 Work 页面和助手。不同项目里的 agent 互相看不见、不能互发消息。
              名字就是项目的标识，建好后不能改；关掉以后再建同名的项目，它的 Work 还在。
            </DialogDescription>
          )}
        </DialogHeader>
        <Input
          autoFocus
          value={v}
          onChange={(e) => (setV(e.target.value), setErr(""))}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.nativeEvent.isComposing && e.keyCode !== 229) save();
          }}
          placeholder={r?.target === "tab" ? "留空 = 用目录名" : r?.target === "newSpace" ? "名字" : ""}
        />
        {err && <div className="text-[12px] text-bad">{err}</div>}
        <DialogFooter>
          <Button onClick={save}>好</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function NewWorkspace({ open }: { open: boolean }) {
  const home = useStore((s) => s.home);
  const [name, setName] = useState("");
  const [cwd, setCwd] = useState("");
  useEffect(() => {
    if (open) {
      setName("");
      setCwd("~/");
    }
  }, [open]);
  const create = () => {
    const abs = cwd.startsWith("~") ? home + cwd.slice(1) : cwd;
    const n = name.trim() || (cwd.replace(/\/+$/, "") && cwd !== "~" ? basename(abs) : "");
    addWorkspace(n, abs.replace(/\/+$/, "") || undefined);
    closeDialog();
  };
  return (
    <Dialog open={open} onOpenChange={(o) => !o && closeDialog()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>新工作空间</DialogTitle>
          <DialogDescription>一个项目一个工作空间：里面新开的 tab 默认在这个目录。</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label>目录</Label>
            <DirPicker value={cwd} onChange={setCwd} onEnter={create} autoFocus />
          </div>
          <div className="space-y-1.5">
            <Label>名字</Label>
            <Input
              value={name}
              placeholder="留空 = 目录名"
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && !e.nativeEvent.isComposing && create()}
            />
          </div>
        </div>
        <DialogFooter>
          <Button onClick={create}>
            <FolderOpen className="size-4" /> 创建
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
