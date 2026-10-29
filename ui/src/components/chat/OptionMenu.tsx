import { Check, ChevronDown, Pin, SlidersHorizontal } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

export type Opt = { value: string; name: string; description?: string };

// 输入框底下那几个小下拉（模式 / 模型 / 思考强度）。不用 Select：Select 的触发器会把选项里的
// 说明文字也一起显示出来，挤成两行；这里触发器只显示名字，说明只在展开的列表里。
// 列表底下的「设为新会话的默认」：把这一项现在的值存成这种 agent 新开会话时的默认（lib/defaults.ts）。
export function OptionMenu({
  label,
  value,
  options,
  onChange,
  defaultValue,
  onSetDefault,
}: {
  label: string;
  value: string;
  options: Opt[];
  onChange: (v: string) => void;
  defaultValue?: string;
  onSetDefault?: () => void;
}) {
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
        {onSetDefault && <SetDefaultItem same={value === defaultValue} onClick={onSetDefault} />}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function SetDefaultItem({ same, onClick, all }: { same: boolean; onClick: () => void; all?: boolean }) {
  return (
    <>
      <DropdownMenuSeparator />
      <DropdownMenuItem disabled={same} onClick={onClick} className="gap-2 text-[12px] text-muted-foreground">
        <Pin className="size-3.5" />
        {same ? "已是新会话的默认" : all ? "把这些设为新会话的默认" : "设为新会话的默认"}
      </DropdownMenuItem>
    </>
  );
}

export type OptGroup = { id: string; label: string; value: string; options: Opt[]; onChange: (v: string) => void; defaultValue?: string };

// 值得一直看得见的模式：只做计划 / 只读（agent 不动文件，以为它在改其实没改）、全放开（不再问就动手）。
// 其它模式（默认、自动、接受编辑）平时就那样，收起来不占地方
const LOUD_MODE = /plan|read-?only|bypass|full-access|dontAsk/i;

// 手机上输入框底栏放不下模式 / 模型 / 思考强度三个下拉（挤得要横着滑）：收成一个按钮，
// 点开是一张按组排的单列菜单 —— 不用子菜单，触屏上子菜单往右弹会出屏幕。
// 按钮上写模型和思考强度（「Opus 5.5 · medium」，要一眼看得到在用哪个）；模式平时就那样，只有落在 LOUD_MODE 里才写出来。
export function OptionsMenu({ groups, mode, onSetDefault }: { groups: OptGroup[]; mode?: OptGroup; onSetDefault?: () => void }) {
  const all = mode ? [mode, ...groups] : groups;
  const nameOf = (g: OptGroup) => g.options.find((o) => o.value === g.value)?.name ?? g.value;
  const modeName = mode && LOUD_MODE.test(mode.value) ? nameOf(mode) : undefined;
  const summary = groups.map(nameOf).filter(Boolean).join(" · ");
  if (!all.length) return null;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className={cn(
          "flex h-7 min-w-0 items-center gap-1.5 rounded-md px-1.5 text-[12px] text-muted-foreground outline-none hover:bg-muted hover:text-foreground data-[state=open]:bg-muted",
        )}
      >
        {!summary && !modeName && <SlidersHorizontal className="size-4" />}
        {modeName && <span className="max-w-28 shrink-0 truncate text-warn">{modeName}</span>}
        {summary && <span className="truncate">{summary}</span>}
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start" className="max-h-[60vh] w-72 overflow-auto" onCloseAutoFocus={(e) => e.preventDefault()}>
        {all.map((g, i) => (
          <DropdownMenuGroup key={g.id}>
            {i > 0 && <DropdownMenuSeparator />}
            <DropdownMenuLabel className="text-[11px] font-normal text-muted-foreground">{g.label}</DropdownMenuLabel>
            {g.options.map((o) => (
              <DropdownMenuItem key={o.value || "__"} onClick={() => g.onChange(o.value)} className="items-start gap-2">
                <Check className={cn("mt-0.5 size-3.5 shrink-0", o.value === g.value ? "opacity-100" : "opacity-0")} />
                <div className="min-w-0">
                  <div className="text-[13px]">{o.name}</div>
                  {o.description && <div className="text-[11px] leading-snug text-muted-foreground">{o.description}</div>}
                </div>
              </DropdownMenuItem>
            ))}
          </DropdownMenuGroup>
        ))}
        {onSetDefault && <SetDefaultItem all same={all.every((g) => g.value === g.defaultValue)} onClick={onSetDefault} />}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
