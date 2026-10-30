import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { currentSpace } from "@/lib/layout";
import { get as getApp, set as setApp, setConfig, useStore } from "@/lib/store";
import { syncWatch } from "@/lib/sync";

// The Work view per project (a layout "space": its Code workspaces plus its own Work). Each project has
// a Work directory (default project: <GERDR_DIR>/work, others: <GERDR_DIR>/projects/<id>/work) with its
// Work site and local functions, and its own Work agent: an ordinary session opened in that directory.
// The current conversation and the agent new ones use are kept per project in config (shared by devices).

/** The project on screen. */
export const workProject = () => currentSpace(getApp().layout);
export const useWorkProject = () => useStore((s) => currentSpace(s.layout));

const dirs: Record<string, string> = {};
const dirP: Record<string, Promise<string>> = {};
export function workDir(project = workProject()): Promise<string> {
  if (dirs[project]) return Promise.resolve(dirs[project]);
  dirP[project] ??= api.work(project).then((r) => (dirs[project] = r.dir));
  return dirP[project];
}

/**
 * The project a Work agent conversation belongs to, undefined for any other session. By its working directory — the Work
 * directory of a project (<GERDR_DIR>/work for the default one, <GERDR_DIR>/projects/<name>/work, server work.Runner.Dir) —
 * so an old conversation is recognised too, not just each project's current one.
 */
export function workProjectOf(sessionId: string): string | undefined {
  const c = getApp().config;
  for (const [p, id] of Object.entries(c.workSessions ?? {})) if (id === sessionId) return p;
  if (c.workSession === sessionId) return "default";
  const cwd = getApp().sessions[sessionId]?.cwd;
  if (!cwd) return undefined;
  const known = Object.keys(dirs).find((p) => dirs[p] === cwd);
  if (known) return known;
  const root = getApp().pluginsDir.replace(/\/plugins\/?$/, ""); // <GERDR_DIR>/plugins
  if (!root || root === getApp().pluginsDir) return undefined;
  if (cwd === root + "/work" || cwd.startsWith(root + "/work/")) return "default";
  const m = cwd.slice(root.length).match(/^\/projects\/([^/]+)\/work(\/|$)/);
  return m ? m[1] : undefined;
}

/** The project's current Work conversation. The default project's used to live in config.workSession (before projects). */
export function workSessionOf(project: string): string | undefined {
  const c = getApp().config;
  return c.workSessions?.[project] ?? (project === "default" ? c.workSession : undefined);
}
export const useWorkSession = () =>
  useStore((s) => s.config.workSessions?.[currentSpace(s.layout)] ?? (currentSpace(s.layout) === "default" ? s.config.workSession : undefined));

function setWorkSession(project: string, id: string) {
  setConfig({ workSessions: { ...getApp().config.workSessions, [project]: id } });
  syncWatch();
}

/** The project's Work agent conversations (sessions opened in its Work directory), newest first. */
export function useWorkChats() {
  const project = useWorkProject();
  const [d, setD] = useState(dirs[project] ?? "");
  const sessions = useStore((s) => s.sessions);
  useEffect(() => {
    setD(dirs[project] ?? "");
    workDir(project).then(setD, () => {});
  }, [project]);
  return Object.values(sessions)
    .filter((s) => d && s.cwd === d)
    .sort((a, b) => b.updatedAt - a.updatedAt);
}

/** The agent new Work conversations in this project use: the one picked last in the chat header, else the global default. */
export function workAgent(project = workProject()): string {
  const c = getApp().config;
  const want = c.workAgents?.[project] ?? c.workAgent;
  const a = want && getApp().agents.find((x) => x.id === want && x.available);
  return a ? a.id : c.defaultAgent || "claude";
}

const creating: Record<string, Promise<string>> = {};

/**
 * Opens a fresh conversation for the project's Work agent and makes it the one on the right. Passing an
 * agent (an engine, or a role from Settings → Agent with its own prompt / skills / MCP / default model)
 * also makes it the project's default for later ones.
 */
export function newWorkChat(agent?: string, project = workProject()): Promise<string> {
  if (agent && agent !== getApp().config.workAgents?.[project]) setConfig({ workAgents: { ...getApp().config.workAgents, [project]: agent } });
  creating[project] ??= (async () => {
    const dir = await workDir(project);
    // No title: a given one counts as the user's (CustomTitle) and would stop the automatic naming Code sessions get (hub/title.go)
    const s = await api.createSession({ agent: agent || workAgent(project), cwd: dir });
    setApp((st) => ({ sessions: { ...st.sessions, [s.id]: s } }));
    setWorkSession(project, s.id);
    return s.id;
  })().finally(() => delete creating[project]);
  return creating[project];
}

export async function ensureWorkSession(project = workProject()): Promise<string> {
  const id = workSessionOf(project);
  if (id && getApp().sessions[id]) return id;
  return newWorkChat(undefined, project);
}

export function openWorkChat(id: string, project = workProject()) {
  setWorkSession(project, id);
}

/** Hand a task to the project's Work agent (opens one if there isn't any). The agent queues it if it's busy. Returns the conversation it went to. */
export async function askWorkAgent(text: string, project = workProject()): Promise<string> {
  const id = await ensureWorkSession(project);
  await api.send(id, [{ type: "text", text }]);
  return id;
}
