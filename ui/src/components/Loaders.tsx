import { useEffect, useState, type CSSProperties } from "react";
import { t } from "@/lib/i18n";
import type { MessageKey } from "@/lib/messages";
import { cn } from "@/lib/utils";

// Gerdr's own working indicators besides the chat's 3×3 orbit (config.loader "builtin:<id>", picked in Settings →
// General → Loading 动画), so there is something to choose without installing an add-on. Written here after the looks
// of beUI Pro's "AI Thinking Loaders" (grid scan, diagonal wave, assembling tiles, diamond loop, connected nodes; its
// orbit is the chat's default) plus the terminal's braille spinner and an equalizer — our own code, plain CSS (the
// keyframes are in index.css), no animation library. They live in the app, not in @gerdr/chat: adding one must not
// cost a package release.
//
// All share the default's frame: a 14px box (size-3.5), coloured by currentColor (the caller's text-brand /
// text-warn), and `paused` (the turn waits on the user) stops the motion and breathes the whole thing.

type Props = { className?: string; paused?: boolean };

const N = 3;
const CELL = "size-[3.5px] rounded-[1px] bg-current";

/**
 * A 3×3 grid whose cells blink with a delay (seconds, negative = already under way) from `delay`; "dim" stays dim,
 * null leaves the cell out.
 */
function Grid({ delay, cls = "ld-blink", className, paused }: Props & { delay: (r: number, c: number) => number | "dim" | null; cls?: string }) {
  return (
    <Box className={className} paused={paused}>
      <span className="inline-grid grid-cols-3 gap-px">
        {Array.from({ length: N * N }, (_, k) => {
          const d = delay(Math.floor(k / N), k % N);
          if (d === null) return <i key={k} className={cn(CELL, "opacity-0")} />;
          if (d === "dim" || paused) return <i key={k} className={cn(CELL, "opacity-20")} />;
          return <i key={k} className={cn(CELL, cls)} style={{ animationDelay: `${d}s` }} />;
        })}
      </span>
    </Box>
  );
}

function Box({ className, paused, children }: Props & { children: React.ReactNode }) {
  return (
    <span aria-hidden className={cn("flex size-3.5 shrink-0 items-center justify-center", className)}>
      <span className={cn("flex items-center justify-center", paused && "ld-paused")}>{children}</span>
    </span>
  );
}

// Grid scan: a bright row sweeps down, like reading lines
const Scan = (p: Props) => <Grid {...p} delay={(r) => (r / N - 1) * 1.2} />;

// Diagonal wave: the light rolls from the top-left corner to the bottom-right
const Wave = (p: Props) => <Grid {...p} delay={(r, c) => ((r + c) / (2 * N - 1) - 1) * 1.2} />;

// Assembling tiles: tiles pop in one at a time in a shuffled order, hold, and leave the same way
const ORDER = [4, 0, 8, 2, 6, 1, 7, 3, 5];
const Assemble = (p: Props) => <Grid {...p} cls="ld-tile" delay={(r, c) => (ORDER.indexOf(r * N + c) / ORDER.length - 1) * 1.8} />;

// Diamond loop: a light goes round the four edge cells, the corners stay empty, the centre dim
const DIAMOND = [1, 5, 7, 3]; // top, right, bottom, left
const Diamond = (p: Props) => (
  <Grid
    {...p}
    delay={(r, c) => {
      const i = DIAMOND.indexOf(r * N + c);
      return i >= 0 ? (i / DIAMOND.length - 1) * 1.2 : r === 1 && c === 1 ? "dim" : null;
    }}
  />
);

// The terminal spinner Claude Code and most CLIs draw (braille dots stepping round)
const FRAMES = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"];
function Braille({ className, paused }: Props) {
  const [i, setI] = useState(0);
  useEffect(() => {
    if (paused) return;
    const id = setInterval(() => setI((x) => (x + 1) % FRAMES.length), 80);
    return () => clearInterval(id);
  }, [paused]);
  return (
    <Box className={className} paused={paused}>
      <span className="font-mono text-[13px] leading-none">{FRAMES[i]}</span>
    </Box>
  );
}

// Equalizer: three bars bouncing out of step
function Bars({ className, paused }: Props) {
  return (
    <Box className={className} paused={paused}>
      <span className="flex h-2.5 items-end gap-[1.5px]">
        {[0, 1, 2].map((i) => (
          <i
            key={i}
            className={cn("h-full w-[2.5px] rounded-[1px] bg-current", paused ? "scale-y-50 opacity-60" : "ld-bar")}
            style={paused ? undefined : ({ animationDelay: `${(i / 3 - 1) * 1}s`, transformOrigin: "bottom" } as CSSProperties)}
          />
        ))}
      </span>
    </Box>
  );
}

// Connected nodes: three nodes joined in a triangle, lighting up in turn as if passing a thought along
const NODES: [number, number][] = [
  [7, 2.5],
  [11.5, 10.5],
  [2.5, 10.5],
];
function Nodes({ className, paused }: Props) {
  return (
    <Box className={className} paused={paused}>
      {/* text-current: menus (shadcn SelectItem) grey out an svg that has no text- class */}
      <svg viewBox="0 0 14 13" className="size-3.5 text-current" fill="currentColor" stroke="currentColor">
        <path d="M7 2.5L11.5 10.5H2.5Z" fill="none" strokeWidth="1" strokeOpacity=".3" strokeLinejoin="round" />
        {NODES.map(([x, y], i) => (
          <circle
            key={i}
            cx={x}
            cy={y}
            r="2"
            stroke="none"
            className={paused ? undefined : "ld-node"}
            opacity={paused ? 0.6 : undefined}
            style={paused ? undefined : { animationDelay: `${(i / 3 - 1) * 1.5}s` }}
          />
        ))}
      </svg>
    </Box>
  );
}

export const BUILTIN_LOADERS: { id: string; name: MessageKey; C: (p: Props) => React.ReactNode }[] = [
  { id: "scan", name: "settings.loader.scan", C: Scan },
  { id: "wave", name: "settings.loader.wave", C: Wave },
  { id: "assemble", name: "settings.loader.assemble", C: Assemble },
  { id: "diamond", name: "settings.loader.diamond", C: Diamond },
  { id: "nodes", name: "settings.loader.nodes", C: Nodes },
  { id: "bars", name: "settings.loader.bars", C: Bars },
  { id: "braille", name: "settings.loader.braille", C: Braille },
];

export const builtinLoader = (id: string) => BUILTIN_LOADERS.find((l) => l.id === id);
export const loaderName = (l: (typeof BUILTIN_LOADERS)[number]) => t(l.name);
