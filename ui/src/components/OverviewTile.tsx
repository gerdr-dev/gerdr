import { ArrowUpRight, GripVertical, Maximize2, Minimize2, Pin, PinOff, X } from "lucide-react";
import { useEffect } from "react";
import { Button } from "@/components/ui/button";
import { basename, focusSession } from "@/lib/actions";
import { locateSession } from "@/lib/layout";
import { removeActive, toggleMain, toggleOverview } from "@/lib/overview";
import { markRead, useStore } from "@/lib/store";
import { cn } from "@/lib/utils";
import { FadeText } from "./FadeText";
import { BorderFlow, useSlowCatchUp } from "./BorderFlow";
import { AgentIcon, StatusDot, dotStatus } from "./AgentIcon";
import { ChatView } from "./chat/ChatView";
import { DRAG_TOUCH, statusTint, whereOf } from "./tileInfo";

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
      {/* An idle one has no dot: StatusDot is transparent then but would still leave a gap */}
      {(dotStatus(s) !== "idle" || unread) && <StatusDot status={dotStatus(s)} unread={unread} />}
      <FadeText>{s.title || basename(s.cwd)}</FadeText>
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
  onDragStart?: (e: React.PointerEvent, label: string) => void;
  /** Tile size at the end of a zoom animation in progress: the chat is laid out at that size meanwhile (clipped). */
  pin?: { width: number; height: number };
  /** Shown alone on a phone: no pinning / zooming there. */
  phone?: boolean;
}) {
  const session = useStore((s) => s.sessions[id]);
  const unread = useStore((s) => !!s.unread[id]);
  const where = useStore((s) => whereOf(s.layout, s.config, id));
  const inWork = useStore((s) => !locateSession(s.layout, id)); // a Work conversation (no pane)
  useEffect(() => {
    if (focused && unread) markRead(id);
  }, [focused, unread, id]);
  if (!session) return null;
  const title = session.title || basename(session.cwd);
  // A Work conversation goes to Work mode (actions.openInWork), any other to its pane
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
        title="拖到别的格子边上放到它旁边，拖到中间交换位置；双击放大，中键关闭"
      >
        {onDragStart && <GripVertical className="-mx-1.5 size-3.5 shrink-0 text-muted-foreground/60" />}
        <AgentIcon agent={session.agent} className="size-4" />
        <FadeText className="min-w-[3em] max-w-[50%] shrink font-medium">{title}</FadeText>
        <StatusDot status={dotStatus(session)} unread={unread} />
        {session.pending ? <span className="shrink-0 whitespace-nowrap rounded-sm bg-bad/15 px-1.5 text-[11px] text-bad">等你</span> : null}
        <FadeText className="min-w-0 flex-1 text-[11px] text-muted-foreground">{where}</FadeText>
        {!phone && (
          <>
            <TileButton title={main ? "取消固定" : "固定在左边"} onClick={() => toggleMain(id)}>
              {main ? <PinOff className="size-3.5" /> : <Pin className="size-3.5" />}
            </TileButton>
            <TileButton title={zoomed ? "还原" : "放大（双击标题栏也行）"} onClick={onZoom}>
              {zoomed ? <Minimize2 className="size-3.5" /> : <Maximize2 className="size-3.5" />}
            </TileButton>
          </>
        )}
        <TileButton title={inWork ? "去 Work 模式" : "回到它所在的 tab"} onClick={jump}>
          <ArrowUpRight className="size-3.5" />
        </TileButton>
        <TileButton title="完成，移出进行中（agent 不受影响）" onClick={() => removeActive(id)}>
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
