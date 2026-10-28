import { Pencil, RotateCcw } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Popover, PopoverAnchor, PopoverContent } from "@/components/ui/popover";
import { api } from "@/lib/api";
import { get } from "@/lib/store";
import type { GMessage } from "@/lib/transcript";
import { cn } from "@/lib/utils";

// 用户消息旁边的「改这句重发」「回到这句之前」：点一下在按钮旁边冒一个小气泡（不是全屏弹窗，不挡对话），
// 再点一次同一个按钮 / 回车 / 点气泡里的按钮 = 确认，Esc 或点别处 = 算了（用户点名要「动静更小」）。
// 不用长按：触控板上按不准，手机上又和选字抢手势。
// 文件那块和原来的弹窗一样：这条消息之后改过哪些文件（rewindPreview）、要不要一起恢复（默认值跟设置走）。

export function RewindButton({ msg, sessionId, edit, onConfirm }: { msg: GMessage; sessionId: string; edit: boolean; onConfirm: (restore: boolean) => void }) {
  const [open, setOpen] = useState(false);
  const [restore, setRestore] = useState(true);
  const [files, setFiles] = useState<string[] | null | undefined>(undefined);
  const hasSnap = !!msg.metadata?.snapshot;

  const show = () => {
    setRestore(get().config.rewindFiles);
    setFiles(undefined);
    setOpen(true);
    if (hasSnap) {
      api
        .rewindPreview(sessionId, msg.metadata!.messageId!)
        .then((r) => setFiles(r.files))
        .catch(() => setFiles(null));
    }
  };
  const confirm = () => {
    setOpen(false);
    onConfirm(restore && hasSnap);
  };
  const Icon = edit ? Pencil : RotateCcw;
  const label = edit ? "改这句重发" : "回到这句之前";

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <button
          data-state={open ? "open" : "closed"}
          data-rewind={edit ? "edit" : "rewind"}
          title={open ? "再点一次确认" : label}
          className={cn("rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground", open && "bg-brand/15 text-brand hover:bg-brand/20 hover:text-brand")}
          onClick={() => (open ? confirm() : show())}
        >
          <Icon className="size-3.5" />
        </button>
      </PopoverAnchor>
      <PopoverContent
        side="top"
        align="end"
        className="w-72 space-y-2 p-3 text-[12px]"
        // 不抢走「再点一次」：点按钮本身不算点到外面
        onInteractOutside={(e) => {
          if ((e.target as HTMLElement | null)?.closest?.('[data-state="open"]')) e.preventDefault();
        }}
      >
        <div className="text-[13px] font-medium">{label}？</div>
        <div className="text-muted-foreground">
          这句和它之后的对话都会丢掉，agent 也会忘掉。{edit ? "原话放回输入框，改完再发。" : ""}
        </div>
        {hasSnap ? (
          <label className="flex cursor-pointer items-start gap-2">
            <Checkbox className="mt-0.5" checked={restore} onCheckedChange={(v) => setRestore(!!v)} />
            <span>
              同时把文件恢复到那时候
              {restore && files === undefined && <span className="text-muted-foreground">（看改了哪些…）</span>}
              {restore && files && files.length === 0 && <span className="text-muted-foreground">（之后没改过文件）</span>}
              {restore && files && files.length > 0 && <span className="text-muted-foreground">（{files.length} 个文件）</span>}
            </span>
          </label>
        ) : (
          <div className="text-muted-foreground">这句没有文件快照，只回退对话。</div>
        )}
        {restore && files && files.length > 0 && (
          <div className="max-h-28 overflow-auto rounded border bg-muted/40 px-2 py-1 font-mono text-[11px]">
            {files.map((f) => (
              <div key={f} className="truncate" title={f}>
                {f}
              </div>
            ))}
          </div>
        )}
        <div className="flex items-center justify-between gap-2 pt-1">
          <span className="text-[11px] text-muted-foreground">再点一次按钮也行 · Esc 取消</span>
          <Button size="sm" className="h-7" autoFocus onClick={confirm}>
            {edit ? "回退并改写" : "回退"}
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
