import { Plus, RotateCcw, Search, X } from "lucide-react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { captureKeys } from "@/lib/commands";
import { addonCommands, addonStatus, type AddonCommand } from "@/lib/addons";
import { ACTION_TITLES, DEFAULT_KEYS, KEY_GROUPS, LONE_KEYS, comboOf, formatCombo, isModifierOnly, matches, parseCombo, type KeyAction } from "@/lib/keys";
import { isNative } from "@/lib/native";
import { askConfirm } from "@/lib/confirm";
import { addonKeyFor, addonKeyId, keyFor, setConfig, useStore, type Config } from "@/lib/store";
import { cn } from "@/lib/utils";
import { t } from "@/lib/i18n";

// 设置 → 快捷键。原来是每个动作一个文本框，手打「prefix+shift+-」这种写法，错了也不知道；⌘ 键还压根不在表里。
// 现在：按分组列、键画成按键的样子，点 + 直接按下想要的组合（要用前缀就先按前缀再按键），撞了别的动作标黄。

/** Settings spells tab1 / active1 / ws1 out as one row per 1–9 (the help folds them into one). */
const NUMBERED = new Set<KeyAction>(["tab1", "active1", "ws1"]);
const ROWS: [string, KeyAction[]][] = KEY_GROUPS.map(([g, acts]) => [
  g,
  acts.flatMap((a) => (NUMBERED.has(a) ? ([1, 2, 3, 4, 5, 6, 7, 8, 9].map((i) => `${a.slice(0, -1)}${i}`) as KeyAction[]) : [a])),
]);

const same = (a: string[], b: string[]) => a.length === b.length && a.every((x, i) => x === b[i]);

/** One entry of config.keys back to its default (dropped) or to `list`. */
function saveKeys(cfg: Config, id: string, list: string[], defaults: string[]) {
  const keys = { ...cfg.keys };
  if (same(list, defaults)) delete keys[id];
  else keys[id] = list;
  setConfig({ keys });
}

/** Every bound key → the titles of what it runs: built-ins first (they win a clash), then add-on commands. */
function useOwners(cfg: Config) {
  const ver = useStore((s) => s.addonVersion);
  return useMemo(() => {
    const m = new Map<string, string[]>();
    const add = (k: string, title: string) => {
      const n = formatCombo(k);
      m.set(n, [...(m.get(n) ?? []), title]);
    };
    for (const a of Object.keys(DEFAULT_KEYS) as KeyAction[]) for (const k of keyFor(cfg, a)) add(k, ACTION_TITLES[a]);
    for (const c of addonCommands) for (const k of addonKeyFor(cfg, c.addon, c.id, c.key)) add(k, c.title);
    return m;
  }, [cfg, ver]);
}

export function KeySettings() {
  const cfg = useStore((s) => s.config);
  useStore((s) => s.addonVersion); // add-on commands come and go
  const [q, setQ] = useState("");
  const [recPrefix, setRecPrefix] = useState(false);
  const owners = useOwners(cfg);

  // The prefix: the next key pressed, whatever it is (captureKeys keeps the global shortcuts out of it); Esc / a click elsewhere cancels
  useEffect(() => {
    if (!recPrefix) return;
    const off = captureKeys((e) => {
      if (!(e.key === "Escape" && !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey)) setConfig({ prefix: comboOf(e) });
      setRecPrefix(false);
    });
    const cancel = () => setRecPrefix(false);
    window.addEventListener("pointerdown", cancel);
    return () => {
      off();
      window.removeEventListener("pointerdown", cancel);
    };
  }, [recPrefix]);

  const needle = q.trim().toLowerCase();
  const hit = (title: string, keys: string[]) =>
    !needle || title.toLowerCase().includes(needle) || keys.some((k) => formatCombo(k).toLowerCase().includes(needle) || k.includes(needle));
  const groups = ROWS.map(([g, acts]) => [g, acts.filter((a) => hit(ACTION_TITLES[a], keyFor(cfg, a)))]) as [string, KeyAction[]][];
  // Add-ons' commands, after the built-in groups: their keys are kept in the same config.keys (addonKeyId)
  const addonRows = addonCommands.filter((c) => hit(c.title, addonKeyFor(cfg, c.addon, c.id, c.key)));

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-4">
        <div>
          <div className="text-[13px]">{t("keySettings.prefix")}</div>
          <div className="text-[12px] text-muted-foreground">{t("keySettings.prefixHint")}</div>
        </div>
        <div className="flex items-center gap-1">
          {recPrefix ? <RecChip label={t("keySettings.recPress")} /> : <Chip label={formatCombo(cfg.prefix)} onClick={() => setRecPrefix(true)} />}
          {cfg.prefix !== "ctrl+b" && (
            <Button
              variant="ghost"
              size="icon"
              className="size-6 text-muted-foreground"
              title={t("keySettings.reset")}
              onClick={() => setConfig({ prefix: "ctrl+b" })}
            >
              <RotateCcw className="size-3.5" />
            </Button>
          )}
        </div>
      </div>

      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input className="h-8 pl-7 text-[13px]" placeholder={t("keySettings.search")} value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        {Object.keys(cfg.keys).length > 0 && (
          <Button
            variant="outline"
            size="sm"
            className="h-8"
            onClick={async () => (await askConfirm({ title: t("keySettings.resetAllConfirm"), action: t("keySettings.reset") })) && setConfig({ keys: {} })}
          >
            {t("keySettings.resetAll")}
          </Button>
        )}
      </div>
      <div className="text-[12px] text-muted-foreground">
        {t("keySettings.howToBefore")} <Plus className="inline size-3" /> {t("keySettings.howToAfter")}
        {!isNative() && t("keySettings.browserNote")}
      </div>
      {/* actions/runAction.ts: each view has its own table, a key it lacks only goes back to Code */}
      <div className="text-[12px] text-muted-foreground">{t("keySettings.viewNote")}</div>

      {groups.map(
        ([g, acts]) =>
          acts.length > 0 && (
            <div key={g}>
              <div className="mb-1 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">{g}</div>
              <div className="divide-y">
                {acts.map((a) => (
                  <KeyRow
                    key={a}
                    name={ACTION_TITLES[a]}
                    list={keyFor(cfg, a)}
                    defaults={DEFAULT_KEYS[a]}
                    onChange={(list) => saveKeys(cfg, a, list, DEFAULT_KEYS[a])}
                    owners={owners}
                  />
                ))}
              </div>
            </div>
          ),
      )}
      {addonRows.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">{t("keySettings.groupAddons")}</div>
          <div className="divide-y">
            {addonRows.map((c) => (
              <AddonKeyRow key={`${c.addon}/${c.id}`} cmd={c} owners={owners} source />
            ))}
          </div>
        </div>
      )}
      {groups.every(([, acts]) => acts.length === 0) && addonRows.length === 0 && (
        <div className="py-6 text-center text-[13px] text-muted-foreground">{t("keySettings.noMatch")}</div>
      )}
    </div>
  );
}

/** An add-on command's row. source: say which add-on it is from (Settings → Shortcuts, not the add-on's own settings). */
function AddonKeyRow({ cmd, owners, source, title }: { cmd: AddonCommand; owners: Map<string, string[]>; source?: boolean; title?: string }) {
  const cfg = useStore((s) => s.config);
  const addon = source ? addonStatus.find((a) => a.id === cmd.addon) : undefined;
  const defaults = cmd.key ? [cmd.key] : [];
  return (
    <KeyRow
      name={cmd.title}
      title={
        <>
          {title ?? cmd.title}
          {cmd.release && (
            <span className="ml-1.5 text-[11px] text-muted-foreground" title={t("keySettings.holdHint")}>
              {t("keySettings.holdNote")}
            </span>
          )}
          {source && <span className="ml-1.5 text-[11px] text-muted-foreground">· {addon?.name ?? cmd.addon}</span>}
        </>
      }
      list={addonKeyFor(cfg, cmd.addon, cmd.id, cmd.key)}
      defaults={defaults}
      onChange={(list) => saveKeys(cfg, addonKeyId(cmd.addon, cmd.id), list, defaults)}
      owners={owners}
      lone={!!cmd.release}
    />
  );
}

/**
 * g.ui.Shortcut: an add-on command's keys inside the add-on's own settings, the same row (and the same config entry)
 * as in Settings → Shortcuts, so either place changes both.
 */
export function AddonShortcut({ addon, command, title, description }: { addon: string; command: string; title?: string; description?: string }) {
  const cfg = useStore((s) => s.config);
  useStore((s) => s.addonVersion);
  const owners = useOwners(cfg);
  const cmd = addonCommands.find((c) => c.addon === addon && c.id === command);
  if (!cmd) return null;
  return (
    <div className="text-[12px]">
      <AddonKeyRow cmd={cmd} owners={owners} title={title} />
      {description && <div className="text-[11px] text-muted-foreground">{description}</div>}
    </div>
  );
}

/**
 * One action's keys: each as a chip (× removes it), + records another (to use the prefix, press it first), and the
 * reset arrow once changed. lone: a modifier pressed and let go by itself is recorded too ("altright"), for held
 * commands (keys.ts LONE_KEYS). A key also bound elsewhere is marked.
 */
function KeyRow({
  name,
  title,
  list,
  defaults,
  onChange,
  owners,
  lone,
}: {
  /** What owners calls it (the title as text). */
  name: string;
  title?: ReactNode;
  list: string[];
  defaults: string[];
  onChange: (list: string[]) => void;
  owners: Map<string, string[]>;
  lone?: boolean;
}) {
  const cfg = useStore((s) => s.config);
  const [rec, setRec] = useState<{ afterPrefix: boolean } | null>(null);
  const changed = !same(list, defaults);

  useEffect(() => {
    if (!rec) return;
    const add = (k: string) => {
      if (!list.includes(k)) onChange([...list, k]);
      setRec(null);
    };
    // A modifier let go with nothing pressed since it went down. handleKeydown hands captureKeys only the keys that
    // aren't modifiers, so modifiers are watched here
    let solo: string | null = null;
    const off = captureKeys((e) => {
      solo = null;
      if (e.key === "Escape" && !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey) return setRec(null);
      const combo = comboOf(e);
      const prefix = parseCombo(cfg.prefix);
      if (!rec.afterPrefix && prefix && matches(prefix, e)) return setRec({ afterPrefix: true });
      add(rec.afterPrefix ? `prefix+${combo}` : combo);
    });
    const down = (e: KeyboardEvent) => {
      solo = lone && !rec.afterPrefix && isModifierOnly(e) && !e.repeat ? comboOf(e) : null;
      if (solo && !LONE_KEYS.includes(solo)) solo = null;
    };
    const up = (e: KeyboardEvent) => {
      if (solo && isModifierOnly(e) && comboOf(e) === solo) add(solo);
    };
    window.addEventListener("keydown", down, true);
    window.addEventListener("keyup", up, true);
    const cancel = () => setRec(null);
    window.addEventListener("pointerdown", cancel);
    return () => {
      off();
      window.removeEventListener("keydown", down, true);
      window.removeEventListener("keyup", up, true);
      window.removeEventListener("pointerdown", cancel);
    };
  }, [rec, cfg, list, lone]);

  const label = rec?.afterPrefix
    ? t("keySettings.recAfterPrefix", { prefix: formatCombo(cfg.prefix) })
    : lone
      ? t("keySettings.recHold")
      : t("keySettings.recPress");
  return (
    <div className="flex items-center gap-3 py-1.5">
      {/* min-w: the label has flex-basis 0, so on a phone the chips took the whole row and squeezed CJK labels to one
          character per line; with a floor the chips wrap instead */}
      <span className="min-w-28 flex-1 text-[13px]">
        {title ?? name}
        {changed && <span className="ml-1.5 text-[11px] text-muted-foreground">{t("keySettings.changed")}</span>}
      </span>
      <div className="flex flex-wrap items-center justify-end gap-1">
        {list.map((k) => {
          // owners holds this row's own name once for the key: the rest are clashes
          const others = [...(owners.get(formatCombo(k)) ?? [])];
          const self = others.indexOf(name);
          if (self >= 0) others.splice(self, 1);
          return (
            <Chip
              key={k}
              label={formatCombo(k)}
              warn={others.length > 0 ? t("keySettings.alsoBound", { actions: others.join(t("keySettings.alsoBoundSep")) }) : undefined}
              onRemove={() => onChange(list.filter((x) => x !== k))}
            />
          );
        })}
        {rec ? (
          <RecChip label={label} />
        ) : (
          <Button
            variant="ghost"
            size="icon"
            className="size-6 text-muted-foreground"
            title={t("keySettings.addKey")}
            onClick={() => setRec({ afterPrefix: false })}
          >
            <Plus className="size-3.5" />
          </Button>
        )}
        <Button
          variant="ghost"
          size="icon"
          // Unchanged rows keep the slot so the + buttons line up on desktop; a phone can't spare the blank column
          className={cn("size-6 text-muted-foreground", !changed && "invisible max-md:hidden")}
          title={t("keySettings.reset")}
          onClick={() => onChange(defaults)}
        >
          <RotateCcw className="size-3.5" />
        </Button>
      </div>
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
          title={t("keySettings.removeKey")}
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
      <span className="ml-1.5 text-muted-foreground">{t("keySettings.escCancel")}</span>
    </span>
  );
}
