// 命令面板里的命令 = 内置动作（同快捷键那张表）+ 插件注册的。
// 按键分发也在这里：前缀状态机、插件命令的键、直接组合键。
import { runAction } from "./actions";
import { ACTION_TITLES, DEFAULT_KEYS, isModifierOnly, matches, parseCombo, type KeyAction } from "./keys";
import { pluginCommands, runCommand } from "./plugins";
import { get, keyFor, set } from "./store";

export type CommandItem = { id: string; title: string; keys: string[]; source: "builtin" | string };

export function allCommands(): CommandItem[] {
  const cfg = get().config;
  const out: CommandItem[] = (Object.keys(ACTION_TITLES) as KeyAction[])
    .filter((a) => !/^tab\d$/.test(a))
    .map((a) => ({ id: a, title: ACTION_TITLES[a], keys: keyFor(cfg, a), source: "builtin" }));
  for (const c of pluginCommands) out.push({ id: c.id, title: c.title, keys: c.key ? [c.key] : [], source: c.plugin });
  return out;
}

export function runBuiltinCommand(id: string): boolean {
  if (id in DEFAULT_KEYS) {
    runAction(id as KeyAction);
    return true;
  }
  return false;
}

export { runCommand };

// ---- 按键分发 ----

type Binding = { combo: ReturnType<typeof parseCombo>; run: () => void };

function bindings(): Binding[] {
  const cfg = get().config;
  const out: Binding[] = [];
  for (const a of Object.keys(DEFAULT_KEYS) as KeyAction[]) {
    for (const k of keyFor(cfg, a)) {
      const combo = parseCombo(k);
      if (combo) out.push({ combo, run: () => runAction(a) });
    }
  }
  // 插件的键排在后面：和内置的撞了，内置优先（插件想覆盖就让用户在设置里把内置那条改掉）。
  for (const c of pluginCommands) {
    if (!c.key) continue;
    const combo = parseCombo(c.key);
    if (combo) out.push({ combo, run: () => void runCommand(c.id) });
  }
  return out;
}

let prefixTimer: ReturnType<typeof setTimeout> | undefined;

function setPrefix(on: boolean) {
  clearTimeout(prefixTimer);
  set({ prefixActive: on });
  if (on) {
    // 进前缀时让输入框失焦：中文输入法开着的话，第二个键会先进输入法（keyCode 229，
    // preventDefault 都拦不住组字），表现是「prefix+v 打出了一个 v」。没有可编辑元素
    // 聚焦时输入法不接管按键。动作做完再把焦点还回去（gerdr:refocus）。
    (document.activeElement as HTMLElement | null)?.blur?.();
  } else {
    requestAnimationFrame(() => window.dispatchEvent(new Event("gerdr:refocus")));
  }
  // 进了前缀忘了按第二个键：8 秒后自动退出，别让下一次打字被吞。
  if (on) prefixTimer = setTimeout(() => setPrefix(false), 8000);
}

// 设置里录快捷键时，按键整个交给录键的那边：不然按 ⌘D 想录下来，先被当成「分屏」执行了。
let capture: ((e: KeyboardEvent) => void) | null = null;
export function captureKeys(f: (e: KeyboardEvent) => void) {
  capture = f;
  return () => {
    if (capture === f) capture = null;
  };
}

export function handleKeydown(e: KeyboardEvent): boolean {
  if (isModifierOnly(e)) return false;
  if (capture && !e.isComposing) {
    e.preventDefault();
    e.stopPropagation();
    capture(e);
    return true;
  }
  const st = get();
  const prefix = parseCombo(st.config.prefix);

  if (st.prefixActive) {
    e.preventDefault();
    e.stopPropagation();
    setPrefix(false);
    if (e.key === "Escape") return true;
    // 前缀再按一次前缀：把它当普通按键还给输入框（比如真要输 ctrl+b）。终端拿这个事件把 ^B 发给 shell
    // （终端里跑着 tmux 时，按两下前缀就是 tmux 的前缀）。
    if (prefix && matches(prefix, e)) {
      window.dispatchEvent(new CustomEvent("gerdr:prefix-literal", { detail: e }));
      return true;
    }
    for (const b of bindings()) {
      if (b.combo?.prefix && matches(b.combo, e)) {
        b.run();
        return true;
      }
    }
    return true;
  }

  // 输入法正在组字时不抢键（候选框里的回车 / 方向键是给输入法的）。
  if (e.isComposing || e.keyCode === 229) return false;

  if (prefix && matches(prefix, e)) {
    e.preventDefault();
    e.stopPropagation();
    setPrefix(true);
    return true;
  }
  for (const b of bindings()) {
    if (b.combo && !b.combo.prefix && matches(b.combo, e)) {
      e.preventDefault();
      e.stopPropagation();
      b.run();
      return true;
    }
  }
  return false;
}
