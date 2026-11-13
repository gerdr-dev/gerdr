import { useSyncExternalStore } from "react";
import { moveSpace } from "@/lib/actions";

/*
 * Dragging a project chip (the rail's, or the sidebar header's) along its list reorders the projects. Pointer events, not
 * HTML5 drag and drop, as in TabDrag.tsx: the chip itself follows the pointer along the list's axis and a line marks the gap
 * it will drop into; the other chips stay put (moving them would move the drop target under the pointer). A mouse / pen
 * only: on a touch screen the finger scrolls the rail and a long press opens the menu, which has 上移 / 下移 instead.
 *
 * The list is the closest [data-space-list] (position: relative, the line is drawn in it); its chips are its
 * [data-space-id] elements, in order. The rail's panel buttons are dragged the same way (`onDrop` moves a panel instead
 * of a project).
 */

type State = { list: string; id: string; to: number; line: number } | null;

let cur: State = null;
const subs = new Set<() => void>();
const setState = (s: State) => {
  cur = s;
  for (const f of subs) f();
};
const subscribe = (f: () => void) => (subs.add(f), () => void subs.delete(f));
/** The drag going on in list `list` (its data-space-list value), null when none. */
export const useSpaceDrag = (list: string) => useSyncExternalStore(subscribe, () => (cur?.list === list ? cur : null));

export function startSpaceDrag(e: React.PointerEvent<HTMLElement>, id: string, axis: "x" | "y", onDrop: (id: string, to: number) => void = moveSpace) {
  if (e.button !== 0 || e.pointerType === "touch") return;
  const el = e.currentTarget;
  const list = el.closest<HTMLElement>("[data-space-list]");
  if (!list) return;
  const pid = e.pointerId;
  const sx = e.clientX;
  const sy = e.clientY;
  let on = false;
  const move = (ev: PointerEvent) => {
    if (ev.pointerId !== pid) return;
    if (!on) {
      if (Math.abs(ev.clientX - sx) + Math.abs(ev.clientY - sy) < 5) return; // still a click
      on = true;
      try {
        el.setPointerCapture(pid);
      } catch {}
      el.style.zIndex = "10";
      el.style.pointerEvents = "none";
    }
    el.style.transform = axis === "y" ? `translateY(${ev.clientY - sy}px)` : `translateX(${ev.clientX - sx}px)`;
    const others = [...list.querySelectorAll<HTMLElement>("[data-space-id]")].filter((x) => x !== el);
    const rects = others.map((x) => x.getBoundingClientRect());
    const p = axis === "y" ? ev.clientY : ev.clientX;
    let to = rects.findIndex((r) => p < (axis === "y" ? r.top + r.height / 2 : r.left + r.width / 2));
    if (to < 0) to = others.length;
    // The gap's middle, relative to the list (both scroll together, so the rects' difference holds)
    const lr = list.getBoundingClientRect();
    const a = rects[to - 1];
    const b = rects[to];
    const end = (r: DOMRect) => (axis === "y" ? r.bottom : r.right);
    const start = (r: DOMRect) => (axis === "y" ? r.top : r.left);
    const at = a && b ? (end(a) + start(b)) / 2 : a ? end(a) + 3 : b ? start(b) - 3 : 0;
    setState({ list: list.dataset.spaceList ?? "", id, to, line: at - (axis === "y" ? lr.top : lr.left) });
  };
  const finish = (drop: boolean) => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", up);
    window.removeEventListener("pointercancel", cancel);
    window.removeEventListener("keydown", key, true);
    el.style.transform = el.style.zIndex = el.style.pointerEvents = "";
    if (on) {
      // The click the release would make is not a click on the chip
      const eat = (ev: MouseEvent) => (ev.stopPropagation(), ev.preventDefault());
      window.addEventListener("click", eat, { capture: true, once: true });
      setTimeout(() => window.removeEventListener("click", eat, true), 0);
      if (drop && cur) onDrop(id, cur.to);
    }
    setState(null);
  };
  const up = (ev: PointerEvent) => ev.pointerId === pid && finish(true);
  const cancel = (ev: PointerEvent) => ev.pointerId === pid && finish(false);
  const key = (ev: KeyboardEvent) => {
    if (ev.key !== "Escape" || !on) return;
    ev.preventDefault();
    ev.stopPropagation();
    finish(false);
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", up);
  window.addEventListener("pointercancel", cancel);
  window.addEventListener("keydown", key, true);
}

/** The line marking where the dragged chip will land, drawn inside its (relative) list. */
export function SpaceDropLine({ list, axis }: { list: string; axis: "x" | "y" }) {
  const d = useSpaceDrag(list);
  if (!d) return null;
  return axis === "y" ? (
    <div className="pointer-events-none absolute z-20 left-1/2 h-0.5 w-7 -translate-x-1/2 rounded bg-brand" style={{ top: d.line - 1 }} />
  ) : (
    <div className="pointer-events-none absolute z-20 top-0.5 bottom-0.5 w-0.5 rounded bg-brand" style={{ left: d.line - 1 }} />
  );
}
