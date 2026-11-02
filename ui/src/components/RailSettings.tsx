import { ArrowDown, ArrowUp, ChevronDown, Ellipsis, Plus, Puzzle, Trash2 } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { assetURL, slots, type SlotReg } from "@/lib/addons";
import {
  addGroup,
  addonMeta,
  lucideIcon,
  moveBlock,
  moveGroup,
  removeGroup,
  renameGroup,
  resolveGroups,
  setFace,
  stepBlock,
  type ResolvedGroup,
} from "@/lib/rail";
import type { RailFace } from "@/lib/store/config";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { AgentGlyph, GLYPHS, LINES, PALETTE } from "./AgentGlyph";
import { AddonParts } from "./AddonSlot";
import { FaceView } from "./RailGroups";

// Settings → 扩展 → 分组: which add-on blocks go behind which button on the rail, and what each button shows
// (lib/rail.ts). One card per button. Reordering is by menu, not drag: it works the same on a phone.

export function RailSettings() {
  useStore((s) => s.addonVersion);
  useStore((s) => s.config.rail);
  const groups = resolveGroups();
  if (!slots.sidebar.length && !slots.rail.length)
    return <div className="py-6 text-center text-[13px] text-muted-foreground">还没有带面板的插件。装一个（比如额度条）后，在这里分组。</div>;
  return (
    <div className="space-y-3">
      <p className="text-[12px] text-muted-foreground">
        把插件的面板分成几组：每组是最左边一列里的一个按钮，可以装几个插件的块，点开时按这里的顺序叠在一起。按钮上显示什么也在这里选。手机抽屉底部用的是同一套。
      </p>
      {groups.map((g, i) => (
        <GroupCard key={g.group.id} g={g} index={i} groups={groups} />
      ))}
      <Button variant="outline" size="sm" className="w-full border-dashed text-muted-foreground" onClick={() => addGroup()}>
        <Plus /> 新分组
      </Button>
    </div>
  );
}

function GroupCard({ g, index, groups }: { g: ResolvedGroup; index: number; groups: ResolvedGroup[] }) {
  const id = g.group.id;
  return (
    <div className="rounded-lg border">
      <div className="flex items-center gap-2 border-b p-2.5">
        <FacePicker face={g.group.face} blocks={g.blocks} onPick={(f) => setFace(id, f)} />
        <NameInput key={g.group.name} value={g.group.name} onSave={(v) => renameGroup(id, v)} />
        {!g.blocks.length && <span className="text-[12px] text-muted-foreground">空的，不会显示</span>}
        <div className="flex-1" />
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon-sm" aria-label="分组的操作">
              <Ellipsis />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem disabled={index === 0} onClick={() => moveGroup(id, -1)}>
              <ArrowUp /> 上移这个分组
            </DropdownMenuItem>
            <DropdownMenuItem disabled={index === groups.length - 1} onClick={() => moveGroup(id, 1)}>
              <ArrowDown /> 下移这个分组
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" disabled={groups.length < 2} onClick={() => removeGroup(id)}>
              <Trash2 /> 删除分组（里面的块移到第一个分组）
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      {g.blocks.map((b, i) => (
        <BlockRow key={b.key} b={b} first={i === 0} last={i === g.blocks.length - 1} groupId={id} groups={groups} />
      ))}
      {!g.blocks.length && <div className="px-3 py-2.5 text-[12px] text-muted-foreground">用别的块上的「移到…」放进来。</div>}
    </div>
  );
}

/** Saved on blur / Enter, not per keystroke: every config change is a PUT of the whole config. */
function NameInput({ value, onSave }: { value: string; onSave: (v: string) => void }) {
  const [v, setV] = useState(value);
  const save = () => {
    const t = v.trim();
    if (t && t !== value) onSave(t);
    else setV(value);
  };
  return (
    <Input
      value={v}
      onChange={(e) => setV(e.target.value)}
      onBlur={save}
      onKeyDown={(e) => e.key === "Enter" && (e.currentTarget as HTMLInputElement).blur()}
      className="h-8 w-40 text-[13px]"
      aria-label="分组名字"
    />
  );
}

/** How a block or face is listed: the icon it declared, else its add-on's icon, else a puzzle piece. */
function RegIcon({ r }: { r: SlotReg }) {
  const Icon = lucideIcon(r.icon);
  const a = addonMeta(r.addon);
  const [broken, setBroken] = useState(false);
  if (Icon) return <Icon className="size-4 shrink-0 text-muted-foreground" />;
  if (a?.icon && !broken) return <img src={assetURL(a.id, a.icon, a.hash)} alt="" className="size-5 shrink-0 rounded-sm" onError={() => setBroken(true)} />;
  return <Puzzle className="size-4 shrink-0 text-muted-foreground" />;
}

function BlockRow({ b, first, last, groupId, groups }: { b: SlotReg; first: boolean; last: boolean; groupId: string; groups: ResolvedGroup[] }) {
  const addonName = addonMeta(b.addon)?.name ?? b.addon;
  return (
    <div className={cn("flex items-center gap-2.5 px-3 py-2 text-[13px]", !first && "border-t border-dashed")}>
      <RegIcon r={b} />
      <span className="min-w-0 truncate">{b.title ?? addonName}</span>
      {b.title && <span className="min-w-0 truncate text-[12px] text-muted-foreground">{addonName}</span>}
      <div className="flex-1" />
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline" size="sm" className="h-7 shrink-0 gap-1 px-2 text-[12px]">
            移到… <ChevronDown className="size-3" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem disabled={first} onClick={() => stepBlock(b.key, -1)}>
            <ArrowUp /> 上移
          </DropdownMenuItem>
          <DropdownMenuItem disabled={last} onClick={() => stepBlock(b.key, 1)}>
            <ArrowDown /> 下移
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuLabel className="text-[11px] font-normal text-muted-foreground">移到分组</DropdownMenuLabel>
          {groups
            .filter((x) => x.group.id !== groupId)
            .map((x) => (
              <DropdownMenuItem key={x.group.id} onClick={() => moveBlock(b.key, x.group.id)}>
                <span className="flex size-4 items-center justify-center">
                  <FaceView face={x.group.face} blocks={x.blocks} small />
                </span>
                {x.group.name}
              </DropdownMenuItem>
            ))}
          <DropdownMenuItem onClick={() => moveBlock(b.key, "new")}>
            <Plus /> 新分组
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

/**
 * The button's face: automatic, one or two typed characters, a built-in icon (the same set roles pick from in Settings →
 * Agent, with its colours), or a face an add-on draws (g.slot("rail")) — from any add-on, not only the group's.
 */
function FacePicker({ face, blocks, onPick }: { face?: RailFace; blocks: SlotReg[]; onPick: (f: RailFace | undefined) => void }) {
  const [open, setOpen] = useState(false);
  const [text, setText] = useState(face && "text" in face ? face.text : "");
  const glyph = face && "glyph" in face ? face : undefined;
  const pick = (f: RailFace | undefined) => (onPick(f), setOpen(false));
  const cell = (on: boolean, title: string, onClick: () => void, body: React.ReactNode, key?: string) => (
    <button
      key={key}
      type="button"
      title={title}
      onClick={onClick}
      className={cn("flex size-8 items-center justify-center rounded-md border", on ? "border-brand bg-accent" : "border-transparent hover:bg-accent")}
    >
      {body}
    </button>
  );
  const icons = (list: Record<string, { name: string }>) =>
    Object.entries(list).map(([k, g]) =>
      cell(glyph?.glyph === k, g.name, () => pick({ glyph: k, color: glyph?.color }), <AgentGlyph name={k} color={glyph?.color} className="size-5" />, k),
    );
  const chars = [...text].slice(0, 2).join("");
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          title="按钮上显示什么"
          className="flex size-9 shrink-0 items-center justify-center rounded-md border bg-sidebar text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <FaceView face={face} blocks={blocks} />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80 space-y-3 p-3">
        <section className="space-y-1.5">
          <div className="text-[11px] text-muted-foreground">文字（一两个字符或 emoji）</div>
          <div className="flex gap-1.5">
            <Input
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder="¥"
              className="h-8 w-24 text-[13px]"
              onKeyDown={(e) => e.key === "Enter" && chars && pick({ text: chars })}
            />
            <Button size="sm" variant="outline" className="h-8" disabled={!chars} onClick={() => pick({ text: chars })}>
              用这个
            </Button>
          </div>
        </section>
        <section className="space-y-1.5">
          <div className="text-[11px] text-muted-foreground">图标</div>
          <div className="flex flex-wrap gap-1">
            {cell(!face, "自动（组里插件画的外观，或第一个块的图标）", () => pick(undefined), <span className="text-[11px] text-muted-foreground">自动</span>)}
            {icons(LINES)}
          </div>
          <div className="flex flex-wrap gap-1">{icons(GLYPHS)}</div>
          {glyph && (
            <div className="flex flex-wrap gap-1 border-t pt-1">
              {cell(!glyph.color, "默认颜色", () => pick({ glyph: glyph.glyph }), <AgentGlyph name={glyph.glyph} className="size-5" />)}
              {Object.entries(PALETTE).map(([k, c]) =>
                cell(
                  glyph.color === k,
                  c.name,
                  () => pick({ glyph: glyph.glyph, color: k }),
                  <span className="size-4 rounded-full" style={{ background: `linear-gradient(135deg, ${c.from}, ${c.to})` }} />,
                  k,
                ),
              )}
            </div>
          )}
        </section>
        {slots.rail.length > 0 && (
          <section className="space-y-1.5">
            <div className="text-[11px] text-muted-foreground">插件画的外观</div>
            <div className="flex flex-wrap gap-1">
              {slots.rail.map((r) => (
                <button
                  key={r.key}
                  type="button"
                  title={r.title ?? addonMeta(r.addon)?.name ?? r.addon}
                  onClick={() => pick({ addon: r.key })}
                  className={cn(
                    "flex h-10 w-11 items-center justify-center overflow-hidden rounded-md border",
                    face && "addon" in face && face.addon === r.key ? "border-brand bg-accent" : "border-transparent bg-sidebar hover:bg-accent",
                  )}
                >
                  <span className="flex max-h-7 w-9 items-center justify-center overflow-hidden">
                    <AddonParts regs={[r]} />
                  </span>
                </button>
              ))}
            </div>
          </section>
        )}
      </PopoverContent>
    </Popover>
  );
}
