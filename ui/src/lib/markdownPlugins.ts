import { useEffect, useSyncExternalStore } from "react";
import type { PluginConfig } from "streamdown";

/**
 * Streamdown plugins for the Markdown in replies and .md files (ChatHost.useMarkdownPlugins, FileViewer): math,
 * $$ … $$ drawn by KaTeX (the official @streamdown/math). Before it a formula showed as its TeX source. (```mermaid
 * is drawn by components/chat/MermaidBlock.tsx instead; lib/mermaid.ts says why.)
 *
 * KaTeX is heavy (code and fonts), so it is imported the first time some Markdown on the page has a formula, not
 * with the page: until then that Markdown draws as before, then once more with it. Markdown without math never
 * gets the plugin, so its arrival doesn't redraw the rest of the conversation.
 */

type MathMod = typeof import("@streamdown/math");
let math: MathMod | undefined;
let loading = false;

// Only $$ … $$ is math: @streamdown/math leaves single dollars alone (singleDollarTextMath off), so "$5 or $10" in a
// sentence stays text
const hasMath = (md: string) => md.includes("$$");

const subs = new Set<() => void>();
const subscribe = (f: () => void) => (subs.add(f), () => void subs.delete(f));

function load() {
  if (math || loading) return;
  loading = true;
  import("@streamdown/math").then(
    (m) => {
      katexStyles();
      math = m;
      subs.forEach((f) => f());
    },
    (e) => {
      // Offline, or the CDN failing: keep showing the source, try again a little later rather than on every render
      console.warn("[gerdr] couldn't load the math plugin for Markdown", e);
      setTimeout(() => (loading = false), 30_000);
    },
  );
}

/**
 * KaTeX's stylesheet (layout and fonts of the formulas). Code here can't import CSS, so it goes in as a <link> to the
 * package through gerdr's /_esm/ proxy, which also serves the fonts it points at. 0.16 is the range @streamdown/math
 * gets KaTeX in (rehype-katex ^0.16), so this resolves to the same KaTeX as the code.
 */
function katexStyles() {
  if (document.querySelector("link[data-gerdr-katex]")) return;
  const l = document.createElement("link");
  l.rel = "stylesheet";
  l.href = "/_esm/katex@0.16/dist/katex.min.css";
  l.dataset.gerdrKatex = "";
  document.head.appendChild(l);
}

let withMath: PluginConfig | undefined;

/** ChatHost.useMarkdownPlugins: math for Markdown that has some, once KaTeX is in. */
export function useMarkdownPlugins(md: string): PluginConfig | undefined {
  const m = useSyncExternalStore(subscribe, () => math);
  const want = hasMath(md);
  useEffect(() => {
    if (want) load();
  }, [want]);
  if (!want || !m) return undefined;
  return (withMath ??= { math: m.math });
}

// One number per plugin object: a key for a MessageResponse that changes exactly when its plugin objects do (it
// redraws only when its text changes). The chat's Markdown keeps its own copy: the package can't import gerdr's src/.
const pluginIds = new WeakMap<object, number>();
let nextPluginId = 0;
export const pluginsKey = (plugins?: PluginConfig) =>
  Object.values(plugins ?? {})
    .map((p) => {
      if (!pluginIds.has(p)) pluginIds.set(p, ++nextPluginId);
      return pluginIds.get(p);
    })
    .join();
