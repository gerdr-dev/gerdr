import { get, setConfig, type Config } from "./store";
import type { AgentInfo, ConfigOption } from "./types";

// 新会话的默认模式 / 配置项（模型、思考强度…），按 agent id 存在 config.json：
//   defaultModes[agent]、defaultConfig[agent][配置项 id]；Gerdr Agent（native）的默认模型例外，就是 llm.defaultModel ——
//   模型设置里本来就有「默认模型」，两处各存一份会对不上。
// 真正套用在后端建会话时（hub.Create）：界面和 `gerdr session new` 开出来的一样；存着的值 agent 不认了就跳过，不报错。

export const MODE = "__mode"; // 模式在下拉框里的组 id（不是配置项）

/** 配置项的可选值摊平（有的 agent 按组报）。 */
export function flatOptions(o: ConfigOption) {
  const arr: any[] = o.options as any[];
  return arr.flatMap((x) => (x.options ? x.options : [x])) as { value: string; name: string; description?: string }[];
}

// The mode a new session starts in when Settings has none for its agent, by engine: mirrors hub.BuiltinMode
// (internal/hub/hub.go), which is what the backend applies, so the picker shows the mode sessions really get.
const BUILTIN_MODES: Record<string, string> = { claude: "auto", native: "bypassPermissions" };

/** Use the engine's cached options for roles that have yet to open a session. */
export function optionsOf(agents: AgentInfo[], id: string, base: string) {
  return agents.find((a) => a.id === id)?.options ?? agents.find((a) => a.id === base)?.options;
}

/** Effective new-session defaults, including the built-in fallbacks applied by the hub. */
export function defaultsOf(agent: string): Record<string, string> {
  const { agents, config: c } = get();
  const out: Record<string, string> = { ...(c.defaultConfig?.[agent] ?? {}) };
  if (agent === "native" && c.llm?.defaultModel) out.model = c.llm.defaultModel;
  const base = c.agents?.find((d) => d.id === agent)?.base ?? agent;
  const mode = c.defaultModes[agent] || BUILTIN_MODES[base];
  if (mode) out[MODE] = mode;
  if (out.effort === undefined && (base === "claude" || base === "codex" || base === "native")) {
    const config = optionsOf(agents, agent, base)?.config;
    const effort = config?.find((option) => option.id === "effort");
    if (!config || (effort && flatOptions(effort).some((option) => option.value === "medium"))) out.effort = "medium";
  }
  return out;
}

/** 设 / 清（值为空）几项默认值，一次写进 config.json（分几次写，几个请求到后端的先后不一定）。
 *  extra：要一起写的别的字段（保存角色时连 agents 一起）。 */
export function setDefaults(agent: string, values: Record<string, string>, extra?: Partial<Config>) {
  const c = get().config;
  const modes = { ...c.defaultModes };
  let llm = c.llm;
  const all = { ...(c.defaultConfig ?? {}) };
  const one = { ...(all[agent] ?? {}) };
  for (const [key, value] of Object.entries(values)) {
    if (key === MODE) {
      if (value) modes[agent] = value;
      else delete modes[agent];
    } else if (agent === "native" && key === "model") {
      llm = { ...llm, defaultModel: value || undefined };
    } else if (value) one[key] = value;
    else delete one[key];
  }
  if (Object.keys(one).length) all[agent] = one;
  else delete all[agent];
  setConfig({ ...extra, defaultModes: modes, defaultConfig: all, llm });
}

export const setDefault = (agent: string, key: string, value: string) => setDefaults(agent, { [key]: value });

/** 删角色时连它的默认值一起删：返回要并进同一次 setConfig 的那部分。 */
export function withoutDefaults(agent: string) {
  const c = get().config;
  const defaultModes = { ...c.defaultModes };
  delete defaultModes[agent];
  const defaultConfig = { ...(c.defaultConfig ?? {}) };
  delete defaultConfig[agent];
  return { defaultModes, defaultConfig };
}
