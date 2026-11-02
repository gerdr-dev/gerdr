import { toast } from "sonner";
import { api, ApiError } from "../api";
import { stripLineAnchor } from "../paths";
import { isPhone } from "../phone";
import { activeTab, leaves, rid, setPane, split, updateTab, type PaneLeaf } from "../layout";
import { set, setLayout } from "../store";

// ---- 文件 ----

let openSeq = 0;

/**
 * Open a path clicked in the chat. Asks as written first and only then without a line anchor: agents like to write
 * `a.md#L21` / `main.go:42`, which aren't part of the name, but `notes#1.md` is a valid file name, so it can't be
 * stripped up front. When it's slow (a network drive) and something else was clicked meanwhile, the late answer is
 * dropped instead of a viewer popping up out of nowhere.
 * beside: in a pane next to the focused one (⌥-click) instead of the viewer dialog; a phone shows one pane at a time,
 * so there it is the dialog anyway.
 */
export async function openPath(raw: string, cwd?: string, opts: { beside?: boolean } = {}) {
  const seq = ++openSeq;
  const beside = opts.beside && !isPhone();
  (document.activeElement as HTMLElement | null)?.blur?.(); // 手机上收键盘，不然查看器只露半截
  const tries = [raw];
  const bare = stripLineAnchor(raw);
  if (bare && bare !== raw) tries.push(bare);
  let err: unknown;
  for (const p of tries) {
    try {
      const stat = await api.fileStat(p, cwd);
      if (seq !== openSeq) return;
      if (stat.dir) {
        toast(`这是个目录：${stat.path}`);
        return;
      }
      if (beside) openFilePane(stat.path);
      else set({ dialog: { kind: "file", stat } });
      return;
    } catch (e) {
      // 写短了的路径对上好几个文件（服务端按后缀找的）：让人挑一个
      if (e instanceof ApiError && e.candidates?.length) {
        if (seq === openSeq) set({ dialog: { kind: "pickFile", path: p, candidates: e.candidates, beside } });
        return;
      }
      err = e;
    }
  }
  if (seq === openSeq) toast.error((err as Error)?.message ?? "打不开这个路径");
}

/**
 * Show a file in a pane beside the focused one. A tab that already has a file pane reuses it (like an editor's side
 * group): ⌥-clicking down a list of paths swaps what that pane shows instead of splitting the chat ever thinner.
 * The file pane takes focus: the user asked to look at it, so that is where the highlight (and keys) should go.
 */
function openFilePane(path: string) {
  setLayout((l) => {
    const t = activeTab(l);
    const ls = leaves(t.root);
    const same = ls.find((p) => p.file?.path === path);
    if (same) return updateTab(l, t.id, (tab) => ({ ...tab, zoom: undefined, focus: same.id }));
    const old = ls.find((p) => p.file);
    if (old) return updateTab(setPane(l, old.id, { file: { path } }), t.id, (tab) => ({ ...tab, zoom: undefined, focus: old.id }));
    const fresh: PaneLeaf = { type: "pane", id: rid("p"), file: { path } };
    return updateTab(l, t.id, (tab) => ({ ...split(tab, tab.zoom ?? tab.focus, "row", fresh), focus: fresh.id }));
  });
}
