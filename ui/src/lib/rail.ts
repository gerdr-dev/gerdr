import * as icons from "lucide-react";
import type { ComponentType } from "react";
import { addonStatus, slots, type SlotReg } from "./addons";

// The rail's add-on buttons: one per add-on that registered "panel" blocks (g.slot("panel")), its blocks stacked in
// the panel. The button — its icon, its title, whether it opens on hover or on click — is the add-on's, from the
// options of its blocks. Gerdr doesn't group add-ons any more (Nov 2026: a Settings page that dragged blocks of several
// add-ons behind one button): an add-on is small source code, so a user who wants several in one button has an agent
// write one add-on with their parts copied in. That covers what grouping did and what it couldn't (rows lined up
// across sources, one icon drawn from all of their data) without a settings page to maintain.

export type RailOpen = "hover" | "click";
/** An icon component an add-on draws in a box of width × height px (opts.icon). */
export type IconComponent = ComponentType<{ width: number; height: number }>;

export type RailButton = { addon: string; title: string; icon?: string | IconComponent; tall: boolean; open: RailOpen; blocks: SlotReg[] };

/**
 * One button per add-on with loaded blocks, in slot order (by the smallest order of its blocks, then add-on id: the
 * slot is kept sorted that way). title / icon / open: the first block that gives one; title defaults to the add-on's
 * name, open to hover. tall: that icon's block asked for a button two cells tall.
 */
export function railButtons(): RailButton[] {
  const out = new Map<string, RailButton>();
  for (const r of slots.panel) {
    let b = out.get(r.addon);
    if (!b) out.set(r.addon, (b = { addon: r.addon, title: "", tall: false, open: "hover", blocks: [] }));
    b.blocks.push(r);
  }
  for (const b of out.values()) {
    b.title = b.blocks.find((x) => x.title)?.title ?? addonMeta(b.addon)?.name ?? b.addon;
    const withIcon = b.blocks.find((x) => x.icon);
    b.icon = withIcon?.icon;
    b.tall = !!withIcon?.tallIcon;
    b.open = b.blocks.find((x) => x.open)?.open ?? "hover";
  }
  return [...out.values()];
}

/** A lucide icon by name ("Gauge", "gauge", "chart-column"), for add-ons that declared one. */
export function lucideIcon(name?: string): icons.LucideIcon | undefined {
  if (!name) return undefined;
  const pascal = name.replace(/(^|[-_ ])(\w)/g, (_, __, c: string) => c.toUpperCase());
  const i = (icons as any)[pascal] ?? (icons as any)[name];
  return typeof i === "object" || typeof i === "function" ? (i as icons.LucideIcon) : undefined;
}

/** The add-on's name and its addon.json icon, for listing a block or sprite. */
export function addonMeta(addon: string) {
  return addonStatus.find((a) => a.id === addon);
}

/** How a sprite is listed: its title with its add-on's name ("Pirate · Pixel Clawd"), or just the add-on's name. */
export function spriteTitle(r: SlotReg) {
  const name = addonMeta(r.addon)?.name ?? r.addon;
  return r.title && r.title !== name ? `${r.title} · ${name}` : name;
}
