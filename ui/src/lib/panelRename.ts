import { api } from "@/lib/api";
import { renamePanelItems } from "@/lib/inbox";
import { get as getApp, setWorkPanel } from "@/lib/store";

/**
 * Panels whose directory was renamed (server work/rename.go: `gerdr panel rename --dir`), old key → new key. Their
 * key changed, and this device keeps things by it: which panel each project shows, a panel's last path, its folded
 * agent column, the inbox's items (`pan:<key>:<id>`) and the ones ×'d. They move over when the server says so (the
 * "panel_renamed" message, before it moves the inbox) or, for a device that was away, when the page opens
 * (loadRenamed, before the inbox's first fetch). Moving is idempotent: a key that is already new is left alone.
 * Cards already in the chats keep the old key they were stamped with; canonPanel finds the panel they belong to.
 */
const aliases = new Map<string, string>();
const subs = new Set<() => void>();

export const onPanelAliases = (f: () => void) => (subs.add(f), () => void subs.delete(f));

/** The key a panel goes by now. */
export function canonPanel(key: string): string {
  for (let i = 0; i < 8 && aliases.has(key); i++) key = aliases.get(key)!;
  return key;
}

const json = <T>(k: string, d: T): T => {
  try {
    return (JSON.parse(localStorage.getItem(k) ?? "null") as T) ?? d;
  } catch {
    return d;
  }
};

// Synchronous: the "inbox" message right behind "panel_renamed" must find the items already moved
export function applyPanelRename(from: string, to: string) {
  if (!from || !to || from === to || aliases.get(from) === to) return;
  aliases.set(from, to);
  const [space, oldName] = from.split("/");
  const newName = to.split("/")[1] ?? "";
  try {
    if (getApp().workPanels[space] === oldName) setWorkPanel(space, newName);
    const path = localStorage.getItem(`gerdr.workPath.${from}`);
    if (path != null) {
      if (localStorage.getItem(`gerdr.workPath.${to}`) == null) localStorage.setItem(`gerdr.workPath.${to}`, path);
      localStorage.removeItem(`gerdr.workPath.${from}`);
    }
    const hidden = json<Record<string, boolean>>("gerdr.workChatHidden", {});
    if (from in hidden) {
      hidden[to] ??= hidden[from];
      delete hidden[from];
      localStorage.setItem("gerdr.workChatHidden", JSON.stringify(hidden));
    }
  } catch {}
  renamePanelItems(from, to);
  for (const f of subs) f();
}

let loaded: Promise<void> | undefined;

/** Every project's renamed panels, applied once per page: before the inbox's first fetch (lib/inbox.ts). */
export function loadRenamed(): Promise<void> {
  loaded ??= api.renamedPanels().then(
    (m) => {
      for (const [from, to] of Object.entries(m ?? {})) applyPanelRename(from, to);
    },
    () => {}, // an older backend: nothing was ever renamed
  );
  return loaded;
}
