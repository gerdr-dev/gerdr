import { ArrowUpRight, GripVertical, Maximize2, Minimize2, Minus, Pin, PinOff, X } from "lucide-react";
import { useEffect } from "react";
import { Button } from "@/components/ui/button";
import { basename, focusSession } from "@/lib/actions";
import { removeActive, toggleMain, toggleOverview } from "@/lib/overview";
import { useWindowInFront } from "@/lib/front";
import { markRead, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { FadeText } from "./FadeText";
import { BorderFlow, useSlowCatchUp } from "./BorderFlow";
import { AgentIcon, StatusDot, dotStatus } from "./AgentIcon";
import { ChatView } from "./chat/ChatView";
import { DRAG_TOUCH, statusTint, whereOf } from "./tileInfo";
import { t } from "@/lib/i18n";

/** A tile as a small chip tinted with its status: the phone's tile switcher, and the tiles that didn't fit in the grid. */
export function TileChip({ id, current, className, onClick }: { id: string; current: boolean; className?: string; onClick: () => void }) {
  const s = useStore((st) => st.sessions[id]);
  const unread = useStore((st) => !!st.unread[id]);
  // Picked and its tile still loading after a moment (PhoneOverview keeps the old tile up meanwhile): the current chip
  // has a brand border, so the flow runs as a gap in it (BorderFlow draws the border meanwhile, in the same place)
  const loading = useSlowCatchUp(current ? id : undefined);
  if (!s) return null;
  return (
    <button
      data-current={current || undefined}
      className={cn(
        "relative flex shrink-0 items-center gap-1.5 rounded-md border px-2 text-[12px]",
        statusTint(s.status, unread),
        current ? cn("font-medium", loading ? "border-transparent" : "border-brand") : "border-transparent text-muted-foreground",
        className,
      )}
      onClick={onClick}
    >
      {loading && <BorderFlow mode="gap" color="var(--brand)" />}
      <FadeText>{s.title || basename(s.cwd)}</FadeText>
      {/* After the title (asked for). An idle one has no dot: StatusDot is transparent then but would still leave a gap */}
      {(dotStatus(s) !== "idle" || unread) && <StatusDot status={dotStatus(s)} unread={unread} />}
    </button>
  );
}

export function Tile({
  id,
  focused,
  main,
  zoomed,
  target,
  onFocus,
  onZoom,
  onMinimize,
  onDragStart,
  pin,
  phone,
}: {
  id: string;
  focused: boolean;
  main: boolean;
  zoomed: boolean;
  /** Another tile is being dragged over this one: dropping swaps them. */
  target: boolean;
  onFocus: () => void;
  onZoom: () => void;
  /** Fold it into the grid's "more" row (the grid only: a phone shows one tile at a time). */
  onMinimize?: () => void;
  onDragStart?: (e: React.PointerEvent, label: string) => void;
  /** Tile size at the end of a zoom animation in progress: the chat is laid out at that size meanwhile (clipped). */
  pin?: { width: number; height: number };
  /** Shown alone on a phone: no pinning / zooming there. */
  phone?: boolean;
}) {
  const session = useStore((s) => s.sessions[id]);
  const unread = useStore((s) => !!s.unread[id]);
  const where = useStore((s) => whereOf(s.layout, id, !phone));
  const front = useWindowInFront();
  // Only while the window is in front (lib/front.ts)
  useEffect(() => {
    if (front && focused && unread) markRead(id);
  }, [front, focused, unread, id]);
  if (!session) return null;
  const title = session.title || basename(session.cwd);
  const jump = () => {
    toggleOverview(false);
    focusSession(id);
  };
  return (
    <div
      className={cn(
        "flex h-full min-h-0 flex-col overflow-clip bg-background",
        !phone && "rounded-md border",
        focused ? "border-brand" : "border-border",
        target && "ring-2 ring-brand",
      )}
      onMouseDown={() => !focused && onFocus()}
    >
      <div
        className={cn(
          // max-md:pl-4 like a pane's title bar (Panes.tsx): on a phone the two show in the same spot, so the icons line up
          "flex h-8 shrink-0 items-center gap-2 border-b px-3 text-[12px] max-md:pl-4",
          // The status is the whole title bar's tint instead of a dot (asked for): readable at a glance across a dozen tiles.
          // Dimming an unfocused tile goes on the contents only, so the tint keeps its strength.
          statusTint(session.status, unread),
          !focused && "[&>*]:opacity-70",
          // touch-none: the bar is a drag handle (track), and it has nothing to scroll
          onDragStart && `cursor-grab ${DRAG_TOUCH}`,
        )}
        onDoubleClick={onZoom}
        onPointerDown={onDragStart && ((e) => onDragStart(e, title))}
        // Middle click closes it, like a browser tab (mousedown is cancelled so the browser doesn't start auto-scroll)
        onMouseDown={(e) => e.button === 1 && e.preventDefault()}
        onAuxClick={(e) => e.button === 1 && removeActive(id)}
        title={t("overviewTile.dragHint")}
      >
        {onDragStart && <GripVertical className="-mx-1.5 size-3.5 shrink-0 text-muted-foreground/60" />}
        <AgentIcon agent={session.agent} className="size-4" />
        {/* A phone has no buttons to leave room for: the title takes the row, the place gets what's left (capped) */}
        <FadeText className={cn("min-w-[3em] shrink font-medium", !phone && "max-w-[50%]")}>{title}</FadeText>
        <StatusDot status={dotStatus(session)} unread={unread} />
        {session.pending ? (
          <span className="shrink-0 whitespace-nowrap rounded-sm bg-bad/15 px-1.5 text-[11px] text-bad">{t("overviewTile.waiting")}</span>
        ) : null}
        <FadeText className={cn("min-w-0 text-[11px] text-muted-foreground", phone ? "max-w-[35%] shrink-0" : "flex-1")}>{where}</FadeText>
        {phone && <div className="flex-1" />}
        {!phone && (
          <>
            {onMinimize && !zoomed && (
              <TileButton title={t("overviewTile.minimize")} onClick={onMinimize}>
                <Minus className="size-3.5" />
              </TileButton>
            )}
            <TileButton title={main ? t("overviewTile.unpin") : t("overviewTile.pinLeft")} onClick={() => toggleMain(id)}>
              {main ? <PinOff className="size-3.5" /> : <Pin className="size-3.5" />}
            </TileButton>
            <TileButton title={zoomed ? t("overviewTile.restore") : t("overviewTile.zoom")} onClick={onZoom}>
              {zoomed ? <Minimize2 className="size-3.5" /> : <Maximize2 className="size-3.5" />}
            </TileButton>
          </>
        )}
        <TileButton title={t("overviewTile.backToTab")} onClick={jump}>
          <ArrowUpRight className="size-3.5" />
        </TileButton>
        <TileButton title={t("overviewTile.done")} onClick={() => removeActive(id)}>
          <X className="size-3.5" />
        </TileButton>
      </div>
      <div className="min-h-0 flex-1 overflow-clip">
        {/* Own pane id: the composer's draft here is separate from the one in the session's real pane */}
        {/* Minus the 1px frame on each side and the 32px + 1px title bar */}
        <div className="h-full w-full" style={pin && { width: pin.width - 2, height: pin.height - 35 }}>
          <ChatView key={id} session={session} paneId={`ov-${id}`} focused={focused} />
        </div>
      </div>
    </div>
  );
}

function TileButton({ title, onClick, children }: { title: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <Button variant="ghost" size="icon" className="size-6 shrink-0" title={title} onClick={onClick} onDoubleClick={(e) => e.stopPropagation()}>
      {children}
    </Button>
  );
}
