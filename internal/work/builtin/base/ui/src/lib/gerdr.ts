// The bridge between this Work project and gerdr around it.
//
// - run(fn, input): call a local function (~/.gerdr/work/local/<file>.ts, "file.export"). Logic and
//   data access (MCP, APIs) live there, not in the page: the page only draws.
// - ask(text): hand a task to the Work agent on the right of the page (gerdr sends it as a message;
//   it queues if the agent is busy).
// - The page follows gerdr's light / dark theme by itself (see the listener at the bottom).
// - The menu's width is --gerdr-sidebar-w, which follows gerdr's sidebar: the user resizes them together by dragging the
//   menu's right edge (gerdr draws that handle over this page). Keep using the variable for the menu width.

/** The project this Work site belongs to: it's served at /work/<project>/. */
export const project = decodeURIComponent(location.pathname.split("/")[2] || "default");

export async function run<T = any>(fn: string, input?: unknown): Promise<T> {
  const res = await fetch("/api/work/run", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", "X-Gerdr": "1" },
    body: JSON.stringify({ project, fn, input }),
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data?.error || res.statusText);
  return data.result as T;
}

export function ask(text: string) {
  window.parent.postMessage({ type: "gerdr:ask", text }, location.origin);
}

/** Opens gerdr's settings (e.g. "mcp" to sign in to an MCP server). */
export function openSettings(tab?: string) {
  window.parent.postMessage({ type: "gerdr:settings", tab }, location.origin);
}

window.addEventListener("message", (e) => {
  if (e.origin !== location.origin) return;
  if (e.data?.type === "gerdr:theme") {
    document.documentElement.classList.toggle("dark", !!e.data.dark);
    document.documentElement.style.colorScheme = e.data.dark ? "dark" : "light";
  } else if (e.data?.type === "gerdr:sidebar" && typeof e.data.width === "number") {
    document.documentElement.style.setProperty("--gerdr-sidebar-w", `${e.data.width}px`);
  }
});
window.parent.postMessage({ type: "gerdr:ready" }, location.origin);
