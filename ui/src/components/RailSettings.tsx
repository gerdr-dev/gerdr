import { ArrowDown, ArrowUp, Ellipsis, GripVertical, Plus, Puzzle, Trash2 } from "lucide-react";
import { createContext, useContext, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuLabel, ContextMenuSeparator, ContextMenuTrigger } from "@/components/ui/context-menu";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { assetURL, slots, type SlotReg } from "@/lib/addons";
import {
  addGroup,
  addonMeta,
  spriteTitle,
  lucideIcon,
  moveBlock,
  moveGroup,
  placeBlock,
  removeGroup,
  renameGroup,
  resolveGroups,
  setFace,
  setOpenMode,
  stepBlock,
  type ResolvedGroup,
} from "@/lib/rail";
import type { RailFace, RailOpen } from "@/lib/store/config";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { AgentGlyph, GLYPHS, LINES, PALETTE } from "./AgentGlyph";
import { SpriteView } from "./AddonSlot";
import { FaceView } from "./RailGroups";

// Settings → 扩展 → 分组: which add-on blocks go behind which button on the rail, and what each button shows
// (lib/rail.ts). One card per button. A block moves by dragging it (within a group, to another one, onto "新分组");
// right-click / long-press offers the same moves as a menu.
//
// The drag is pointer events with capture, not HTML5 drag and drop (same reasons as TabDrag.tsx: a ghost we draw
// ourselves, and it works with touch). Where it would land is found under the pointer through data-drop-* attributes;
// nothing moves while dragging, a line shows the spot.

/** Where a dragged block would land: a group (or "new"), before a block of it or at its end. */
type Drop = { group: string; before?: string };
type DragState = { key: string; title: string; x: number; y: number; drop: Drop | null };
const DragCtx = createContext<{ drag: DragState | null; start: (e: React.PointerEvent, key: string, title: string) => void }>({
  drag: null,
  start: () => {},
});

function dropAt(x: number, y: number): Drop | null {
  const el = document.elementFromPoint(x, y);
  const row = el?.closest<HTMLElement>("[data-drop-block]");
  if (row) {
    const r = row.getBoundingClientRect();
    const { group = "", key, next } = row.dataset;
    return { group, before: y < r.top + r.height / 2 ? key : next || undefined };
  }
  const card = el?.closest<HTMLElement>("[data-drop-group]");
  if (card) return { group: card.dataset.dropGroup! };
  return el?.closest("[data-drop-new]") ? { group: "new" } : null;
}

function useBlockDrag() {
  const [drag, setDrag] = useState<DragState | null>(null);
  const ref = useRef<DragState | null>(null);
  const put = (d: DragState | null) => ((ref.current = d), setDrag(d));
  const start = (e: React.PointerEvent, key: string, title: string) => {
    if (e.button !== 0) return;
    e.preventDefault();
    const el = e.currentTarget as HTMLElement;
    el.setPointerCapture(e.pointerId);
    const x0 = e.clientX;
    const y0 = e.clientY;
    let moved = false;
    const move = (ev: PointerEvent) => {
      // A few pixels first, so a press that wobbles isn't a drag
      if (!moved && Math.hypot(ev.clientX - x0, ev.clientY - y0) < 4) return;
      moved = true;
      put({ key, title, x: ev.clientX, y: ev.clientY, drop: dropAt(ev.clientX, ev.clientY) });
    };
    const end = (ev: PointerEvent) => {
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", end);
      el.removeEventListener("pointercancel", end);
      const d = ref.current;
      put(null);
      if (ev.type === "pointerup" && d?.drop) placeBlock(key, d.drop.group, d.drop.before);
    };
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", end);
    el.addEventListener("pointercancel", end);
  };
  return { drag, start };
}

export function RailSettings() {
  useStore((s) => s.addonVersion);
  useStore((s) => s.config.rail);
  const groups = resolveGroups();
  const dnd = useBlockDrag();
  if (!slots.sidebar.length)
    return <div className="py-6 text-center text-[13px] text-muted-foreground">还没有带面板的插件。装一个（比如额度条）后，在这里分组。</div>;
  return (
    <DragCtx.Provider value={dnd}>
      <div className="space-y-3">
        <p className="text-[12px] text-muted-foreground">
          窗口最左边那条竖栏（左侧条）上，每个分组是一个按钮。打开按钮，旁边会弹出这组插件的面板，按下面的顺序从上往下排。一个分组可以放好几个插件。
        </p>
        {groups.map((g, i) => (
          <GroupCard key={g.group.id} g={g} index={i} groups={groups} />
        ))}
        <Button
          data-drop-new
          variant="outline"
          size="sm"
          className={cn("w-full border-dashed text-muted-foreground", dnd.drag?.drop?.group === "new" && "border-brand text-foreground")}
          onClick={() => addGroup()}
        >
          <Plus /> {dnd.drag ? "放到新分组" : "新分组"}
        </Button>
        {dnd.drag &&
          createPortal(
            <div
              className="pointer-events-none fixed z-[100] flex items-center gap-1.5 rounded-md border bg-popover px-2.5 py-1.5 text-[13px] shadow-lg"
              style={{ left: dnd.drag.x + 10, top: dnd.drag.y + 8 }}
            >
              <GripVertical className="size-3.5 text-muted-foreground" />
              {dnd.drag.title}
            </div>,
            document.body,
          )}
      </div>
    </DragCtx.Provider>
  );
}

/** Hover or click to open this button's panel (RailGroup.open; RailGroups.tsx). Touch screens always tap. */
function OpenMode({ id, mode }: { id: string; mode: RailOpen }) {
  return (
    <Select value={mode} onValueChange={(v) => setOpenMode(id, v as RailOpen)}>
      <SelectTrigger size="sm" className="w-28 text-[12px]" aria-label="怎么打开面板">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="hover">悬停打开</SelectItem>
        <SelectItem value="click">点击打开</SelectItem>
      </SelectContent>
    </Select>
  );
}

function GroupCard({ g, index, groups }: { g: ResolvedGroup; index: number; groups: ResolvedGroup[] }) {
  const id = g.group.id;
  const { drag } = useContext(DragCtx);
  const here = drag?.drop?.group === id;
  return (
    <div data-drop-group={id} className={cn("rounded-lg border", here && "border-brand")}>
      <div className="flex items-center gap-2 border-b p-2.5">
        <FacePicker face={g.group.face} blocks={g.blocks} onPick={(f) => setFace(id, f)} />
        <NameInput key={g.group.name} value={g.group.name} onSave={(v) => renameGroup(id, v)} />
        {!g.blocks.length && <span className="text-[12px] text-muted-foreground">空的，不会显示</span>}
        <div className="flex-1" />
        {g.blocks.length > 0 && <OpenMode id={id} mode={g.group.open ?? "hover"} />}
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
        <BlockRow key={b.key} b={b} next={g.blocks[i + 1]?.key} first={i === 0} last={i === g.blocks.length - 1} groupId={id} groups={groups} />
      ))}
      {/* Dropping at the group's end: the line under its last block */}
      {here && !drag?.drop?.before && g.blocks.length > 0 && <div className="mx-3 -mt-px h-0.5 rounded-full bg-brand" />}
      {!g.blocks.length && <div className="px-3 py-2.5 text-[12px] text-muted-foreground">把别的分组里的插件拖进来。</div>}
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

/**
 * One block: drag it anywhere on the row (a mouse; on a touch screen by the grip, so a swipe still scrolls), or
 * right-click / long-press it for the same moves as a menu.
 */
function BlockRow({
  b,
  next,
  first,
  last,
  groupId,
  groups,
}: {
  b: SlotReg;
  next?: string;
  first: boolean;
  last: boolean;
  groupId: string;
  groups: ResolvedGroup[];
}) {
  const addonName = addonMeta(b.addon)?.name ?? b.addon;
  const title = b.title ?? addonName;
  const { drag, start } = useContext(DragCtx);
  const lineAbove = drag?.drop?.group === groupId && drag.drop.before === b.key && drag.key !== b.key;
  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div
          data-drop-block
          data-group={groupId}
          data-key={b.key}
          data-next={next ?? ""}
          className={cn(
            "relative flex cursor-grab items-center gap-2.5 py-2 pr-3 pl-1.5 text-[13px] select-none active:cursor-grabbing",
            !first && "border-t border-dashed",
            drag?.key === b.key && "opacity-40",
          )}
          onPointerDown={(e) => e.pointerType !== "touch" && start(e, b.key, title)}
        >
          {lineAbove && <div className="absolute inset-x-3 -top-px h-0.5 rounded-full bg-brand" />}
          {/* touch-none: on a phone the press on the grip drags instead of scrolling the settings */}
          <span
            aria-hidden
            className="flex h-6 w-4 shrink-0 touch-none items-center justify-center text-muted-foreground/60"
            onPointerDown={(e) => e.pointerType === "touch" && start(e, b.key, title)}
          >
            <GripVertical className="size-3.5" />
          </span>
          <RegIcon r={b} />
          <span className="min-w-0 truncate">{title}</span>
          {b.title && <span className="min-w-0 truncate text-[12px] text-muted-foreground">{addonName}</span>}
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem disabled={first} onClick={() => stepBlock(b.key, -1)}>
          <ArrowUp /> 上移
        </ContextMenuItem>
        <ContextMenuItem disabled={last} onClick={() => stepBlock(b.key, 1)}>
          <ArrowDown /> 下移
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuLabel className="text-[11px] font-normal text-muted-foreground">移到分组</ContextMenuLabel>
        {groups
          .filter((x) => x.group.id !== groupId)
          .map((x) => (
            <ContextMenuItem key={x.group.id} onClick={() => moveBlock(b.key, x.group.id)}>
              <span className="flex size-4 items-center justify-center">
                <FaceView face={x.group.face} blocks={x.blocks} small />
              </span>
              {x.group.name}
            </ContextMenuItem>
          ))}
        <ContextMenuItem onClick={() => moveBlock(b.key, "new")}>
          <Plus /> 新分组
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

/**
 * The button's face: automatic, one or two typed characters, a built-in icon (the same set roles pick from in Settings →
 * Agent, with its colours), or a sprite (g.slot("sprite")) — from any add-on, not only the group's.
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
      <PopoverContent align="start" className="max-h-(--radix-popover-content-available-height) w-80 space-y-3 overflow-y-auto p-3">
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
            {cell(!face, "自动（组里插件的素材，或第一个块的图标）", () => pick(undefined), <span className="text-[11px] text-muted-foreground">自动</span>)}
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
        {slots.sprite.length > 0 && (
          <section className="space-y-1.5">
            <div className="text-[11px] text-muted-foreground">素材</div>
            <div className="flex flex-wrap gap-1">
              {/* Same 32px cells as the icons above: a sprite shrinks into a 24×20 box here, not its rail size */}
              {slots.sprite.map((r) =>
                cell(
                  !!face && "sprite" in face && face.sprite === r.key,
                  spriteTitle(r),
                  () => pick({ sprite: r.key }),
                  <SpriteView reg={r} state="idle" clip width={24} height={20} />,
                  r.key,
                ),
              )}
            </div>
          </section>
        )}
      </PopoverContent>
    </Popover>
  );
}
