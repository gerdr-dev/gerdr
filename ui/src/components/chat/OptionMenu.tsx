import { Check, ChevronDown } from "lucide-react";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

export type Opt = { value: string; name: string; description?: string };

// 输入框底下那几个小下拉（模式 / 模型 / 思考强度）。不用 Select：Select 的触发器会把选项里的
// 说明文字也一起显示出来，挤成两行；这里触发器只显示名字，说明只在展开的列表里。
export function OptionMenu({ label, value, options, onChange }: { label: string; value: string; options: Opt[]; onChange: (v: string) => void }) {
  const cur = options.find((o) => o.value === value);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className="flex h-7 min-w-0 max-w-44 items-center gap-1 rounded-md px-2 text-[12px] max-md:max-w-none max-md:shrink-0 max-md:px-1.5 text-muted-foreground outline-none hover:bg-muted hover:text-foreground data-[state=open]:bg-muted">
        <span className="truncate">{cur?.name ?? (value || label)}</span>
        {/* 手机上省掉箭头：底栏就这么宽，留给名字 */}
        <ChevronDown className="size-3 shrink-0 opacity-60 max-md:hidden" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-80 w-72 overflow-auto" onCloseAutoFocus={(e) => e.preventDefault()}>
        <DropdownMenuLabel className="text-[11px] font-normal text-muted-foreground">{label}</DropdownMenuLabel>
        {options.map((o) => (
          <DropdownMenuItem key={o.value || "__"} onClick={() => onChange(o.value)} className="items-start gap-2">
            <Check className={cn("mt-0.5 size-3.5 shrink-0", o.value === value ? "opacity-100" : "opacity-0")} />
            <div className="min-w-0">
              <div className="text-[13px]">{o.name}</div>
              {o.description && <div className="text-[11px] leading-snug text-muted-foreground">{o.description}</div>}
            </div>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
