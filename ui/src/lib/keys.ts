// 快捷键：herdr / tmux 那套「前缀键 + 一个键」。前缀默认 ctrl+b。
//
// 认键用 KeyboardEvent.code（物理键位）而不是 key：中文输入法开着时，按下的字母先进输入法，
// key 是 "Process"、isComposing 为真，按 key 认就是「prefix+v 没反应」。code 不受输入法影响
// （herdr 为同一个问题专门做了「进前缀时切到英文键盘」，浏览器里用 code 就绕开了）。

export type KeyAction =
  | "splitRight"
  | "splitDown"
  | "closePane"
  | "zoom"
  | "focusLeft"
  | "focusDown"
  | "focusUp"
  | "focusRight"
  | "swapLeft"
  | "swapDown"
  | "swapUp"
  | "swapRight"
  | "nextPane"
  | "prevPane"
  | "newTab"
  | "nextTab"
  | "prevTab"
  | "lastTab"
  | "closeTab"
  | "renameTab"
  | "tab1"
  | "tab2"
  | "tab3"
  | "tab4"
  | "tab5"
  | "tab6"
  | "tab7"
  | "tab8"
  | "tab9"
  | "newWorkspace"
  | "renameWorkspace"
  | "closeWorkspace"
  | "workspaces"
  | "nextWorkspace"
  | "prevWorkspace"
  | "sessions"
  | "toggleSidebar"
  | "help"
  | "settings"
  | "importHerdr"
  | "palette"
  | "nextAttention"
  | "prevAttention"
  | "focusAttention"
  | "renamePane"
  | "stop";

export const ACTION_TITLES: Record<KeyAction, string> = {
  splitRight: "左右分屏",
  splitDown: "上下分屏",
  closePane: "关闭 pane",
  zoom: "放大 / 还原 pane",
  focusLeft: "焦点 ←",
  focusDown: "焦点 ↓",
  focusUp: "焦点 ↑",
  focusRight: "焦点 →",
  swapLeft: "和左边交换",
  swapDown: "和下面交换",
  swapUp: "和上面交换",
  swapRight: "和右边交换",
  nextPane: "下一个 pane",
  prevPane: "上一个 pane",
  newTab: "新建 tab",
  nextTab: "下一个 tab",
  prevTab: "上一个 tab",
  lastTab: "上次的 tab",
  closeTab: "关闭 tab",
  renameTab: "重命名 tab",
  tab1: "tab 1",
  tab2: "tab 2",
  tab3: "tab 3",
  tab4: "tab 4",
  tab5: "tab 5",
  tab6: "tab 6",
  tab7: "tab 7",
  tab8: "tab 8",
  tab9: "tab 9",
  newWorkspace: "新建工作空间",
  renameWorkspace: "重命名工作空间",
  closeWorkspace: "关闭工作空间",
  workspaces: "切换工作空间",
  nextWorkspace: "下一个工作空间",
  prevWorkspace: "上一个工作空间",
  sessions: "会话列表（接着以前的聊）",
  toggleSidebar: "显示 / 隐藏侧栏",
  help: "快捷键帮助",
  settings: "设置",
  importHerdr: "从 herdr 导入（工作空间、tab、会话）",
  palette: "命令面板",
  nextAttention: "下一个要你处理的会话",
  prevAttention: "上一个要你处理的会话",
  focusAttention: "跳到在等你的会话",
  renamePane: "重命名会话",
  stop: "打断当前会话",
};

// 默认键位照着 herdr（src/config/model.rs 里那张表），再加上 gerdr 自己的几个。
export const DEFAULT_KEYS: Record<KeyAction, string[]> = {
  splitRight: ["prefix+v"],
  splitDown: ["prefix+-"],
  closePane: ["prefix+x"],
  zoom: ["prefix+z"],
  focusLeft: ["prefix+h", "prefix+left"],
  focusDown: ["prefix+j", "prefix+down"],
  focusUp: ["prefix+k", "prefix+up"],
  focusRight: ["prefix+l", "prefix+right"],
  swapLeft: ["prefix+shift+h"],
  swapDown: ["prefix+shift+j"],
  swapUp: ["prefix+shift+k"],
  swapRight: ["prefix+shift+l"],
  nextPane: ["prefix+tab"],
  prevPane: ["prefix+shift+tab"],
  newTab: ["prefix+c"],
  nextTab: ["prefix+n"],
  prevTab: ["prefix+p"],
  lastTab: ["prefix+;"],
  closeTab: ["prefix+shift+x"],
  renameTab: ["prefix+shift+t"],
  tab1: ["prefix+1"],
  tab2: ["prefix+2"],
  tab3: ["prefix+3"],
  tab4: ["prefix+4"],
  tab5: ["prefix+5"],
  tab6: ["prefix+6"],
  tab7: ["prefix+7"],
  tab8: ["prefix+8"],
  tab9: ["prefix+9"],
  newWorkspace: ["prefix+shift+n"],
  renameWorkspace: ["prefix+shift+w"],
  closeWorkspace: ["prefix+shift+d"],
  workspaces: ["prefix+w"],
  nextWorkspace: ["prefix+)"],
  prevWorkspace: ["prefix+("],
  sessions: ["prefix+g"],
  toggleSidebar: ["prefix+b"],
  help: ["prefix+?"],
  settings: ["prefix+s"],
  importHerdr: [],
  palette: ["prefix+:", "meta+k", "ctrl+shift+p"],
  nextAttention: ["prefix+d"],
  prevAttention: ["prefix+u"],
  focusAttention: ["prefix+o"],
  renamePane: ["prefix+shift+p"],
  stop: [],
};

export type Combo = { prefix: boolean; ctrl: boolean; alt: boolean; shift: boolean; meta: boolean; key: string };

const ALIASES: Record<string, string> = {
  minus: "-",
  dash: "-",
  plus: "+",
  space: " ",
  esc: "escape",
  return: "enter",
  cmd: "meta",
  command: "meta",
  option: "alt",
  opt: "alt",
  "←": "left",
  "→": "right",
  "↑": "up",
  "↓": "down",
  semicolon: ";",
  question: "?",
  colon: ":",
};

export function parseCombo(s: string): Combo | null {
  const parts = s
    .toLowerCase()
    .split("+")
    .map((x) => x.trim());
  // "prefix++"：最后一段是 "+"
  if (s.endsWith("++")) {
    parts.splice(parts.length - 2, 2, "+");
  }
  const c: Combo = { prefix: false, ctrl: false, alt: false, shift: false, meta: false, key: "" };
  for (const raw of parts) {
    const p = ALIASES[raw] ?? raw;
    if (p === "prefix") c.prefix = true;
    else if (p === "ctrl" || p === "control") c.ctrl = true;
    else if (p === "alt") c.alt = true;
    else if (p === "shift") c.shift = true;
    else if (p === "meta") c.meta = true;
    else c.key = p;
  }
  return c.key ? c : null;
}

// code → 不受布局 / 输入法影响的键名（shift 时给出符号）。
const CODE_KEYS: Record<string, [string, string]> = {
  Minus: ["-", "_"],
  Equal: ["=", "+"],
  BracketLeft: ["[", "{"],
  BracketRight: ["]", "}"],
  Backslash: ["\\", "|"],
  Semicolon: [";", ":"],
  Quote: ["'", '"'],
  Comma: [",", "<"],
  Period: [".", ">"],
  Slash: ["/", "?"],
  Backquote: ["`", "~"],
  Digit1: ["1", "!"],
  Digit2: ["2", "@"],
  Digit3: ["3", "#"],
  Digit4: ["4", "$"],
  Digit5: ["5", "%"],
  Digit6: ["6", "^"],
  Digit7: ["7", "&"],
  Digit8: ["8", "*"],
  Digit9: ["9", "("],
  Digit0: ["0", ")"],
  ArrowLeft: ["left", "left"],
  ArrowRight: ["right", "right"],
  ArrowUp: ["up", "up"],
  ArrowDown: ["down", "down"],
  Tab: ["tab", "tab"],
  Space: [" ", " "],
  Enter: ["enter", "enter"],
  Escape: ["escape", "escape"],
  Backspace: ["backspace", "backspace"],
};

/** 一个按键事件对应的键名（字母一律小写，shift 另记）。 */
export function eventKey(e: KeyboardEvent): { key: string; sym: string } {
  if (e.code.startsWith("Key")) {
    const k = e.code.slice(3).toLowerCase();
    return { key: k, sym: k };
  }
  const m = CODE_KEYS[e.code];
  if (m) return { key: m[0], sym: e.shiftKey ? m[1] : m[0] };
  const k = e.key.toLowerCase();
  return { key: k, sym: k };
}

/** combo 匹配不匹配这个事件（不看 prefix 那一位）。符号键两种写法都认："prefix+?" 和 "prefix+shift+/"。 */
export function matches(c: Combo, e: KeyboardEvent): boolean {
  const { key, sym } = eventKey(e);
  if (c.ctrl !== e.ctrlKey || c.alt !== e.altKey || c.meta !== e.metaKey) return false;
  if (c.key === key && c.shift === e.shiftKey) return true;
  // 写成符号的（"?"、"("、":"）：只要按出来的就是这个符号，shift 不另算。
  if (!c.shift && sym === c.key && sym !== key) return true;
  return false;
}

export function isModifierOnly(e: KeyboardEvent) {
  return ["Shift", "Control", "Alt", "Meta", "CapsLock", "Fn"].includes(e.key);
}

let prefixLabel = "⌃B";
/** 前缀键在界面上怎么显示（配置变了由 App 刷新）。 */
export function setPrefixLabel(prefix: string) {
  prefixLabel = formatCombo(prefix);
}

export function formatCombo(s: string): string {
  const c = parseCombo(s);
  if (!c) return s;
  const mac = typeof navigator !== "undefined" && /Mac/.test(navigator.platform);
  const parts: string[] = [];
  if (c.prefix) parts.push(prefixLabel);
  const mods: string[] = [];
  if (c.ctrl) mods.push(mac ? "⌃" : "Ctrl+");
  if (c.alt) mods.push(mac ? "⌥" : "Alt+");
  if (c.shift) mods.push(mac ? "⇧" : "Shift+");
  if (c.meta) mods.push(mac ? "⌘" : "Win+");
  const names: Record<string, string> = { left: "←", right: "→", up: "↑", down: "↓", tab: "Tab", enter: "↵", escape: "Esc", " ": "Space" };
  parts.push(mods.join("") + (names[c.key] ?? c.key.toUpperCase()));
  return parts.join(" ");
}
