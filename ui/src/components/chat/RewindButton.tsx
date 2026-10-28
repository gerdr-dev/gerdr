import { Pencil, RotateCcw } from "lucide-react";
import { useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Popover, PopoverAnchor, PopoverContent } from "@/components/ui/popover";
import { cn } from "@/lib/utils";

// 用户消息旁边的「改这句重发」「回到这句之前」：点一下在按钮旁边冒一个小气泡（不是全屏弹窗，不挡对话），
// 再点一次同一个按钮 / 回车 / 点气泡里的按钮 = 确认，Esc 或点别处 = 算了（用户点名要「动静更小」）。
// 不用长按：触控板上按不准，手机上又和选字抢手势。
// 只回退对话，不动文件（以前的文件快照坑太多，删了）。

export function RewindButton({ edit, onConfirm }: { edit: boolean; onConfirm: () => void }) {
  const [open, setOpen] = useState(false);
  const btnRef = useRef<HTMLButtonElement>(null);

  const confirm = () => {
    setOpen(false);
    onConfirm();
  };
  const Icon = edit ? Pencil : RotateCcw;
  const label = edit ? "改这句重发" : "回到这句之前";

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <button
          ref={btnRef}
          data-state={open ? "open" : "closed"}
          data-rewind={edit ? "edit" : "rewind"}
          title={open ? "再点一次确认" : label}
          className={cn("rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground", open && "bg-brand/15 text-brand hover:bg-brand/20 hover:text-brand")}
          onClick={() => (open ? confirm() : setOpen(true))}
        >
          <Icon className="size-3.5" />
        </button>
      </PopoverAnchor>
      <PopoverContent
        side="top"
        align="end"
        className="w-72 space-y-2 p-3 text-[12px]"
        // 不抢走「再点一次」：只有点自己这个按钮不算点到外面。
        // 以前按 [data-state="open"] 判断，旁边那个按钮 / 外层元素也会命中，两个气泡就能同时开着；
        // 「改这句重发」和「回到这句之前」是二选一，点另一个就得把这个关掉。
        onInteractOutside={(e) => {
          if (btnRef.current?.contains(e.target as Node)) e.preventDefault();
        }}
      >
        <div className="text-[13px] font-medium">{label}？</div>
        <div className="text-muted-foreground">
          这句和它之后的对话都会丢掉，agent 也会忘掉。文件不会恢复。{edit ? "原话放回输入框，改完再发。" : ""}
        </div>
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
