import { useId, type ReactNode } from "react";
import {
  Activity,
  Bell,
  BookOpen,
  Bot,
  Brain,
  Briefcase,
  Bug,
  Calendar,
  Cat,
  ChartColumn,
  Code,
  Coins,
  Compass,
  Database,
  Feather,
  FlaskConical,
  Gauge,
  Ghost,
  Globe,
  GraduationCap,
  MessageCircle,
  Palette,
  PenTool,
  Rocket,
  Search,
  Shield,
  Sparkles,
  Terminal,
  Timer,
  Wallet,
  Wrench,
  Zap,
  type LucideIcon,
} from "lucide-react";
import { t } from "@/lib/i18n";
import type { MessageKey } from "@/lib/messages";
import { cn } from "@/lib/utils";

// Built-in abstract icons for agents (roles). The robot head they all used to share was hard to tell apart once there
// were several Native roles, so each glyph is a distinct shape AND a distinct hue: either alone tells two rows apart.
// Colours are fixed (like the vendor logos in AgentIcon), not theme tokens: they are the icon, and every pair reads on
// both the light and the dark background. A role picks one in Settings → Agent (`icon` in config.json); one that
// didn't gets a stable pick from its id (autoGlyph). Next to them, line icons (LINES, from lucide, which the UI already
// uses; the robot head is one): grey by default, for a role whose job is easier to show than to tell apart by colour.
// Either kind can be recoloured with one of the PALETTE gradients (`color` in config.json).

type Glyph = { label: MessageKey; from: string; to: string; draw: (paint: string, id: string) => ReactNode };

/** Line icons a role can pick; drawn in the muted text colour unless it picked a colour. Keys share GLYPHS' space. */
export const LINES: Record<string, { label: MessageKey; icon: LucideIcon }> = {
  bot: { label: "agentGlyph.line.bot", icon: Bot },
  brain: { label: "agentGlyph.line.brain", icon: Brain },
  code: { label: "agentGlyph.line.code", icon: Code },
  terminal: { label: "agentGlyph.line.terminal", icon: Terminal },
  bug: { label: "agentGlyph.line.bug", icon: Bug },
  wrench: { label: "agentGlyph.line.wrench", icon: Wrench },
  search: { label: "agentGlyph.line.search", icon: Search },
  shield: { label: "agentGlyph.line.shield", icon: Shield },
  book: { label: "agentGlyph.line.book", icon: BookOpen },
  pen: { label: "agentGlyph.line.pen", icon: PenTool },
  feather: { label: "agentGlyph.line.feather", icon: Feather },
  palette: { label: "agentGlyph.line.palette", icon: Palette },
  flask: { label: "agentGlyph.line.flask", icon: FlaskConical },
  database: { label: "agentGlyph.line.database", icon: Database },
  globe: { label: "agentGlyph.line.globe", icon: Globe },
  chat: { label: "agentGlyph.line.chat", icon: MessageCircle },
  cap: { label: "agentGlyph.line.cap", icon: GraduationCap },
  briefcase: { label: "agentGlyph.line.briefcase", icon: Briefcase },
  compass: { label: "agentGlyph.line.compass", icon: Compass },
  rocket: { label: "agentGlyph.line.rocket", icon: Rocket },
  zap: { label: "agentGlyph.line.zap", icon: Zap },
  sparkles: { label: "agentGlyph.line.sparkles", icon: Sparkles },
  cat: { label: "agentGlyph.line.cat", icon: Cat },
  ghost: { label: "agentGlyph.line.ghost", icon: Ghost },
  gauge: { label: "agentGlyph.line.gauge", icon: Gauge },
  coins: { label: "agentGlyph.line.coins", icon: Coins },
  wallet: { label: "agentGlyph.line.wallet", icon: Wallet },
  timer: { label: "agentGlyph.line.timer", icon: Timer },
  calendar: { label: "agentGlyph.line.calendar", icon: Calendar },
  chart: { label: "agentGlyph.line.chart", icon: ChartColumn },
  bell: { label: "agentGlyph.line.bell", icon: Bell },
  activity: { label: "agentGlyph.line.activity", icon: Activity },
};

/** Colours a role can give its glyph: the glyphs' own gradients, ordered by hue. */
export const PALETTE: Record<string, { label: MessageKey; from: string; to: string }> = {
  red: { label: "agentGlyph.color.red", from: "#FCA5A5", to: "#B91C1C" },
  rose: { label: "agentGlyph.color.rose", from: "#FDBA74", to: "#E11D48" },
  orange: { label: "agentGlyph.color.orange", from: "#FCD34D", to: "#C2410C" },
  amber: { label: "agentGlyph.color.amber", from: "#FDE68A", to: "#D97706" },
  green: { label: "agentGlyph.color.green", from: "#BEF264", to: "#15803D" },
  teal: { label: "agentGlyph.color.teal", from: "#5EEAD4", to: "#0F766E" },
  cyan: { label: "agentGlyph.color.cyan", from: "#67E8F9", to: "#0E7490" },
  sky: { label: "agentGlyph.color.sky", from: "#7DD3FC", to: "#0369A1" },
  indigo: { label: "agentGlyph.color.indigo", from: "#A5B4FC", to: "#4F46E5" },
  violet: { label: "agentGlyph.color.violet", from: "#C4B5FD", to: "#7C3AED" },
  pink: { label: "agentGlyph.color.pink", from: "#F9A8D4", to: "#DB2777" },
  slate: { label: "agentGlyph.color.slate", from: "#CBD5E1", to: "#475569" },
};

const stroke = (paint: string, w = 2.2) => ({ fill: "none", stroke: paint, strokeWidth: w, strokeLinecap: "round" as const, strokeLinejoin: "round" as const });

export const GLYPHS: Record<string, Glyph> = {
  orbit: {
    label: "agentGlyph.glyph.orbit",
    from: "#FDBA74",
    to: "#E11D48",
    draw: (p) => (
      <>
        <circle cx="12" cy="12" r="5.5" fill={p} />
        <ellipse cx="12" cy="12" rx="10.5" ry="3.6" transform="rotate(-25 12 12)" {...stroke(p, 1.8)} />
      </>
    ),
  },
  spark: {
    label: "agentGlyph.glyph.spark",
    from: "#FDE68A",
    to: "#D97706",
    draw: (p) => <path fill={p} d="M12 1.5C12.8 8 16 11.2 22.5 12 16 12.8 12.8 16 12 22.5 11.2 16 8 12.8 1.5 12 8 11.2 11.2 8 12 1.5Z" />,
  },
  bloom: {
    label: "agentGlyph.glyph.bloom",
    from: "#F9A8D4",
    to: "#DB2777",
    draw: (p) => (
      <g fill={p} fillOpacity=".75">
        <circle cx="12" cy="7" r="5" />
        <circle cx="17" cy="12" r="5" />
        <circle cx="12" cy="17" r="5" />
        <circle cx="7" cy="12" r="5" />
      </g>
    ),
  },
  prism: {
    label: "agentGlyph.glyph.prism",
    from: "#C4B5FD",
    to: "#7C3AED",
    draw: (p) => <path fill={p} fillRule="evenodd" stroke={p} strokeWidth="1.5" strokeLinejoin="round" d="M12 3 21 19.5H3Z M12 10.5 7.9 17.5h8.2Z" />,
  },
  knot: {
    label: "agentGlyph.glyph.knot",
    from: "#A5B4FC",
    to: "#4F46E5",
    draw: (p) => <path {...stroke(p, 2.4)} d="M12 12C10 9 8 7.5 6 7.5a4.5 4.5 0 0 0 0 9c2 0 4-1.5 6-4.5s4-4.5 6-4.5a4.5 4.5 0 0 1 0 9c-2 0-4-1.5-6-4.5Z" />,
  },
  drop: {
    label: "agentGlyph.glyph.drop",
    from: "#7DD3FC",
    to: "#0369A1",
    draw: (p) => <path fill={p} d="M12 2.5S5 10 5 14.5a7 7 0 0 0 14 0C19 10 12 2.5 12 2.5Z" />,
  },
  wave: {
    label: "agentGlyph.glyph.wave",
    from: "#5EEAD4",
    to: "#0F766E",
    draw: (p) => <path {...stroke(p)} d="M3 7c3-3 6 3 9 0s6-3 9 0M3 12c3-3 6 3 9 0s6-3 9 0M3 17c3-3 6 3 9 0s6-3 9 0" />,
  },
  hex: {
    label: "agentGlyph.glyph.hex",
    from: "#BEF264",
    to: "#15803D",
    draw: (p) => (
      <>
        <path {...stroke(p)} d="M12 2.8 20 7.4v9.2l-8 4.6-8-4.6V7.4Z" />
        <circle cx="12" cy="12" r="3" fill={p} />
      </>
    ),
  },
  moon: {
    label: "agentGlyph.glyph.moon",
    from: "#CBD5E1",
    to: "#475569",
    draw: (p, id) => (
      <>
        <mask id={`${id}m`}>
          <rect width="24" height="24" fill="#fff" />
          <circle cx="17" cy="7.5" r="7.5" fill="#000" />
        </mask>
        <circle cx="12" cy="12" r="9.5" fill={p} mask={`url(#${id}m)`} />
      </>
    ),
  },
  layers: {
    label: "agentGlyph.glyph.layers",
    from: "#FCA5A5",
    to: "#B91C1C",
    draw: (p) => (
      <>
        <path fill={p} stroke={p} strokeWidth="1.5" strokeLinejoin="round" d="M12 3 21 8l-9 5-9-5Z" />
        <path {...stroke(p, 2)} d="m3 12.5 9 5 9-5M3 17l9 5 9-5" />
      </>
    ),
  },
  link: {
    label: "agentGlyph.glyph.link",
    from: "#67E8F9",
    to: "#0E7490",
    draw: (p) => (
      <>
        <circle cx="8.5" cy="12" r="6" {...stroke(p)} />
        <circle cx="15.5" cy="12" r="6" {...stroke(p)} />
      </>
    ),
  },
  tiles: {
    label: "agentGlyph.glyph.tiles",
    from: "#FCD34D",
    to: "#C2410C",
    draw: (p) => (
      <g fill={p}>
        <rect x="3" y="3" width="8" height="8" rx="2" />
        <rect x="13" y="3" width="8" height="8" rx="2" />
        <rect x="3" y="13" width="8" height="8" rx="2" />
        <circle cx="17" cy="17" r="4" />
      </g>
    ),
  },
};

/** Gerdr Agent (the built-in native one) always shows this; kept out of the automatic picks so no role looks like it. */
export const NATIVE_GLYPH = "orbit";

// automatic picks are the colourful glyphs, minus Gerdr Agent's
const AUTO = Object.keys(GLYPHS).filter((k) => k !== NATIVE_GLYPH);

/** A stable glyph for an agent that didn't pick one: same id → same icon on every device. */
export function autoGlyph(id: string): string {
  let h = 0;
  for (const c of id) h = (h * 31 + c.charCodeAt(0)) | 0;
  return AUTO[Math.abs(h) % AUTO.length];
}

/** color: a PALETTE key; none / unknown = the glyph's own colours. */
export function AgentGlyph({ name, color, className }: { name: string; color?: string; className?: string }) {
  const line = LINES[name];
  const key = line || GLYPHS[name] ? name : autoGlyph(name);
  const g = GLYPHS[key];
  const c = (color && PALETTE[color]) || (g ? { from: g.from, to: g.to } : undefined);
  // Gradients and masks need an id unique on the page (the same glyph is drawn many times). The glyph's name and colour
  // are part of it so that ids from two React roots that do collide still point at the same colours.
  const id = useId().replace(/[^\w-]/g, "") + key + (color ?? "");
  const grad = c && (
    <defs>
      <linearGradient id={id} gradientUnits="userSpaceOnUse" x1="4" y1="2" x2="20" y2="22">
        <stop stopColor={c.from} />
        <stop offset="1" stopColor={c.to} />
      </linearGradient>
    </defs>
  );
  if (line) {
    // lucide puts children inside its <svg>, so the gradient can live there too
    const Icon = line.icon;
    return (
      <Icon className={cn("shrink-0", !c && "text-muted-foreground", className)} stroke={c ? `url(#${id})` : "currentColor"} aria-label={t(line.label)}>
        {grad}
      </Icon>
    );
  }
  return (
    <svg viewBox="0 0 24 24" className={cn("shrink-0", className)} aria-label={t(g.label)}>
      {grad}
      {g.draw(`url(#${id})`, id)}
    </svg>
  );
}
