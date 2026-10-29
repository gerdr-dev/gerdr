import { useLayoutEffect, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import { moveWorkspace } from "@/lib/actions";

/*
 * Reordering workspaces in the sidebar by dragging, done the same way as the tab bar (TabDrag.tsx) so the two feel alike:
 * pointer events + setPointerCapture instead of HTML5 drag and drop (whose ghost is a translucent browser screenshot we can't style),
 * the dragged row stays put as a faded placeholder while a copy follows the pointer, a line marks the gap it will land in, and the
 * other rows don't make room (if they did, what's under the pointer would keep moving and the drop point would jump).
 * After the drop the moved rows slide into place (FLIP).
 *
 * The ghost only moves vertically: it's a list, and a row wandering sideways over the chat looks like it's about to be dropped there.
 * Rows keep data-ws-id, which the tab drag also uses to drop a tab onto a workspace.
 */

type Drag = {
  wsId: string;
  from: number;
  y: number;
  offY: number;
  left: number;
  w: number;
  h: number;
  label: string;
  /** insert before the to-th row (original indexes); null = would land where it already is */
  to: number | null;
  lineY: number;
};

let current: Drag | null = null;
const subs = new Set<() => void>();
const setDrag = (d: Drag | null) => {
  current = d;
  for (const f of subs) f();
};
const subscribe = (f: () => void) => (subs.add(f), () => void subs.delete(f));
const useDrag = () => useSyncExternalStore(subscribe, () => current);

let flipPending: Map<string, number> | null = null;
const rowsIn = (list: HTMLElement) => [...list.querySelectorAll<HTMLElement>("[data-ws-id]")];

/** order: the workspace ids in the list, so the FLIP runs after a reorder renders. */
export function useWorkspaceDrag(list: React.RefObject<HTMLDivElement | null>, order: string) {
  const drag = useDrag();
  useLayoutEffect(() => {
    const prev = flipPending;
    flipPending = null;
    if (!prev || !list.current) return;
    for (const el of rowsIn(list.current)) {
      const old = prev.get(el.dataset.wsId!);
      if (old == null) continue;
      const dy = old - el.getBoundingClientRect().top;
      if (Math.abs(dy) < 1) continue;
      el.animate([{ transform: `translateY(${dy}px)` }, { transform: "none" }], { duration: 200, easing: "cubic-bezier(0.2, 0.8, 0.2, 1)" });
    }
  }, [order]);
  return {
    drag,
    onPointerDown: (e: React.PointerEvent<HTMLElement>, wsId: string, from: number, label: string) => {
      if (list.current) start(e, list.current, wsId, from, label);
    },
  };
}

function start(e: React.PointerEvent<HTMLElement>, list: HTMLElement, wsId: string, from: number, label: string) {
  // Left button / pen only; a finger on the sidebar scrolls it
  if (e.button !== 0 || e.pointerType === "touch") return;
  const el = e.currentTarget;
  const r = el.getBoundingClientRect();
  const sy = e.clientY;
  const sx = e.clientX;
  const pid = e.pointerId;
  let d: Drag | null = null;
  const update = (next: Drag | null) => {
    d = next;
    setDrag(next);
  };

  const locate = (y: number): { to: number | null; lineY: number } => {
    const rs = rowsIn(list).map((x) => x.getBoundingClientRect());
    let ins = rs.findIndex((x) => y < x.top + x.height / 2);
    if (ins < 0) ins = rs.length;
    // rows touch (no gap), so the line sits on the boundary
    const lineY = ins < rs.length ? rs[ins].top : rs[rs.length - 1].bottom;
    if (ins === from || ins === from + 1) return { to: null, lineY };
    return { to: ins > from ? ins - 1 : ins, lineY };
  };

  const move = (ev: PointerEvent) => {
    if (ev.pointerId !== pid) return;
    if (!d) {
      if (Math.hypot(ev.clientX - sx, ev.clientY - sy) < 5) return;
      try {
        el.setPointerCapture(pid);
      } catch {}
      document.body.style.cursor = "grabbing";
      document.body.style.userSelect = "none";
      d = { wsId, from, y: sy, offY: sy - r.top, left: r.left, w: r.width, h: r.height, label, to: null, lineY: 0 };
    }
    update({ ...d, y: ev.clientY, ...locate(ev.clientY) });
  };

  const finish = (commit: boolean) => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", up);
    window.removeEventListener("pointercancel", cancel);
    window.removeEventListener("keydown", key, true);
    if (!d) return;
    document.body.style.cursor = "";
    document.body.style.userSelect = "";
    try {
      el.releasePointerCapture(pid);
    } catch {}
    // The browser still fires a click after the drag (down and up on the same row): don't let it select the workspace
    const eat = (ev: MouseEvent) => (ev.stopPropagation(), ev.preventDefault());
    window.addEventListener("click", eat, true);
    setTimeout(() => window.removeEventListener("click", eat, true), 0);
    if (commit && d.to != null) {
      const m = new Map<string, number>();
      for (const x of rowsIn(list)) m.set(x.dataset.wsId!, x.getBoundingClientRect().top);
      m.set(wsId, d.y - d.offY); // the dragged one slides in from where the ghost was
      flipPending = m;
      moveWorkspace(wsId, d.to);
    }
    update(null);
  };
  const up = (ev: PointerEvent) => ev.pointerId === pid && finish(true);
  const cancel = (ev: PointerEvent) => ev.pointerId === pid && finish(false);
  const key = (ev: KeyboardEvent) => {
    if (ev.key !== "Escape" || !d) return;
    ev.preventDefault();
    ev.stopPropagation();
    finish(false);
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", up);
  window.addEventListener("pointercancel", cancel);
  window.addEventListener("keydown", key, true);
}

/** The ghost row and the drop line, drawn on top of everything; neither takes pointer events. */
export function WorkspaceDragLayer({ index }: { index: (wsId: string) => number }) {
  const drag = useDrag();
  if (!drag) return null;
  return createPortal(
    <div className="pointer-events-none fixed inset-0 z-[100]">
      <div
        className="fixed flex items-center gap-2 rounded-md bg-popover px-2 text-[13px] font-medium text-popover-foreground shadow-lg ring-1 ring-border"
        style={{ left: drag.left, top: drag.y - drag.offY, width: drag.w, height: drag.h }}
      >
        <span className="w-4 shrink-0 text-center font-mono text-[11px] text-muted-foreground">{index(drag.wsId) + 1}</span>
        <span className="min-w-0 flex-1 truncate">{drag.label}</span>
      </div>
      {drag.to != null && (
        <div
          className="fixed h-0.5 rounded-full bg-brand shadow-[0_0_0_1px_var(--background)]"
          style={{ left: drag.left + 4, top: drag.lineY - 1, width: drag.w - 8 }}
        />
      )}
    </div>,
    document.body,
  );
}
