// 急救面板：主界面（界面目录，人和 agent 都能改）改坏了的时候，在页面右侧拉出来，叫 agent 来修、或者先退回之前的一版。
//
// 为什么这样挂：
// - 和主界面在同一页、并排：一边看主界面坏成什么样 / 修好没有，一边和 agent 说话。
// - 但和主界面一点都不共享：这个脚本由 uisite 生成的页面外壳直接 <script> 引入（不经过界面目录的编译），
//   自带打进来的 React（不用 importmap / esm.sh），挂在 body 下自己的节点、Shadow DOM 里（主界面的样式进不来，这里的也出不去）。
//   主界面的 React 整个崩了、编译不过、包拉不下来，这一块照样能用。
// - 开没开记在 localStorage：回退版本、agent 改完要刷新页面，刷新后面板还在。
//
// 打开：App 菜单「帮助 → 急救」、主界面报错页上的链接（/rescue 会跳到 /?rescue=1）、页面报错时右下角冒出来的「急救」。
import { createRoot } from "react-dom/client";
import { Panel } from "./Panel";
import { CSS } from "./style";

const OPEN_KEY = "gerdr.rescue.open";
const WIDTH_KEY = "gerdr.rescue.width";

const store = {
  get(k: string) {
    try {
      return localStorage.getItem(k);
    } catch {
      return null;
    }
  },
  set(k: string, v: string | null) {
    try {
      v === null ? localStorage.removeItem(k) : localStorage.setItem(k, v);
    } catch {}
  },
};

function mount() {
  if (document.getElementById("__gerdr_rescue")) return;
  const host = document.createElement("div");
  host.id = "__gerdr_rescue";
  document.body.appendChild(host);
  // 键盘事件别冒泡出去：主界面在 window 上听 keydown，看 document.activeElement 不是输入框就把焦点抢到它的输入框
  // （「在哪打字都进对话框」）。Shadow DOM 里的 textarea 从外面看 activeElement 只是这个 div，字就全跑到左边去了（踩过）。
  // 只拦冒泡：主界面在捕获阶段的快捷键（前缀键那些）照常能用。
  for (const t of ["keydown", "keyup", "keypress"]) host.addEventListener(t, (e) => e.stopPropagation());
  const shadow = host.attachShadow({ mode: "open" });
  const style = document.createElement("style");
  style.textContent = CSS;
  shadow.appendChild(style);
  const box = document.createElement("div");
  shadow.appendChild(box);

  // 给主界面让出右边那一块：padding 加在 html 上（主界面的 html / body / #root 都是 100% 高宽，跟着变窄）。
  // 这条样式放在页面里、不在 Shadow DOM 里，因为要作用到主界面。手机上面板盖满全屏，不挤。
  const squeeze = document.createElement("style");
  document.head.appendChild(squeeze);

  let open = store.get(OPEN_KEY) === "1";
  let width = Math.max(340, Number(store.get(WIDTH_KEY)) || 460);
  const q = new URLSearchParams(location.search);
  if (q.has("rescue")) {
    open = true;
    q.delete("rescue");
    const s = q.toString();
    history.replaceState(history.state, "", location.pathname + (s ? "?" + s : "") + location.hash);
  }

  const root = createRoot(box);
  const render = () => {
    store.set(OPEN_KEY, open ? "1" : null);
    // The page shell's own full-screen layers (build-error overlay, loading screen) sit above the panel in z-order:
    // on wide screens they move out of its way; on phones the panel fills the screen, so hide them (the panel shows
    // the same error itself), otherwise they cover it completely.
    squeeze.textContent = open
      ? `@media (min-width: 768px) { html { padding-right: ${width}px !important; } #__gerdr_overlay, #__gerdr_loading { right: ${width}px !important; } }` +
        `@media (max-width: 767px) { #__gerdr_overlay, #__gerdr_loading { display: none !important; } }`
      : "";
    root.render(<Panel open={open} width={width} onOpen={setOpen} onWidth={setWidth} />);
  };
  const setOpen = (v: boolean) => {
    open = v;
    render();
  };
  const setWidth = (w: number, done?: boolean) => {
    width = Math.round(Math.min(Math.max(340, w), window.innerWidth * 0.7));
    if (done) store.set(WIDTH_KEY, String(width));
    render();
  };
  (window as any).__gerdrRescue = {
    open: () => setOpen(true),
    close: () => setOpen(false),
    toggle: () => setOpen(!open),
  };
  render();
}

if (document.body) mount();
else document.addEventListener("DOMContentLoaded", mount);
