import * as icons from "lucide-react";
import { addonStatus, slots, type SlotReg } from "./addons";
import { get, setConfig } from "./store";
import type { RailConfig, RailFace, RailGroup } from "./store/config";

// The rail's add-on buttons. Add-ons register blocks (g.slot("sidebar")) and faces (g.slot("rail")); which blocks go
// behind which button, and what each button shows, is the user's (config.rail, shared by every device), set in
// Settings → 扩展 → 窄条. Add-ons can't group themselves: blocks that belong together (a "today" and two quota meters)
// often come from different authors, and only the user knows they're one subject.
//
// Without a config there's one button holding every block (what the rail had before groups). A block no group lists
// (an add-on installed since) joins the first group. Keys of blocks that aren't loaded now (an add-on disabled for a
// while) stay in their group, so it comes back where it was.

export const DEFAULT_GROUP: RailGroup = { id: "default", name: "插件", blocks: [] };

/** The groups as configured, or the one default group. Never empty. */
export function railGroups(cfg: RailConfig | undefined = get().config.rail): RailGroup[] {
  return cfg?.groups?.length ? cfg.groups : [DEFAULT_GROUP];
}

export type ResolvedGroup = { group: RailGroup; blocks: SlotReg[] };

/** Each group with the blocks loaded now, in its order; unlisted loaded blocks appended to the first group. */
export function resolveGroups(cfg?: RailConfig): ResolvedGroup[] {
  const groups = railGroups(cfg);
  const byKey = new Map(slots.sidebar.map((r) => [r.key, r]));
  const listed = new Set(groups.flatMap((g) => g.blocks));
  const out = groups.map((group) => ({ group, blocks: group.blocks.map((k) => byKey.get(k)).filter((r): r is SlotReg => !!r) }));
  out[0].blocks.push(...slots.sidebar.filter((r) => !listed.has(r.key)));
  return out;
}

/** What a button shows when the user picked nothing: a face from an add-on of its blocks, else the first block's icon. */
export function autoFace(blocks: SlotReg[]): RailFace | undefined {
  const mine = new Set(blocks.map((b) => b.addon));
  const face = slots.rail.find((f) => mine.has(f.addon));
  if (face) return { addon: face.key };
  return undefined;
}

/** A lucide icon by name ("Gauge", "gauge", "chart-column"), for blocks / faces that declared one. */
export function lucideIcon(name?: string): icons.LucideIcon | undefined {
  if (!name) return undefined;
  const pascal = name.replace(/(^|[-_ ])(\w)/g, (_, __, c: string) => c.toUpperCase());
  const i = (icons as any)[pascal] ?? (icons as any)[name];
  return typeof i === "object" || typeof i === "function" ? (i as icons.LucideIcon) : undefined;
}

/** The add-on's name and its addon.json icon, for listing a block or face. */
export function addonMeta(addon: string) {
  return addonStatus.find((a) => a.id === addon);
}

// ---- editing (Settings → 扩展 → 窄条) ----

/**
 * Saves a changed copy of the groups. The first edit writes the default out: it materializes the unlisted blocks into
 * the first group, so their order is the user's from then on.
 */
function edit(f: (groups: RailGroup[]) => RailGroup[]) {
  const resolved = resolveGroups();
  const groups = railGroups().map((g, i) => {
    const have = new Set(g.blocks);
    const extra = i === 0 ? resolved[0].blocks.map((b) => b.key).filter((k) => !have.has(k)) : [];
    return { ...g, blocks: [...g.blocks, ...extra] };
  });
  setConfig({ rail: { groups: f(groups) } });
}

const without = (groups: RailGroup[], key: string) => groups.map((g) => ({ ...g, blocks: g.blocks.filter((k) => k !== key) }));

export function renameGroup(id: string, name: string) {
  edit((gs) => gs.map((g) => (g.id === id ? { ...g, name } : g)));
}

export function setFace(id: string, face: RailFace | undefined) {
  edit((gs) => gs.map((g) => (g.id === id ? { ...g, face } : g)));
}

/** Moves a group up / down the rail. */
export function moveGroup(id: string, by: -1 | 1) {
  edit((gs) => {
    const i = gs.findIndex((g) => g.id === id);
    const j = i + by;
    if (i < 0 || j < 0 || j >= gs.length) return gs;
    const out = [...gs];
    [out[i], out[j]] = [out[j], out[i]];
    return out;
  });
}

/** Removes a group; its blocks go to the first one left (there's always one). */
export function removeGroup(id: string) {
  edit((gs) => {
    if (gs.length < 2) return gs;
    const gone = gs.find((g) => g.id === id);
    const rest = gs.filter((g) => g.id !== id);
    rest[0] = { ...rest[0], blocks: [...rest[0].blocks, ...(gone?.blocks ?? [])] };
    return rest;
  });
}

const newId = () => "g" + Math.random().toString(36).slice(2, 8);

/** A new empty group at the end; returns its id. */
export function addGroup(name = "新按钮"): string {
  const id = newId();
  edit((gs) => [...gs, { id, name, blocks: [] }]);
  return id;
}

/** Moves a block into another group (at its end), or into a new group when `to` is "new". */
export function moveBlock(key: string, to: string) {
  edit((gs) => {
    let out = without(gs, key);
    if (to === "new") return [...out, { id: newId(), name: "新按钮", blocks: [key] }];
    out = out.map((g) => (g.id === to ? { ...g, blocks: [...g.blocks, key] } : g));
    return out;
  });
}

/** Moves a block up / down inside its group, past the loaded neighbours (keys of unloaded blocks keep their places). */
export function stepBlock(key: string, by: -1 | 1) {
  const loaded = new Set(slots.sidebar.map((r) => r.key));
  edit((gs) =>
    gs.map((g) => {
      const i = g.blocks.indexOf(key);
      if (i < 0) return g;
      let j = i + by;
      while (j >= 0 && j < g.blocks.length && !loaded.has(g.blocks[j])) j += by;
      if (j < 0 || j >= g.blocks.length) return g;
      const blocks = [...g.blocks];
      [blocks[i], blocks[j]] = [blocks[j], blocks[i]];
      return { ...g, blocks };
    }),
  );
}
