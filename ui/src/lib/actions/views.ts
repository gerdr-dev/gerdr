// The view on screen: Code (the panes), Work (the project's site + its agent) or the overview (the grid of agents).
// Work and the overview cover the Code layout, so anything aimed at "the focused pane" has to ask the view instead:
// which conversation is in front of the user, which composer is theirs. Each view's keys are in runAction.ts.
import { currentSpace } from "../layout";
import { overviewFocus, toggleOverview } from "../overview";
import { get, overviewShown, setMode } from "../store";
import { workSessionOf } from "../work";
import { focusedLeaf } from "./shared";

export type ViewName = "code" | "work" | "overview";

export type View = {
  name: ViewName;
  /** The conversation the session keys (stop, rename, next-needs-you…) act on. */
  session(): string | undefined;
  /** The pane id of the composer on screen (its textarea's data-composer), for ⌘S. */
  composer(): string | undefined;
  /** Step aside so the Code layout shows (nothing to do in Code itself). */
  leave(): void;
};

const code: View = {
  name: "code",
  session: () => focusedLeaf(get().layout)?.sessionId,
  composer: () => {
    const leaf = focusedLeaf(get().layout);
    return leaf?.sessionId ? leaf.id : undefined;
  },
  leave: () => {},
};

// WorkView.tsx's pane ids: the conversation the page shows over itself (showSession), else the Work agent's chat
const work: View = {
  name: "work",
  session: () => get().workPeek ?? workSessionOf(currentSpace(get().layout)),
  composer: () => {
    const { workPeek, layout } = get();
    return workPeek ? `work-peek:${workPeek}` : `work:${currentSpace(layout)}`;
  },
  leave: () => setMode("agents"),
};

const overview: View = {
  name: "overview",
  session: () => overviewFocus(get()),
  composer: () => {
    const f = overviewFocus(get());
    return f ? `ov-${f}` : undefined;
  },
  leave: () => toggleOverview(false),
};

export function currentView(): View {
  if (overviewShown()) return overview;
  return get().mode === "work" ? work : code;
}

/**
 * Going to a pane (focusing it, opening a tab) means seeing it: in Work it would land hidden under the site. Called
 * after the layout change, so a jump to another project records the mode of the project landed in, not this one.
 */
export function showCode() {
  if (get().mode === "work") setMode("agents");
}
