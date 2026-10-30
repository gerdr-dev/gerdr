import { Play } from "lucide-react";

/** The round play mark over a video thumbnail (chat bubble, composer): the video plays only once opened in the lightbox. */
export function PlayBadge({ small }: { small?: boolean }) {
  return (
    <div className="pointer-events-none absolute inset-0 flex items-center justify-center">
      <span
        className={`flex items-center justify-center rounded-full bg-background/70 text-foreground shadow-sm ring-1 ring-border backdrop-blur ${small ? "size-7" : "size-10"}`}
      >
        <Play className={`fill-current ${small ? "size-3.5" : "size-4"} translate-x-px`} />
      </span>
    </div>
  );
}
