import type { ReactNode } from "react";
import { slots } from "@/lib/addons";
import { spriteTitle } from "@/lib/rail";
import { lucideIcon } from "@/lib/rail";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { t } from "@/lib/i18n";
import { FitSprite } from "./AddonSlot";
import { AgentGlyph, GLYPHS, LINES, PALETTE } from "./AgentGlyph";
import type { MessageKey } from "@/lib/messages";

/**
 * An icon as gerdr stores one, for agents (AgentDef.icon / color) and panels (ui.json icon / color) alike: a colourful
 * glyph or a line icon by key (AgentGlyph GLYPHS / LINES), or "sprite:<key>", a picture an add-on brought
 * (g.slot("sprite"), drawn still); plus a colour (a PALETTE key) for the built-in ones. "" is the owner's default.
 */
import { SPRITE } from "./AgentIcon";

/** Whether an icon value is one this picker knows: else the owner draws its default. */
export const knownIcon = (icon?: string) => !!icon && (!!GLYPHS[icon] || !!LINES[icon] || icon.startsWith(SPRITE));

/** Draws a stored icon; `fallback` while it's "" or unknown, or its sprite's add-on isn't loaded (disabled, removed, not yet). */
export function StoredIcon({
  icon,
  color,
  fallback,
  className,
  state,
  clip,
}: {
  icon?: string;
  color?: string;
  fallback: ReactNode;
  className?: string;
  /** A sprite's state: "idle" (default, still) or "working" / "waiting" (moving, if the sprite animates). */
  state?: "idle" | "working" | "waiting";
  /** A sprite kept inside its box (default): lists must not be pushed around. */
  clip?: boolean;
}) {
  useStore((s) => s.addonVersion);
  if (!icon || !knownIcon(icon)) return <>{fallback}</>;
  if (icon.startsWith(SPRITE)) {
    const sprite = slots.sprite.find((r) => r.key === icon.slice(SPRITE.length));
    return sprite ? <FitSprite reg={sprite} className={className} state={state} clip={clip} /> : <>{fallback}</>;
  }
  return <AgentGlyph name={icon} color={color} className={className} />;
}

/**
 * An add-on's icon (an "icon" setting's value, g.ui.Icon): a stored icon as above, else a lucide icon by name ("Coffee",
 * an add-on's default), else `fallback`. Built-in glyphs draw in their own colours.
 */
export function AddonIcon({
  icon,
  className,
  fallback,
  state,
  clip = false,
}: {
  icon?: string;
  className?: string;
  fallback?: ReactNode;
  state?: "idle" | "working" | "waiting";
  /** Off by default: a moving sprite is drawn to stick out a little (a hat over the top), as in the loader. */
  clip?: boolean;
}) {
  const L = !knownIcon(icon) ? lucideIcon(icon) : undefined;
  if (L) return <L className={className} />;
  return <StoredIcon icon={icon} className={className} fallback={fallback ?? null} state={state} clip={clip} />;
}

/**
 * Picks one: the default (`def`, the owner's: a vendor logo, an automatic glyph, a panel's briefcase), a colourful
 * glyph, a line icon or a sprite add-ons brought; then a colour for whichever built-in icon shows (none for sprites
 * and for a default the owner draws itself: `colorable` says).
 */
export function IconPicker({
  icon,
  color,
  onChange,
  def,
  colorable,
}: {
  icon: string;
  color: string;
  onChange: (p: { icon?: string; color?: string }) => void;
  def: { title: string; node: ReactNode; glyph?: string };
  colorable: boolean;
}) {
  useStore((s) => s.addonVersion);
  const cell = (on: boolean, title: string, onClick: () => void, body: ReactNode, key?: string) => (
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
  const icons = (list: Record<string, { label: MessageKey }>) =>
    Object.entries(list).map(([k, g]) =>
      cell(icon === k, t(g.label), () => onChange({ icon: k }), <AgentGlyph name={k} color={color} className="size-5" />, k),
    );
  return (
    <div className="space-y-1">
      <div className="flex flex-wrap gap-1">
        {cell(!icon, def.title, () => onChange({ icon: "" }), def.node)}
        {icons(GLYPHS)}
      </div>
      <div className="flex flex-wrap gap-1">{icons(LINES)}</div>
      {slots.sprite.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {slots.sprite.map((r) =>
            cell(icon === SPRITE + r.key, spriteTitle(r), () => onChange({ icon: SPRITE + r.key }), <FitSprite reg={r} className="size-6" />, r.key),
          )}
        </div>
      )}
      {colorable && (
        <div className="flex flex-wrap gap-1 border-t pt-1">
          {cell(!color, t("agentSettings.colorDefault"), () => onChange({ color: "" }), <AgentGlyph name={icon || def.glyph || "orbit"} className="size-5" />)}
          {Object.entries(PALETTE).map(([k, c]) =>
            cell(
              color === k,
              t(c.label),
              () => onChange({ color: k }),
              <span className="size-4 rounded-full" style={{ background: `linear-gradient(135deg, ${c.from}, ${c.to})` }} />,
              k,
            ),
          )}
        </div>
      )}
    </div>
  );
}
