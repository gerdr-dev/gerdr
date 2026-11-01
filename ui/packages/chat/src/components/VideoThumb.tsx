import { useState } from "react";
import { cn } from "../utils";

// A still of a video (chat bubble, composer); it plays only once opened in the lightbox.
//
// A bare <video> drew wrong for a few frames on every remount (switching back to a session, a row scrolled back into
// view; a phone screen recording): until its metadata arrives it lays out at the default 300x150, so the bubble came
// up too wide, and until the first frame decodes Android's WebView paints its own grey poster with a big play circle.
// So each video's ratio and first frame are kept per URL: a remount draws the saved frame as a plain <img> at the
// right size (no video load at all), and a video seen for the first time stays invisible over the muted box until a
// frame is there to show. (Not a blank poster attribute instead: a poster stays up until playback starts, so the
// still would never show.)
const seen = new Map<string, { ratio: number; frame?: string }>();

// Height of the saved frame in pixels: thumbnails are at most 10rem tall, this covers that at 2x
const FRAME_H = 320;

function grab(v: HTMLVideoElement): string | undefined {
  if (!v.videoWidth || !v.videoHeight) return;
  const h = Math.min(FRAME_H, v.videoHeight);
  const c = document.createElement("canvas");
  c.width = Math.round((v.videoWidth / v.videoHeight) * h);
  c.height = h;
  try {
    c.getContext("2d")?.drawImage(v, 0, 0, c.width, c.height);
    return c.toDataURL("image/jpeg", 0.8);
  } catch {
    return; // cross-origin video taints the canvas: no saved frame, the video loads again next time
  }
}

/**
 * fill: cover the parent's box (the composer's fixed square). Otherwise heightRem tall and as wide as the video's
 * ratio makes it (16:9 until a video's ratio is first known); the parent clips anything wider than the column.
 */
export function VideoThumb({ url, fill, heightRem = 10 }: { url: string; fill?: boolean; heightRem?: number }) {
  const key = url.split("#")[0];
  const [info, setInfo] = useState(() => seen.get(key));
  const [shown, setShown] = useState(false);
  const ratio = info?.ratio ?? 16 / 9;
  const box = fill
    ? { className: "size-full object-cover" }
    : { className: "object-cover", style: { height: `${heightRem}rem`, width: `${(heightRem * ratio).toFixed(3)}rem`, maxWidth: "none" } };
  if (info?.frame) return <img src={info.frame} alt="" draggable={false} {...box} className={cn("pointer-events-none", box.className)} />;
  const save = (v: HTMLVideoElement) => {
    if (!v.videoWidth || !v.videoHeight) return;
    const next = { ratio: v.videoWidth / v.videoHeight, frame: grab(v) };
    seen.set(key, next);
    // Keep the live <video> on this mount (swapping it for the <img> would just redraw the same frame); only the
    // width follows the real ratio
    if (Math.abs(next.ratio - ratio) > 0.01) setInfo({ ratio: next.ratio });
  };
  return (
    <video
      src={`${key}#t=0.1`}
      muted
      playsInline
      preload="metadata"
      onLoadedMetadata={(e) => {
        const v = e.currentTarget;
        if (v.videoWidth && v.videoHeight && !seen.has(key)) {
          const r = v.videoWidth / v.videoHeight;
          seen.set(key, { ratio: r });
          if (Math.abs(r - ratio) > 0.01) setInfo({ ratio: r });
        }
      }}
      // loadeddata: the frame at the current position is decodable. The #t=0.1 seek may land after it, so seeked
      // grabs again (the later frame wins)
      onLoadedData={(e) => {
        setShown(true);
        save(e.currentTarget);
      }}
      onSeeked={(e) => save(e.currentTarget)}
      {...box}
      className={cn("pointer-events-none transition-opacity duration-100", !shown && "opacity-0", box.className)}
    />
  );
}
