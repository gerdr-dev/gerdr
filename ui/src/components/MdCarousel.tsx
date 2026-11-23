import { ChevronLeft, ChevronRight } from "lucide-react";
import { type ReactNode, useRef, useState } from "react";
import { t } from "@/lib/i18n";
import { pageX, useSwipePager } from "@/lib/swipePager";

/** Antigravity (Gemini) writes `<!-- slide -->` on its own line between the slides of a ```carousel block. */
export const splitSlides = (code: string) =>
  code
    .split(/^[ \t]*<!--\s*slide\s*-->[ \t]*$/m)
    .map((s) => s.trim())
    .filter(Boolean);

/**
 * A ```carousel block (Antigravity's Markdown extension for side-by-side proposals, screenshots…) as pages to flip
 * through, each page Markdown drawn by `render` (the .md viewer's own renderer, so local images resolve the same way).
 * Two ways of moving, by device:
 * - Touch (a phone, a tablet): the image lightbox's pager (useSwipePager), so a swipe feels the same as there: the page
 *   follows the finger, a flick turns it, a finger landing mid-slide swipes on. Native scroll-snap snapped with each
 *   browser's own physics there, unlike the lightbox. The pages share one grid cell, each moved to its place by a
 *   transform, so the block is as tall as the tallest page. No wrap-around (a document's pages have an order).
 * - A mouse / trackpad: native horizontal scroll with snapping. The pager only knows touches, and a trackpad fling
 *   through several pages at once is smoother than anything we'd do with wheel events (asked to keep it).
 */
export function MdCarousel({ code, render }: { code: string; render: (md: string) => ReactNode }) {
  const slides = splitSlides(code);
  const [touch] = useState(() => matchMedia("(pointer: coarse)").matches);
  const body = useRef<HTMLDivElement>(null);
  const track = useRef<HTMLDivElement>(null);
  const [at, setAt] = useState(0);
  const pager = useSwipePager(
    body,
    track,
    touch && slides.length > 1 ? String(at) : undefined,
    at,
    (step) => (slides[at + step] !== undefined ? String(at + step) : undefined),
    () => {},
    (id) => setAt(Number(id)),
  );
  const go = (step: number) => {
    if (!step) return;
    if (touch) return pager.turn(step);
    const el = body.current;
    if (el) el.scrollTo({ left: Math.max(0, Math.min(slides.length - 1, at + step)) * el.clientWidth, behavior: "smooth" });
  };
  const page = "min-w-0 px-4 py-2 [&_img]:mx-auto [&_img]:max-h-[60vh]";
  return (
    <div
      className="not-prose my-4 flex flex-col rounded-xl border border-border outline-none"
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === "ArrowLeft") (e.preventDefault(), go(-1));
        else if (e.key === "ArrowRight") (e.preventDefault(), go(1));
      }}
    >
      {touch ? (
        <div ref={body} className="overflow-hidden">
          <div ref={track} className="grid will-change-transform">
            {slides.map((s, i) => (
              <div key={i} className={`${page} [grid-area:1/1]`} style={{ transform: pageX(i) }} inert={i !== at}>
                {render(s)}
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div
          ref={body}
          className="flex snap-x snap-mandatory overflow-x-auto [scrollbar-width:none]"
          onScroll={(e) => setAt(Math.round(e.currentTarget.scrollLeft / Math.max(1, e.currentTarget.clientWidth)))}
        >
          {slides.map((s, i) => (
            <div key={i} className={`${page} w-full shrink-0 snap-center`}>
              {render(s)}
            </div>
          ))}
        </div>
      )}
      {slides.length > 1 && (
        <div className="flex items-center justify-center gap-3 border-t border-border py-1.5 text-xs text-muted-foreground">
          <button
            type="button"
            title={t("mdCarousel.prev")}
            disabled={at <= 0}
            onClick={() => go(-1)}
            className="rounded-md p-1 hover:bg-accent disabled:opacity-30"
          >
            <ChevronLeft className="size-4" />
          </button>
          <div className="flex items-center gap-1.5">
            {slides.map((_, i) => (
              <button
                key={i}
                type="button"
                aria-label={`${i + 1}`}
                onClick={() => go(i - at)}
                className={`size-1.5 rounded-full ${i === at ? "bg-foreground" : "bg-muted-foreground/40"}`}
              />
            ))}
          </div>
          <span className="tabular-nums">
            {at + 1} / {slides.length}
          </span>
          <button
            type="button"
            title={t("mdCarousel.next")}
            disabled={at >= slides.length - 1}
            onClick={() => go(1)}
            className="rounded-md p-1 hover:bg-accent disabled:opacity-30"
          >
            <ChevronRight className="size-4" />
          </button>
        </div>
      )}
    </div>
  );
}
