// 快捷键：herdr / tmux 那套「前缀键 + 一个键」。前缀默认 ctrl+b。
import { t } from "./i18n";
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
  | "navBack"
  | "navForward"
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
  | "active1"
  | "active2"
  | "active3"
  | "active4"
  | "active5"
  | "active6"
  | "active7"
  | "active8"
  | "active9"
  | "newWorkspace"
  | "renameWorkspace"
  | "closeWorkspace"
  | "workspaces"
  | "nextWorkspace"
  | "prevWorkspace"
  | "ws1"
  | "ws2"
  | "ws3"
  | "ws4"
  | "ws5"
  | "ws6"
  | "ws7"
  | "ws8"
  | "ws9"
  | "newSpace"
  | "nextSpace"
  | "prevSpace"
  | "sessions"
  | "toggleSidebar"
  | "overview"
  | "help"
  | "settings"
  | "importHerdr"
  | "palette"
  | "nextAttention"
  | "prevAttention"
  | "focusAttention"
  | "renamePane"
  | "stop"
  | "stash";

// Built at load: the language is fixed for the page (lib/i18n.ts)
export const ACTION_TITLES: Record<KeyAction, string> = {
  splitRight: t("keys.splitRight"),
  splitDown: t("keys.splitDown"),
  duplicateRight: t("keys.duplicateRight"),
  duplicateDown: t("keys.duplicateDown"),
  splitTerminal: t("keys.splitTerminal"),
  splitTerminalDown: t("keys.splitTerminalDown"),
  terminalTab: t("keys.terminalTab"),
  closePane: t("keys.closePane"),
  zoom: t("keys.zoom"),
  focusLeft: t("keys.focusLeft"),
  focusDown: t("keys.focusDown"),
  focusUp: t("keys.focusUp"),
  focusRight: t("keys.focusRight"),
  swapLeft: t("keys.swapLeft"),
  swapDown: t("keys.swapDown"),
  swapUp: t("keys.swapUp"),
  swapRight: t("keys.swapRight"),
  nextPane: t("keys.nextPane"),
  prevPane: t("keys.prevPane"),
  newTab: t("keys.newTab"),
  nextTab: t("keys.nextTab"),
  prevTab: t("keys.prevTab"),
  lastTab: t("keys.lastTab"),
  navBack: t("keys.navBack"),
  navForward: t("keys.navForward"),
  closeTab: t("keys.closeTab"),
  reopenClosed: t("keys.reopenClosed"),
  renameTab: t("keys.renameTab"),
  tab1: t("keys.tabN", { n: 1 }),
  tab2: t("keys.tabN", { n: 2 }),
  tab3: t("keys.tabN", { n: 3 }),
  tab4: t("keys.tabN", { n: 4 }),
  tab5: t("keys.tabN", { n: 5 }),
  tab6: t("keys.tabN", { n: 6 }),
  tab7: t("keys.tabN", { n: 7 }),
  tab8: t("keys.tabN", { n: 8 }),
  tab9: t("keys.tabN", { n: 9 }),
  active1: t("keys.activeN", { n: 1 }),
  active2: t("keys.activeN", { n: 2 }),
  active3: t("keys.activeN", { n: 3 }),
  active4: t("keys.activeN", { n: 4 }),
  active5: t("keys.activeN", { n: 5 }),
  active6: t("keys.activeN", { n: 6 }),
  active7: t("keys.activeN", { n: 7 }),
  active8: t("keys.activeN", { n: 8 }),
  active9: t("keys.activeN", { n: 9 }),
  newWorkspace: t("keys.newWorkspace"),
  renameWorkspace: t("keys.renameWorkspace"),
  closeWorkspace: t("keys.closeWorkspace"),
  workspaces: t("keys.workspaces"),
  nextWorkspace: t("keys.nextWorkspace"),
  prevWorkspace: t("keys.prevWorkspace"),
  ws1: t("keys.wsN", { n: 1 }),
  ws2: t("keys.wsN", { n: 2 }),
  ws3: t("keys.wsN", { n: 3 }),
  ws4: t("keys.wsN", { n: 4 }),
  ws5: t("keys.wsN", { n: 5 }),
  ws6: t("keys.wsN", { n: 6 }),
  ws7: t("keys.wsN", { n: 7 }),
  ws8: t("keys.wsN", { n: 8 }),
  ws9: t("keys.wsN", { n: 9 }),
  newSpace: t("keys.newSpace"),
  nextSpace: t("keys.nextSpace"),
  prevSpace: t("keys.prevSpace"),
  sessions: t("keys.sessions"),
  toggleSidebar: t("keys.toggleSidebar"),
  overview: t("keys.overview"),
  help: t("keys.help"),
  settings: t("keys.settings"),
  importHerdr: t("keys.importHerdr"),
  palette: t("keys.palette"),
  nextAttention: t("keys.nextAttention"),
  prevAttention: t("keys.prevAttention"),
  focusAttention: t("keys.focusAttention"),
  renamePane: t("keys.renamePane"),
  stop: t("keys.stop"),
  stash: t("keys.stash"),
};

// 默认键位照着 herdr（src/config/model.rs 里那张表），再加上 gerdr 自己的几个。
// ⌘ 开头的是 mac 上的习惯键（⌘D 分屏、⌘W 关 pane……）：原来只写在桌面 App 的菜单里，设置里看不到也改不了（用户问「怎么没有 ⌘D」）。
// 现在都在这张表里：网页先收到按键、自己处理掉，菜单上的键由网页同步过去（syncMenuKeys），改了这里菜单跟着变。
// 浏览器里 ⌘T / ⌘W / ⌘N / ⌘1–9 这些被浏览器自己占着，按了是浏览器的功能，只在 App 里管用。
const MAC_KEYS: Record<KeyAction, string[]> = {
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
  // Not on Windows / Linux: Ctrl+E is end-of-line in a terminal pane
  lastTab: ["prefix+;", "meta+e"],
  // The mouse's side buttons do these (actions/nav.ts). No keys by default: the usual ones (⌃- / ⌘[) are taken by
  // terminals or by prevTab / prevSpace
  navBack: [],
  navForward: [],
  closeTab: ["prefix+shift+x"],
  // 和浏览器一样。浏览器里这个键被浏览器自己占着（重开它的标签页），只在桌面 App 里管用；那边用命令面板
  reopenClosed: ["meta+shift+t"],
  renameTab: ["prefix+shift+t"],
  // ⌘1–9 used to be the tabs; the sidebar replaced the tab bar and its rows aren't numbered, so ⌘N now goes to the Nth agent
  // of "active" (the section on top, numbered while ⌘ is held: lib/modHeld.ts). The tabs keep prefix+N.
  tab1: ["prefix+1"],
  tab2: ["prefix+2"],
  tab3: ["prefix+3"],
  tab4: ["prefix+4"],
  tab5: ["prefix+5"],
  tab6: ["prefix+6"],
  tab7: ["prefix+7"],
  tab8: ["prefix+8"],
  tab9: ["prefix+9"],
  active1: ["meta+1"],
  active2: ["meta+2"],
  active3: ["meta+3"],
  active4: ["meta+4"],
  active5: ["meta+5"],
  active6: ["meta+6"],
  active7: ["meta+7"],
  active8: ["meta+8"],
  active9: ["meta+9"],
  newWorkspace: ["prefix+shift+n", "meta+shift+n"],
  renameWorkspace: ["prefix+shift+w"],
  closeWorkspace: ["prefix+shift+d"],
  workspaces: ["prefix+w"],
  nextWorkspace: ["prefix+)"],
  prevWorkspace: ["prefix+("],
  // ⌃⌘1–9: the numbers in front of the sidebar's workspaces (within the current session); ⌘1–9 are "active". Not ⇧⌘1–9 (macOS keeps
  // ⇧⌘3 / 4 / 5 for screenshots, they never reach the page) nor ⌃1–9 (desktops, when Mission Control shortcuts are on)
  ws1: ["meta+ctrl+1"],
  ws2: ["meta+ctrl+2"],
  ws3: ["meta+ctrl+3"],
  ws4: ["meta+ctrl+4"],
  ws5: ["meta+ctrl+5"],
  ws6: ["meta+ctrl+6"],
  ws7: ["meta+ctrl+7"],
  ws8: ["meta+ctrl+8"],
  ws9: ["meta+ctrl+9"],
  newSpace: [],
  // ⌃⌘] / ⌃⌘[ next to tabs' ⇧⌘] / ⇧⌘[. Not ⌘`: macOS uses that to cycle an app's windows, and now that each session can
  // have a window of its own the two would fight
  nextSpace: ["meta+ctrl+]", "prefix+]"],
  prevSpace: ["meta+ctrl+[", "prefix+["],
  sessions: ["prefix+g", "meta+o"],
  toggleSidebar: ["prefix+b", "meta+ctrl+s"],
  overview: ["prefix+a"],
  help: ["prefix+?", "meta+/"],
  settings: ["prefix+s", "meta+,"],
  importHerdr: [],
  palette: ["prefix+:", "meta+k", "ctrl+shift+p"],
  nextAttention: ["prefix+d"],
  prevAttention: ["prefix+u"],
  focusAttention: ["prefix+o", "meta+j"],
  renamePane: ["prefix+shift+p"],
  stop: [],
  stash: ["meta+s"],
};

/*
 * Windows / Linux. The mac table's ⌘ is "meta", which there is the Windows key: Windows keeps nearly every Win+ combination
 * (Win+D desktop, Win+1–9 taskbar, Win+T…) and most never reach the page, and the Windows App has no menu bar to fall back on.
 * So off the mac every ⌘ binding is dropped and these take their place (prefix bindings are shared), after Windows Terminal /
 * VS Code: Ctrl+Shift+letter, not Ctrl+letter, because Ctrl+W / D / K / T are delete-word / EOF / kill-line / transpose in
 * terminals and inputs. Not Ctrl+Alt+anything that types: AltGr is Ctrl+Alt on European layouts (German AltGr+7/8/9 = { [ ]).
 * In a browser (not the App) Chrome keeps Ctrl+Shift+T/W/N and Ctrl+Tab for itself, same as ⌘T / ⌘W on the mac.
 */
const PC_EXTRA: Partial<Record<KeyAction, string[]>> = {
  duplicateRight: ["alt+shift+d"],
  duplicateDown: ["alt+shift+-"],
  closePane: ["ctrl+shift+w"],
  zoom: ["ctrl+shift+enter"],
  newTab: ["ctrl+shift+t"],
  nextTab: ["ctrl+tab"],
  prevTab: ["ctrl+shift+tab"],
  reopenClosed: ["ctrl+shift+alt+t"],
  ...Object.fromEntries(
    [1, 2, 3, 4, 5, 6, 7, 8, 9].flatMap((i) => [
      [`active${i}`, [`ctrl+${i}`]],
      [`ws${i}`, [`alt+${i}`]],
    ]),
  ),
  newWorkspace: ["ctrl+shift+n"],
  nextSpace: ["ctrl+alt+]"],
  prevSpace: ["ctrl+alt+["],
  sessions: ["ctrl+shift+o"],
  toggleSidebar: ["ctrl+shift+b"],
  help: ["ctrl+shift+/"],
  settings: ["ctrl+,"],
  focusAttention: ["ctrl+shift+j"],
  // Not Ctrl+S: in a terminal pane that's XOFF / bash's forward history search
  stash: ["ctrl+shift+s"],
};

export const IS_MAC = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

/**
 * The defaults for this platform (custom keys in config.json are still one set shared by every device). Computed at load, so it
 * can't call parseCombo: that reads ALIASES, declared further down (temporal dead zone).
 */
export const DEFAULT_KEYS: Record<KeyAction, string[]> = IS_MAC
  ? MAC_KEYS
  : (Object.fromEntries(
      (Object.keys(MAC_KEYS) as KeyAction[]).map((a) => [a, [...MAC_KEYS[a].filter((k) => !/(^|\+)meta\+/.test(k)), ...(PC_EXTRA[a] ?? [])]]),
    ) as Record<KeyAction, string[]>);

/** Groups in Settings and the help (tab1 / active1 / ws1 stand for their 1–9). */
export const KEY_GROUPS: [string, KeyAction[]][] = [
  [
    t("keys.group.split"),
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
  [t("keys.group.tab"), ["newTab", "terminalTab", "nextTab", "prevTab", "lastTab", "navBack", "navForward", "tab1", "closeTab", "reopenClosed", "renameTab"]],
  [t("keys.group.workspace"), ["newWorkspace", "workspaces", "nextWorkspace", "prevWorkspace", "ws1", "renameWorkspace", "closeWorkspace"]],
  [t("keys.group.project"), ["newSpace", "nextSpace", "prevSpace"]],
  [t("keys.group.session"), ["active1", "sessions", "nextAttention", "prevAttention", "focusAttention", "renamePane", "stop", "stash"]],
  [t("keys.group.other"), ["palette", "overview", "toggleSidebar", "settings", "help", "importHerdr"]],
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
  // Not every "keydown" is a real KeyboardEvent: Chrome's autofill / Android's native <select> picker dispatch a plain
  // Event("keydown") with no key or code, and reading them threw on every such pick.
  if (typeof e.code !== "string" || typeof e.key !== "string") return { key: "", sym: "" };
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

/**
 * A combo as keycaps, for drawing each key on its own (KeyCaps.tsx): [["⌃", "B"], ["V"]] for prefix+v, [["⌘", "D"]] for
 * meta+d. One inner list per key press; a prefix binding is two presses. macOS order (⌃⌥⇧⌘), words off the mac.
 */
export function comboCaps(s: string, prefix: string): string[][] {
  const c = parseCombo(s);
  if (!c) return [[s]];
  const mac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);
  const names: Record<string, string> = { left: "←", right: "→", up: "↑", down: "↓", tab: "Tab", enter: "↵", escape: "Esc", " ": "Space", backspace: "⌫" };
  const caps: string[] = [];
  if (c.ctrl) caps.push(mac ? "⌃" : "Ctrl");
  if (c.alt) caps.push(mac ? "⌥" : "Alt");
  if (c.shift) caps.push(mac ? "⇧" : "Shift");
  if (c.meta) caps.push(mac ? "⌘" : "Win");
  caps.push(names[c.key] ?? c.key.toUpperCase());
  return c.prefix ? [...comboCaps(prefix, prefix), caps] : [caps];
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
