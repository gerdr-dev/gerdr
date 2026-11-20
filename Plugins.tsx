// Plugins in the nsh UI. A release nsh bundle runs no code it downloads, so a plugin can't live in this VM: each is an
// nsh app of its own (plugins/<id>/), shown where the UI puts a <PluginSlot> by the native component `gerdr.plugin`
// that Gerdr's Android app registers (gerdr.worktrees/nsh-android NshPlugins.kt). That host runs the plugin in a
// view of its own and relays: the slot's `ctx` goes to the plugin, what the plugin asks of the UI comes back here.
//
// Where plugins come from: GET /plugins.json from the computer (tools/token-helper.py, over adb reverse; Gerdr's
// backend in a real build): [{ id, src }] with src "dev:<ws>" (the plugin's own `nsh dev`, hot updates) or
// "bundle:<http>#v=<version>" (its built bundle; a new version is downloaded and run within seconds).
import { signal, host, nativeComponent, platform } from "nsh";
import { api } from "./store";

export type Plugin = { id: string; src: string };
export const plugins = signal<Plugin[]>([]);
export const pluginError = signal("");

// Only where a host draws plugins (Gerdr's Android app); the Mac's dev host has no `gerdr.plugin`
const hosted = () => platform.os === "android" && host.available();

let loaded = false;
export function loadPlugins() {
  if (loaded || !hosted()) return;
  loaded = true;
  // Followed every 2 s: a plugin's new build changes its src ("#v=…"), and its slots load it again right away
  const poll = () =>
    fetch("http://127.0.0.1:7899/plugins.json")
      .then((r) => r.json())
      .then((list: Plugin[]) => {
        if (JSON.stringify(list) !== JSON.stringify(plugins())) plugins.set(list);
      })
      .catch((e) => pluginError.set(`plugins: ${e?.message ?? e}`));
  poll();
  setInterval(poll, 2000);
  host.on<{ plugin: string; message: string }>("plugin.error", (e) => {
    pluginError.set(`${e.plugin}: ${e.message}`);
    setTimeout(() => pluginError() === `${e.plugin}: ${e.message}` && pluginError.set(""), 6000);
  });
}

// A plugin's settings are its Gerdr add-on's (same id): GET /api/addons → settingValues
const settingsOf = signal<Record<string, Record<string, unknown>>>({});
let settingsAsked = false;
export function pluginSettings(id: string) {
  if (!settingsAsked) {
    settingsAsked = true;
    api("GET", "/api/addons").then((list) => {
      const out: Record<string, Record<string, unknown>> = {};
      for (const a of Array.isArray(list) ? list : (list?.addons ?? [])) out[a.id] = a.settingValues ?? {};
      settingsOf.set(out);
    });
  }
  return settingsOf()[id] ?? {};
}

/** Composer edits plugins ask for, by slot: a composer listens to its own slot's */
const editors = new Map<string, (text: string) => void>();
let listening = false;
export function onComposerEdit(slot: string, set: (text: string) => void) {
  if (!listening) {
    listening = true;
    host.on<{ slot: string; op: string; text: string }>("plugin.composer", (e) => {
      if (e.op === "set") editors.get(e.slot)?.(e.text);
    });
  }
  editors.set(slot, set);
  return () => editors.delete(slot);
}

export type SlotCtx = { session: string; text: string; lastReply: string; settings: Record<string, unknown>; hints: string[] };

export const PluginSlot = nativeComponent<{ plugin: string; slot: string; src: string; ctx: SlotCtx }>("gerdr.plugin", {
  props: ["plugin", "slot", "src", "ctx"],
});

export { hosted as pluginsHosted };
