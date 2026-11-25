import { Check, ChevronDown, Pin, SlidersHorizontal } from "lucide-react";
import { toast } from "sonner";
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
  agentName,
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
  agentName: string;
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
        {onSetDefault && <SetDefaultItem agent={agentName} values={cur?.name ?? value} same={value === defaultValue} onClick={onSetDefault} />}
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

function SetDefaultItem({ agent, values, same, onClick, all }: { agent: string; values: string; same: boolean; onClick: () => void; all?: boolean }) {
  return (
    <>
      <DropdownMenuSeparator />
      <DropdownMenuItem
        disabled={same}
        onClick={() => {
          onClick();
          toast.success(t("options.defaultSaved", { agent, values }));
        }}
        className="gap-2 text-[12px] text-muted-foreground"
      >
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

// Modes worth keeping in sight longest: plan / read-only (the agent changes no files while you think it does) and the
// wide-open ones (acts without asking). The everyday ones (default, auto, accept edits) are the first to go when the row
// runs out of room
const LOUD_MODE = /plan|read-?only|bypass|full-access|dontAsk/i;

// The model and effort as one button (desktop: the mode stays its own dropdown); on a phone the mode folds in too.
// 手机上输入框底栏放不下模式 / 模型 / 思考强度三个下拉（挤得要横着滑）：收成一个按钮，
// 点开是一张按组排的单列菜单 —— 不用子菜单，触屏上子菜单往右弹会出屏幕。
// The button names the mode, model and effort ("Auto  Opus 5.5 · medium") while they fit; with no room they drop out
// one step at a time (`short`), a LOUD_MODE mode staying longer than an everyday one.
export function OptionsMenu({
  agentName,
  groups,
  mode,
  onSetDefault,
  short,
  chevron,
}: {
  agentName: string;
  groups: OptGroup[];
  /** The full (desktop) row: the model + effort menu, with the same arrow as the single dropdowns */
  chevron?: boolean;
  mode?: OptGroup;
  onSetDefault?: () => void;
  /** No room on the row: 1 = an everyday mode goes (the effort says more, asked for), 2 = the effort
   *  goes, 3 = a loud mode goes, leaving only the model (the rest is one tap away in the menu) */
  short?: number;
}) {
  const all = mode ? [mode, ...groups] : groups;
  const nameOf = (g: OptGroup) => g.options.find((o) => o.value === g.value)?.name ?? g.value;
  // At 3 even a loud mode goes: "Full access" takes half a phone's row
  // A mode not in effect yet shows whatever it is, as "Auto*": the menu says which one is running
  const lvl = short ?? 0;
  const modeName = !mode ? undefined : actualOf(mode) ? `${nameOf(mode)}*` : lvl < 1 || (lvl < 3 && LOUD_MODE.test(mode.value)) ? nameOf(mode) : undefined;
  const shown = lvl >= 2 ? groups.filter((g) => g.id === "model").slice(0, 1) : groups;
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
        {/* The same separator as between the model and its effort: without it "Auto Opus 5.5" reads as one name */}
        {modeName && summary && <span className="-mx-0.5 shrink-0">·</span>}
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
        {onSetDefault && (
          <SetDefaultItem
            all
            agent={agentName}
            values={all.map(nameOf).filter(Boolean).join(" · ")}
            same={all.every((g) => g.value === g.defaultValue)}
            onClick={onSetDefault}
          />
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
