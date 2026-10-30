import { cn } from "../utils";

// 运行中的 loading（照 beUI 的 AI Thinking Loaders · Pixel orbit，自己写的）：3×3 小方块，外圈一颗带尾巴的光点在转。
// 外圈 8 格按顺时针排（[行, 列]）。每格跑同一段「亮 → 渐暗」的动画，起点按位置错开，连起来就是在绕圈。中间 1 格常暗，当底。
// （最早是 4×4、每格 3px，太碎；换成 3×3。每格 3.5px：4px 嫌大，3.5px 在 2 倍屏上正好 7 个物理像素，不糊。）
// （试过用 logo 那条狗当 loading：整条狗 16px 看不清，狗脸又画不好看，回到转圈。）
// 一行一排格子，看得出是一圈（别让 prettier 打散）
// prettier-ignore
const RING: [number, number][] = [
  [0, 0], [0, 1], [0, 2],
  [1, 2], [2, 2],
  [2, 1], [2, 0],
  [1, 0],
];
const N = 3;
const DUR = 1.2; // 秒，转一圈

/**
 * 颜色跟 currentColor 走，外面用 text-brand / text-warn 定。
 * paused：停着等人（比如等审批）—— 不转，整体慢慢呼吸，看得出「在等，不是在跑」。
 */
export function PixelLoader({ className, paused }: { className?: string; paused?: boolean }) {
  const ring = new Map(RING.map(([r, c], i) => [r * N + c, i]));
  return (
    // 外面一层占 14px（size-3.5），和工具行、组头的图标一样宽、图形居中：竖着看图标一条线，后面的字也从同一处开始
    <span aria-hidden className={cn("flex size-3.5 shrink-0 items-center justify-center", className)}>
      <span className={cn("inline-grid grid-cols-3 gap-px", paused && "animate-[gerdr-pixel-breathe_1.6s_ease-in-out_infinite]")}>
        {Array.from({ length: N * N }, (_, k) => {
          const i = ring.get(k);
          return (
            <i
              key={k}
              className={cn("size-[3.5px] rounded-[1px] bg-current", i === undefined || paused ? "opacity-20" : "gerdr-pixel-orbit")}
              style={i === undefined || paused ? undefined : { animationDelay: `${(i / RING.length - 1) * DUR}s` }}
            />
          );
        })}
      </span>
    </span>
  );
}
