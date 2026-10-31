import { useEffect, useState } from "react";
import type { AvailableCommand } from "@gerdr/chat/types";
import { api } from "./api";

// A session's slash commands come as a key in its summary, not inline (hub cmdSets: the inline lists made the
// session list megabytes). Fetched here when a session is actually on screen, once per distinct list: most sessions
// of an agent share one.

const cache = new Map<string, AvailableCommand[]>();
const inflight = new Map<string, Promise<AvailableCommand[]>>();

function load(key: string) {
  let p = inflight.get(key);
  if (!p) {
    p = api
      .commandSet(key)
      .then((c) => (cache.set(key, c), c))
      .finally(() => inflight.delete(key));
    inflight.set(key, p);
  }
  return p;
}

/** The command list a summary's commandsKey stands for; undefined until it's in (or with no key). */
export function useCommandSet(key: string | undefined): AvailableCommand[] | undefined {
  const [, bump] = useState(0);
  useEffect(() => {
    if (!key || cache.has(key)) return;
    let live = true;
    load(key).then(
      () => live && bump((n) => n + 1),
      () => {}, // a key the server dropped: the next summary brings a fresh one
    );
    return () => {
      live = false;
    };
  }, [key]);
  return key ? cache.get(key) : undefined;
}
