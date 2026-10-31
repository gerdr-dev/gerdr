// 实验室：选中即复制（config.copyOnSelect）。照 herdr 的 ui.copy_on_select 做，用过 herdr 的人手感一样：
//   - 拖着选，松开鼠标就复制，选区马上清掉（清掉 = 告诉你「已经拿走了」，也免得下一次点击被当成在选区上拖）；
//   - 双击选词 / 三击选行，复制后高亮留 500ms 再清，让人看清复制的是哪一段；
//   - 底部提示「已复制」2 秒；
//   - 复制完光标回到这个 pane 的输入框（终端本来就在终端里）。
// 对话等普通页面看 DOM 选区（document 上的 mouseup）；终端是 xterm 自己画的选区，不进 DOM，Terminal.tsx 里单独接。
// 输入框 / 可编辑区域里的选区不管：在那里选字是为了改、为了删，复制走了还清掉选区就没法改了。
import { toast } from "sonner";
import { isCoarse } from "./phone";
import { get, prefOf } from "./store";

const HIGHLIGHT_MS = 500; // herdr 的 PANE_COPY_HIGHLIGHT_DURATION
const TOAST_MS = 2000; // herdr 的 COPY_FEEDBACK_DURATION

export const copyOnSelectOn = () => !!prefOf(get(), "copyOnSelect");

// 还没到点的「清选区」：三击时第二下（双击）复制完会定一个，第三下又定一个。新的一次复制要先把旧的撤掉 —— 不然旧的提前
// 跑，整行的高亮刚出来就被清掉、焦点进了输入框；接着第三下的再清一次，清掉的就是输入框里的光标了（还聚焦着却没光标，
// 打不了字、输入法也起不来；用户报的）
let pendingClear: ReturnType<typeof setTimeout> | undefined;

/** 复制并提示；clear 是清掉选区的办法，多击时晚一点再清。 */
export function copySelected(text: string, multiClick: boolean, clear: () => void) {
  if (!text) return; // herdr：空内容不写剪贴板、不提示
  clearTimeout(pendingClear);
  const done = () => {
    toast("已复制", { id: "copy-on-select", duration: TOAST_MS });
    clearTimeout(pendingClear);
    if (multiClick) pendingClear = setTimeout(clear, HIGHLIGHT_MS);
    else clear();
  };
  // clipboard API 要页面聚焦、安全上下文（https / localhost）；不行就退回 execCommand（它复制的是当前 DOM 选区，
  // 所以退回时不能先清选区）
  const fallback = () => {
    if (document.execCommand("copy")) done();
  };
  if (navigator.clipboard) navigator.clipboard.writeText(text).then(done, fallback);
  else fallback();
}

function editable(n: Node | null): boolean {
  const el = n instanceof Element ? n : n?.parentElement;
  return !!el?.closest("input, textarea, select, [contenteditable]:not([contenteditable=false])");
}

/** 挂在 document 上，管 DOM 里的选区（终端除外）。返回卸载函数。 */
export function watchCopyOnSelect(): () => void {
  let seq = 0;
  const onUp = (e: MouseEvent) => {
    if (e.button !== 0 || !copyOnSelectOn()) return;
    if ((e.target as Element | null)?.closest?.(".xterm")) return; // 终端自己管
    // 等浏览器把这次点击的选区定下来（mouseup 当下双击的选区有时还没落）。只认最后一次松手：双击点得快时，第一下的检查
    // 还没跑第二下就把词选上了，按第一下（单击）算会当成拖选马上清掉
    const my = ++seq;
    setTimeout(() => {
      if (my !== seq) return;
      const sel = window.getSelection();
      if (!sel || sel.isCollapsed || !sel.rangeCount) return;
      // 输入框里的选区不进 getSelection 的节点，看焦点（点对话里的字时焦点会离开输入框）
      if (editable(sel.anchorNode) || editable(sel.focusNode) || editable(document.activeElement)) return;
      // 复制完把光标放回这个 pane 的输入框，接着就能粘贴 / 打字（用户要的）。清选区那一刻再聚焦：聚焦输入框会顶掉页面选区，
      // 早了双击的高亮就看不到了。不在 pane 里（设置弹窗之类）、没有输入框的 pane（子 agent）不动焦点；触屏上不聚焦（会弹键盘）
      const pane = (sel.anchorNode instanceof Element ? sel.anchorNode : sel.anchorNode?.parentElement)?.closest("[data-pane-id]");
      const box = pane?.querySelector<HTMLTextAreaElement>("textarea[data-composer]");
      // 三击选段时浏览器把段尾的换行也选上了，粘进输入框会多出空行
      const text = e.detail >= 3 ? sel.toString().replace(/\s+$/, "") : sel.toString();
      copySelected(text, e.detail >= 2, () => {
        // 焦点已经在输入框里（用户等不及自己点进去了）时别清：这时的页面选区就是输入框的光标
        if (editable(document.activeElement)) return;
        sel.removeAllRanges();
        if (box?.isConnected && !isCoarse() && !document.querySelector("[role=dialog]")) box.focus();
      });
    });
  };
  document.addEventListener("mouseup", onUp);
  return () => document.removeEventListener("mouseup", onUp);
}
