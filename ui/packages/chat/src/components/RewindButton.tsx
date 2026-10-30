import { RotateCcw } from "lucide-react";
import { useRef, useState } from "react";
import { Button } from "../ui/button";
import { Popover, PopoverAnchor, PopoverContent } from "../ui/popover";
import { cn } from "../utils";
import { t } from "../i18n";

// 用户消息旁边的「回到这句之前」：点一下在按钮旁边冒一个小气泡（不是全屏弹窗，不挡对话），
// 再点一次同一个按钮 / 回车 / 点气泡里的按钮 = 确认，Esc 或点别处 = 算了（用户点名要「动静更小」）。
// 不用长按：触控板上按不准，手机上又和选字抢手势。
// 只回退对话，不动文件（以前的文件快照坑太多，删了）。原话放回输入框，改不改、发不发人自己定 ——
// 以前另有一个「改这句重发」，两个做的事一样（回退都会放回原话），合成了这一个。

export function RewindButton({ onConfirm }: { onConfirm: () => void }) {
  const [open, setOpen] = useState(false);
  const btnRef = useRef<HTMLButtonElement>(null);

  const confirm = () => {
    setOpen(false);
    onConfirm();
  };
  const label = t("rewind.label");

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverAnchor asChild>
        <button
          ref={btnRef}
          data-state={open ? "open" : "closed"}
          title={open ? t("rewind.clickAgain") : label}
          className={cn(
            "rounded-sm p-1 text-muted-foreground hover:bg-muted hover:text-foreground",
            open && "bg-brand/15 text-brand hover:bg-brand/20 hover:text-brand",
          )}
          onClick={() => (open ? confirm() : setOpen(true))}
        >
          <RotateCcw className="size-3.5" />
        </button>
      </PopoverAnchor>
      <PopoverContent
        side="top"
        align="end"
        className="w-72 space-y-2 p-3 text-[12px]"
        // 不抢走「再点一次」：只有点自己这个按钮不算点到外面。
        // 别按 [data-state="open"] 判断：外层元素也会命中，点别的消息的按钮时这个气泡关不掉。
        onInteractOutside={(e) => {
          if (btnRef.current?.contains(e.target as Node)) e.preventDefault();
        }}
      >
        <div className="text-[13px] font-medium">{t("rewind.question")}</div>
        <div className="text-muted-foreground">{t("rewind.explain")}</div>
        <div className="flex items-center justify-between gap-2 pt-1">
          <span className="text-[11px] text-muted-foreground">{t("rewind.hint")}</span>
          <Button size="sm" className="h-7" autoFocus onClick={confirm}>
            {t("rewind.confirm")}
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}
