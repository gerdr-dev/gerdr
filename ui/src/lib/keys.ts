// 快捷键：herdr / tmux 那套「前缀键 + 一个键」。前缀默认 ctrl+b。
//
// 认键用 KeyboardEvent.code（物理键位）而不是 key：中文输入法开着时，按下的字母先进输入法，
// key 是 "Process"、isComposing 为真，按 key 认就是「prefix+v 没反应」。code 不受输入法影响
// （herdr 为同一个问题专门做了「进前缀时切到英文键盘」，浏览器里用 code 就绕开了）。

export type KeyAction =
  | "splitRight"
  | "splitDown"
  | "duplicateRight"
  | "duplicateDown"
  | "splitTerminal"
  | "splitTerminalDown"
  | "terminalTab"
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
  | "reopenClosed"
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
  | "newSpace"
  | "nextSpace"
  | "prevSpace"
  | "renameSpace"
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
  splitRight: "左右分屏（空面板）",
  splitDown: "上下分屏（空面板）",
  duplicateRight: "复制到右边（会话 / 终端）",
  duplicateDown: "复制到下面（会话 / 终端）",
  splitTerminal: "右边开终端",
  splitTerminalDown: "下面开终端",
  terminalTab: "新 tab 开终端",
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
  reopenClosed: "重新打开刚关的 tab / pane",
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
  newSpace: "新 session",
  nextSpace: "下一个 session",
  prevSpace: "上一个 session",
  renameSpace: "重命名 session",
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
// ⌘ 开头的是 mac 上的习惯键（⌘D 分屏、⌘W 关 pane……）：原来只写在桌面 App 的菜单里，设置里看不到也改不了（用户问「怎么没有 ⌘D」）。
// 现在都在这张表里：网页先收到按键、自己处理掉，菜单上的键由网页同步过去（syncMenuKeys），改了这里菜单跟着变。
// 浏览器里 ⌘T / ⌘W / ⌘N / ⌘1–9 这些被浏览器自己占着，按了是浏览器的功能，只在 App 里管用。
export const DEFAULT_KEYS: Record<KeyAction, string[]> = {
  // prefix+v / prefix+- 出空面板（挑 agent、目录、开终端、接旧会话）；⌘D 照 iTerm 的习惯直接复制一个能用的：
  // 焦点是会话就起同 agent / 目录 / 模式的新会话，是终端就开同目录终端，是空面板才出空面板
  splitRight: ["prefix+v"],
  splitDown: ["prefix+-"],
  duplicateRight: ["meta+d"],
  duplicateDown: ["meta+shift+d"],
  // prefix+t 在 tmux 里是大钟，没人用；herdr 也没占
  splitTerminal: ["prefix+t"],
  splitTerminalDown: ["prefix+shift+-"],
  terminalTab: ["prefix+shift+c"],
  closePane: ["prefix+x", "meta+w"],
  zoom: ["prefix+z", "meta+shift+enter"],
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
  newTab: ["prefix+c", "meta+t"],
  nextTab: ["prefix+n", "meta+shift+]"],
  prevTab: ["prefix+p", "meta+shift+["],
  lastTab: ["prefix+;"],
  closeTab: ["prefix+shift+x"],
  // 和浏览器一样。浏览器里这个键被浏览器自己占着（重开它的标签页），只在桌面 App 里管用；那边用命令面板
  reopenClosed: ["meta+shift+t"],
  renameTab: ["prefix+shift+t"],
  tab1: ["prefix+1", "meta+1"],
  tab2: ["prefix+2", "meta+2"],
  tab3: ["prefix+3", "meta+3"],
  tab4: ["prefix+4", "meta+4"],
  tab5: ["prefix+5", "meta+5"],
  tab6: ["prefix+6", "meta+6"],
  tab7: ["prefix+7", "meta+7"],
  tab8: ["prefix+8", "meta+8"],
  tab9: ["prefix+9", "meta+9"],
  newWorkspace: ["prefix+shift+n", "meta+shift+n"],
  renameWorkspace: ["prefix+shift+w"],
  closeWorkspace: ["prefix+shift+d"],
  workspaces: ["prefix+w"],
  nextWorkspace: ["prefix+)"],
  prevWorkspace: ["prefix+("],
  newSpace: [],
  nextSpace: [],
  prevSpace: [],
  renameSpace: [],
  sessions: ["prefix+g", "meta+o"],
  toggleSidebar: ["prefix+b", "meta+ctrl+s"],
  help: ["prefix+?", "meta+/"],
  settings: ["prefix+s", "meta+,"],
  importHerdr: [],
  palette: ["prefix+:", "meta+k", "ctrl+shift+p"],
  nextAttention: ["prefix+d"],
  prevAttention: ["prefix+u"],
  focusAttention: ["prefix+o", "meta+j"],
  renamePane: ["prefix+shift+p"],
  stop: [],
};

/** 设置页和帮助里的分组（tab1 代表 tab1–9）。 */
export const KEY_GROUPS: [string, KeyAction[]][] = [
  [
    "分屏",
    [
      "splitRight",
      "splitDown",
      "splitTerminal",
      "splitTerminalDown",
      "closePane",
      "zoom",
      "focusLeft",
      "focusDown",
      "focusUp",
      "focusRight",
      "swapLeft",
      "swapDown",
      "swapUp",
      "swapRight",
      "nextPane",
      "prevPane",
    ],
  ],
  ["tab", ["newTab", "terminalTab", "nextTab", "prevTab", "lastTab", "tab1", "closeTab", "reopenClosed", "renameTab"]],
  ["工作空间", ["newWorkspace", "workspaces", "nextWorkspace", "prevWorkspace", "renameWorkspace", "closeWorkspace"]],
  ["session", ["newSpace", "nextSpace", "prevSpace", "renameSpace"]],
  ["会话", ["sessions", "nextAttention", "prevAttention", "focusAttention", "renamePane", "stop"]],
  ["其他", ["palette", "toggleSidebar", "settings", "help", "importHerdr"]],
];

/** 按键事件 → 设置里存的写法（"meta+shift+d"）。前缀另算，这里不管。 */
export function comboOf(e: KeyboardEvent): string {
  const parts: string[] = [];
  if (e.ctrlKey) parts.push("ctrl");
  if (e.altKey) parts.push("alt");
  if (e.shiftKey) parts.push("shift");
  if (e.metaKey) parts.push("meta");
  parts.push(eventKey(e).key);
  return parts.join("+");
}

/**
 * 写法 → Electron 菜单的 accelerator（"meta+shift+d" → "Cmd+Shift+D"）。带前缀的、没有 ⌘ 的不上菜单：
 * 菜单只认一次按下的组合；不带 ⌘ 的（ctrl+shift+p）放上去会在输入框里抢键。
 */
export function toAccelerator(s: string): string | undefined {
  const c = parseCombo(s);
  if (!c || c.prefix || !c.meta) return;
  const names: Record<string, string> = {
    left: "Left",
    right: "Right",
    up: "Up",
    down: "Down",
    enter: "Enter",
    tab: "Tab",
    escape: "Esc",
    " ": "Space",
    "+": "Plus",
    backspace: "Backspace",
  };
  const mods = ["Cmd", c.ctrl && "Ctrl", c.alt && "Alt", c.shift && "Shift"].filter(Boolean);
  return [...mods, names[c.key] ?? c.key.toUpperCase()].join("+");
}

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
