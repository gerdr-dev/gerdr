import { Plus, RotateCcw, Search, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { captureKeys } from "@/lib/commands";
import { ACTION_TITLES, DEFAULT_KEYS, KEY_GROUPS, comboOf, formatCombo, matches, parseCombo, type KeyAction } from "@/lib/keys";
import { isNative } from "@/lib/native";
import { askConfirm } from "@/lib/confirm";
import { keyFor, setConfig, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";

// 设置 → 快捷键。原来是每个动作一个文本框，手打「prefix+shift+-」这种写法，错了也不知道；⌘ 键还压根不在表里。
// 现在：按分组列、键画成按键的样子，点 + 直接按下想要的组合（要用前缀就先按前缀再按键），撞了别的动作标黄。

type Target = KeyAction | "prefix";
type Rec = { target: Target; afterPrefix: boolean };

/** 设置页里 tab1 / ws1 展开成 1–9 各一行（帮助里合成一行）。 */
const ROWS: [string, KeyAction[]][] = KEY_GROUPS.map(([g, acts]) => [
  g,
  acts.flatMap((a) => (a === "tab1" || a === "ws1" ? ([1, 2, 3, 4, 5, 6, 7, 8, 9].map((i) => `${a.slice(0, -1)}${i}`) as KeyAction[]) : [a])),
]);

const same = (a: string[], b: string[]) => a.length === b.length && a.every((x, i) => x === b[i]);

export function KeySettings() {
  const cfg = useStore((s) => s.config);
  const [q, setQ] = useState("");
  const [rec, setRec] = useState<Rec | null>(null);

  const setList = (a: KeyAction, list: string[]) => {
    const keys = { ...cfg.keys };
    if (same(list, DEFAULT_KEYS[a])) delete keys[a];
    else keys[a] = list;
    setConfig({ keys });
  };

  // 录键：按键全交给这里（captureKeys 让全局的快捷键先别动）；点别处 / Esc 取消
  useEffect(() => {
    if (!rec) return;
    const off = captureKeys((e) => {
      if (e.key === "Escape" && !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey) return setRec(null);
      const combo = comboOf(e);
      if (rec.target === "prefix") {
        setConfig({ prefix: combo });
        return setRec(null);
      }
      const prefix = parseCombo(cfg.prefix);
      if (!rec.afterPrefix && prefix && matches(prefix, e)) return setRec({ ...rec, afterPrefix: true });
      const k = rec.afterPrefix ? `prefix+${combo}` : combo;
      const cur = keyFor(cfg, rec.target);
      if (!cur.includes(k)) setList(rec.target, [...cur, k]);
      setRec(null);
    });
    const cancel = () => setRec(null);
    window.addEventListener("pointerdown", cancel);
    return () => {
      off();
      window.removeEventListener("pointerdown", cancel);
    };
  }, [rec, cfg]);

  // 同一个键绑了几个动作：标出来（按到时只有排在前面的那个生效）
  const owners = useMemo(() => {
    const m = new Map<string, KeyAction[]>();
    for (const a of Object.keys(DEFAULT_KEYS) as KeyAction[])
      for (const k of keyFor(cfg, a)) {
        const n = formatCombo(k);
        m.set(n, [...(m.get(n) ?? []), a]);
      }
    return m;
  }, [cfg]);

  const needle = q.trim().toLowerCase();
  const groups = ROWS.map(([g, acts]) => [
    g,
    acts.filter(
      (a) =>
        !needle ||
        ACTION_TITLES[a].toLowerCase().includes(needle) ||
        keyFor(cfg, a).some((k) => formatCombo(k).toLowerCase().includes(needle) || k.includes(needle)),
    ),
  ]) as [string, KeyAction[]][];

  const recLabel = (r: Rec) => (r.afterPrefix ? `${formatCombo(cfg.prefix)} 之后再按一个键…` : "按下组合键…");

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-4">
        <div>
          <div className="text-[13px]">前缀键</div>
          <div className="text-[12px] text-muted-foreground">先按它松开，再按后面那个键（和 tmux / herdr 一样）。默认 ⌃B</div>
        </div>
        <div className="flex items-center gap-1">
          {rec?.target === "prefix" ? (
            <RecChip label="按下组合键…" />
          ) : (
            <Chip label={formatCombo(cfg.prefix)} onClick={() => setRec({ target: "prefix", afterPrefix: false })} />
          )}
          {cfg.prefix !== "ctrl+b" && (
            <Button variant="ghost" size="icon" className="size-6 text-muted-foreground" title="恢复默认" onClick={() => setConfig({ prefix: "ctrl+b" })}>
              <RotateCcw className="size-3.5" />
            </Button>
          )}
        </div>
      </div>

      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input className="h-8 pl-7 text-[13px]" placeholder="搜动作或按键" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        {Object.keys(cfg.keys).length > 0 && (
          <Button
            variant="outline"
            size="sm"
            className="h-8"
            onClick={async () => (await askConfirm({ title: "所有快捷键恢复成默认？", action: "恢复默认" })) && setConfig({ keys: {} })}
          >
            全部恢复默认
          </Button>
        )}
      </div>
      <div className="text-[12px] text-muted-foreground">
        点 <Plus className="inline size-3" /> 再按下想要的键；要用前缀就先按前缀再按键。点键帽上的 × 删掉。
        {!isNative() && " ⌘T、⌘W、⌘N、⌘1–9 这几个在浏览器里被浏览器自己占着，只在 App 里管用。"}
      </div>

      {groups.map(
        ([g, acts]) =>
          acts.length > 0 && (
            <div key={g}>
              <div className="mb-1 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">{g}</div>
              <div className="divide-y">
                {acts.map((a) => {
                  const list = keyFor(cfg, a);
                  const changed = !same(list, DEFAULT_KEYS[a]);
                  return (
                    <div key={a} className="flex items-center gap-3 py-1.5">
                      <span className="flex-1 text-[13px]">
                        {ACTION_TITLES[a]}
                        {changed && <span className="ml-1.5 text-[11px] text-muted-foreground">已改</span>}
                      </span>
                      <div className="flex flex-wrap items-center justify-end gap-1">
                        {list.map((k) => {
                          const others = (owners.get(formatCombo(k)) ?? []).filter((x) => x !== a);
                          return (
                            <Chip
                              key={k}
                              label={formatCombo(k)}
                              warn={others.length > 0 ? `也绑给了「${others.map((x) => ACTION_TITLES[x]).join("」「")}」` : undefined}
                              onRemove={() =>
                                setList(
                                  a,
                                  list.filter((x) => x !== k),
                                )
                              }
                            />
                          );
                        })}
                        {rec?.target === a ? (
                          <RecChip label={recLabel(rec)} />
                        ) : (
                          <Button
                            variant="ghost"
                            size="icon"
                            className="size-6 text-muted-foreground"
                            title="加一个键"
                            onClick={() => setRec({ target: a, afterPrefix: false })}
                          >
                            <Plus className="size-3.5" />
                          </Button>
                        )}
                        <Button
                          variant="ghost"
                          size="icon"
                          className={cn("size-6 text-muted-foreground", !changed && "invisible")}
                          title="恢复默认"
                          onClick={() => setList(a, DEFAULT_KEYS[a])}
                        >
                          <RotateCcw className="size-3.5" />
                        </Button>
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          ),
      )}
      {groups.every(([, acts]) => acts.length === 0) && <div className="py-6 text-center text-[13px] text-muted-foreground">没有匹配的快捷键</div>}
    </div>
  );
}

function Chip({ label, warn, onClick, onRemove }: { label: string; warn?: string; onClick?: () => void; onRemove?: () => void }) {
  return (
    <span
      title={warn}
      onClick={onClick}
      className={cn(
        "group/chip inline-flex h-6 items-center gap-0.5 rounded-md border bg-muted/50 px-1.5 font-mono text-[12px]",
        warn && "border-warn/60 text-warn",
        onClick && "cursor-pointer hover:bg-muted",
      )}
    >
      {label}
      {onRemove && (
        <button
          className="-mr-0.5 rounded-sm text-muted-foreground opacity-0 group-hover/chip:opacity-100 hover:text-foreground [@media(hover:none)]:opacity-100"
          title="删掉这个键"
          onClick={onRemove}
        >
          <X className="size-3" />
        </button>
      )}
    </span>
  );
}

function RecChip({ label }: { label: string }) {
  return (
    <span
      // 点在录键的框上不算「点别处」
      onPointerDown={(e) => e.stopPropagation()}
      className="inline-flex h-6 animate-pulse items-center rounded-md border border-brand px-1.5 text-[12px] text-brand"
    >
      {label}
      <span className="ml-1.5 text-muted-foreground">Esc 取消</span>
    </span>
  );
}
