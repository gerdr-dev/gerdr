import { useId, useRef, type ReactNode } from "react";
import { ImagePlus } from "lucide-react";
import { toast } from "sonner";
import { slots } from "@/lib/addons";
import { spriteTitle } from "@/lib/rail";
import { lucideIcon } from "@/lib/rail";
import { useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { t } from "@/lib/i18n";
import { FitSprite } from "./AddonSlot";
import { AgentGlyph, GLYPHS, LINES, PALETTE, colorStops, isHexColor } from "./AgentGlyph";
import { imageToIcon, isImageIcon, TEXT_ICON } from "@/lib/iconImage";
import type { MessageKey } from "@/lib/messages";

/**
 * An icon as gerdr stores one, for agents (AgentDef.icon / color) and panels (ui.json icon / color) alike: a colourful
 * glyph or a line icon by key (AgentGlyph GLYPHS / LINES), or "sprite:<key>", a picture an add-on brought
 * (g.slot("sprite"), drawn still); plus a colour (a PALETTE key) for the built-in ones. "" is the owner's default.
 * Projects and panels also take "text:<up to 3 characters>" (drawn on a tile of the colour, which may also be a
 * "#rrggbb") and an uploaded picture, kept inline as a small data URL (lib/iconImage.ts) so it goes wherever the
 * value goes (layout.json, a panel's ui.json, another computer, the phone) with no file to serve.
 */
import { SPRITE } from "./AgentIcon";

/** Whether an icon value is one this picker knows: else the owner draws its default. */
export const knownIcon = (icon?: string) =>
  !!icon && (!!GLYPHS[icon] || !!LINES[icon] || icon.startsWith(SPRITE) || (icon.startsWith(TEXT_ICON) && icon.length > TEXT_ICON.length) || isImageIcon(icon));

/**
 * A text icon: up to three characters on a rounded tile of the colour (the foreground colour by default). Drawn as an
 * SVG so it scales with the box like the glyphs do; the font shrinks as the text gets wider (a CJK character counts
 * as a whole width, a Latin one as about half).
 */
export function TextIcon({ text, color, className }: { text: string; color?: string; className?: string }) {
  const c = colorStops(color);
  const id = ("ti" + (color ?? "")).replace(/[^\w-]/g, "") + useId().replace(/[^\w-]/g, "");
  const w = [...text].reduce((n, ch) => n + (ch.codePointAt(0)! >= 0x2e80 ? 1 : 0.62), 0) || 1;
  return (
    <svg viewBox="0 0 24 24" className={cn("shrink-0", className)} aria-label={text}>
      {c && (
        <defs>
          <linearGradient id={id} gradientUnits="userSpaceOnUse" x1="2" y1="0" x2="22" y2="24">
            <stop stopColor={c.from} />
            <stop offset="1" stopColor={c.to} />
          </linearGradient>
        </defs>
      )}
      <rect width="24" height="24" rx="6" fill={c ? `url(#${id})` : "var(--foreground)"} />
      <text
        x="12"
        y="12.5"
        textAnchor="middle"
        dominantBaseline="central"
        fontSize={Math.min(14, 19 / w)}
        fontWeight={600}
        fill={c ? "white" : "var(--background)"}
      >
        {text}
      </text>
    </svg>
  );
}

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
  if (icon.startsWith(TEXT_ICON)) return <TextIcon text={icon.slice(TEXT_ICON.length)} color={color} className={className} />;
  if (isImageIcon(icon)) return <img src={icon} alt="" draggable={false} className={cn("shrink-0 rounded-[22%] object-cover", className)} />;
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
  custom,
}: {
  icon: string;
  color: string;
  onChange: (p: { icon?: string; color?: string }) => void;
  def: { title: string; node: ReactNode; glyph?: string };
  colorable: boolean;
  /** Also a text icon, an uploaded picture and any colour: projects and panels (agents' icons stay the built-in kinds). */
  custom?: boolean;
}) {
  useStore((s) => s.addonVersion);
  const file = useRef<HTMLInputElement>(null);
  const text = icon.startsWith(TEXT_ICON) ? icon.slice(TEXT_ICON.length) : "";
  const upload = async (f?: File) => {
    if (!f) return;
    try {
      onChange({ icon: await imageToIcon(f) });
    } catch (e: any) {
      toast.error(t("iconPicker.uploadFailed"), { description: e?.message ?? String(e) });
    }
  };
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
      {custom && (
        // Its own words or its own picture: a project's or panel's face that isn't one of the stock icons (asked for)
        <div className="flex flex-wrap items-center gap-1 border-t pt-1">
          <label
            className={cn("flex h-8 items-center gap-1.5 rounded-md border px-1.5", text ? "border-brand bg-accent" : "border-transparent hover:bg-accent")}
          >
            <TextIcon text={text || "Aa"} color={color} className={cn("size-5", !text && "opacity-50")} />
            <input
              value={text}
              placeholder={t("iconPicker.text")}
              onChange={(e) => onChange({ icon: e.target.value.trim() ? TEXT_ICON + [...e.target.value.trimStart()].slice(0, 3).join("") : "" })}
              className="w-16 bg-transparent text-[12px] outline-none placeholder:text-muted-foreground"
            />
          </label>
          {isImageIcon(icon) && cell(true, t("iconPicker.image"), () => {}, <img src={icon} alt="" className="size-6 rounded-[22%] object-cover" />)}
          <button
            type="button"
            className="flex h-8 items-center gap-1 rounded-md px-1.5 text-[12px] text-muted-foreground hover:bg-accent hover:text-foreground"
            onClick={() => file.current?.click()}
          >
            <ImagePlus className="size-4" /> {t("iconPicker.upload")}
          </button>
          <input
            ref={file}
            type="file"
            accept="image/*"
            hidden
            onChange={(e) => {
              void upload(e.target.files?.[0]);
              e.target.value = "";
            }}
          />
        </div>
      )}
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
          {custom && (
            // Any colour, beyond the palette: the native picker, drawn as one more swatch
            <label
              title={t("iconPicker.customColor")}
              className={cn(
                "relative flex size-8 cursor-pointer items-center justify-center rounded-md border",
                isHexColor(color) ? "border-brand bg-accent" : "border-transparent hover:bg-accent",
              )}
            >
              <span
                className="size-4 rounded-full"
                style={{ background: isHexColor(color) ? color : "conic-gradient(red, yellow, lime, cyan, blue, magenta, red)" }}
              />
              <input
                type="color"
                value={isHexColor(color) ? color : "#6366f1"}
                onChange={(e) => onChange({ color: e.target.value })}
                className="absolute inset-0 cursor-pointer opacity-0"
              />
            </label>
          )}
        </div>
      )}
    </div>
  );
}
