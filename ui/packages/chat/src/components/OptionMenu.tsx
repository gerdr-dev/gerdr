import { Check, ChevronDown, Pin, SlidersHorizontal } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { cn } from "../utils";
import { t } from "../i18n";

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
  actual,
}: {
  /** The value really in effect, when it isn't value (OptGroup.actual) */
  actual?: string;
  label: string;
  value: string;
  options: Opt[];
  onChange: (v: string) => void;
  defaultValue?: string;
  onSetDefault?: () => void;
}) {
  const cur = options.find((o) => o.value === value);
  const act = actual !== undefined && actual !== value ? options.find((o) => o.value === actual) : undefined;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className="flex h-7 min-w-0 max-w-44 items-center gap-1 rounded-md px-1.5 text-[12px] max-md:max-w-none max-md:shrink-0 text-muted-foreground outline-none hover:bg-muted hover:text-foreground data-[state=open]:bg-muted">
        {/* Not in effect yet: the chosen name with a "*" (the one running is in the menu; "Default → Auto" took a
            phone's whole row) */}
        <span className={cn("truncate", act && "text-warn")}>
          {cur?.name ?? (value || label)}
          {act && "*"}
        </span>
        {/* 手机上省掉箭头：底栏就这么宽，留给名字 */}
        <ChevronDown className="size-3 shrink-0 opacity-60 max-md:hidden" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-80 w-72 overflow-auto" onCloseAutoFocus={(e) => e.preventDefault()}>
        <DropdownMenuLabel className="text-[11px] font-normal text-muted-foreground">{label}</DropdownMenuLabel>
        {options.map((o) => (
          <DropdownMenuItem key={o.value || "__"} onClick={() => onChange(o.value)} className="items-start gap-2">
            <Check className={cn("mt-0.5 size-3.5 shrink-0", o.value === value ? "opacity-100" : "opacity-0")} />
            <div className="min-w-0">
              <div className="text-[13px]">
                {o.name}
                {act && <ActualTag value={o.value} actual={act.value} chosen={value} />}
              </div>
              {o.description && <div className="text-[11px] leading-snug text-muted-foreground">{o.description}</div>}
            </div>
          </DropdownMenuItem>
        ))}
        {onSetDefault && <SetDefaultItem same={value === defaultValue} onClick={onSetDefault} />}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

const actualOf = (g: OptGroup) => g.actual !== undefined && g.actual !== g.value && g.options.some((o) => o.value === g.actual);

// The value in effect isn't the one chosen (OptGroup.actual): a tag on each of the two items, the one running now and
// the chosen one that comes next. Not a line under the title: it took two lines and pushed the menu into scrolling.
function ActualTag({ value, actual, chosen }: { value: string; actual: string; chosen: string }) {
  if (value !== actual && value !== chosen) return null;
  const now = value === actual;
  return (
    <span className={cn("ml-1.5 rounded px-1 text-[10px]", now ? "bg-warn/15 text-warn" : "bg-muted text-muted-foreground")}>
      {t(now ? "options.actual" : "options.next")}
    </span>
  );
}

function SetDefaultItem({ same, onClick, all }: { same: boolean; onClick: () => void; all?: boolean }) {
  return (
    <>
      <DropdownMenuSeparator />
      <DropdownMenuItem disabled={same} onClick={onClick} className="gap-2 text-[12px] text-muted-foreground">
        <Pin className="size-3.5" />
        {same ? t("options.isDefault") : all ? t("options.setAllDefault") : t("options.setDefault")}
      </DropdownMenuItem>
    </>
  );
}

export type OptGroup = {
  id: string;
  label: string;
  value: string;
  options: Opt[];
  onChange: (v: string) => void;
  defaultValue?: string;
  /** The value really in effect when it isn't value (ChatSession.turn.mode): the agent takes the choice later */
  actual?: string;
};

// 值得一直看得见的模式：只做计划 / 只读（agent 不动文件，以为它在改其实没改）、全放开（不再问就动手）。
// 其它模式（默认、自动、接受编辑）平时就那样，收起来不占地方
const LOUD_MODE = /plan|read-?only|bypass|full-access|dontAsk/i;

// The model and effort as one button (desktop: the mode stays its own dropdown); on a phone the mode folds in too.
// 手机上输入框底栏放不下模式 / 模型 / 思考强度三个下拉（挤得要横着滑）：收成一个按钮，
// 点开是一张按组排的单列菜单 —— 不用子菜单，触屏上子菜单往右弹会出屏幕。
// 按钮上写模型和思考强度（「Opus 5.5 · medium」，要一眼看得到在用哪个）；模式平时就那样，只有落在 LOUD_MODE 里才写出来。
export function OptionsMenu({
  groups,
  mode,
  onSetDefault,
  short,
  chevron,
}: {
  groups: OptGroup[];
  /** The full (desktop) row: the model + effort menu, with the same arrow as the single dropdowns */
  chevron?: boolean;
  mode?: OptGroup;
  onSetDefault?: () => void;
  /** No room on the row: 1 = model and a loud mode, 2 = only the model (the rest is one tap away in the menu) */
  short?: number;
}) {
  const all = mode ? [mode, ...groups] : groups;
  const nameOf = (g: OptGroup) => g.options.find((o) => o.value === g.value)?.name ?? g.value;
  // At 2 the mode goes too: "Full access" takes half a phone's row
  // A mode not in effect yet shows whatever it is, as "Auto*": the menu says which one is running
  const modeName = !mode ? undefined : actualOf(mode) ? `${nameOf(mode)}*` : (short ?? 0) < 2 && LOUD_MODE.test(mode.value) ? nameOf(mode) : undefined;
  const shown = short ? groups.filter((g) => g.id === "model").slice(0, 1) : groups;
  const summary = (shown.length ? shown : groups.slice(0, 1)).map(nameOf).filter(Boolean).join(" · ");
  if (!all.length) return null;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className={cn(
          "flex h-7 min-w-0 items-center gap-1.5 rounded-md px-1.5 text-[12px] text-muted-foreground outline-none hover:bg-muted hover:text-foreground data-[state=open]:bg-muted",
        )}
      >
        {!summary && !modeName && <SlidersHorizontal className="size-4" />}
        {modeName && <span className={cn("max-w-28 shrink-0 truncate", mode && actualOf(mode) && "text-warn")}>{modeName}</span>}
        {summary && <span className="truncate">{summary}</span>}
        {chevron && <ChevronDown className="-ml-0.5 size-3 shrink-0 opacity-60" />}
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
                  <div className="text-[13px]">
                    {o.name}
                    {actualOf(g) && <ActualTag value={o.value} actual={g.actual!} chosen={g.value} />}
                  </div>
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
